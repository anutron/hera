package view

import (
	"context"
	"io"
	"log/slog"

	"github.com/coder/websocket"
	"github.com/gdamore/tcell/v2"

	"github.com/anutron/hera/internal/db"
	"github.com/anutron/hera/internal/view/screen"
)

// NewSessionFunc returns a SessionFunc that drives one hera-view session
// per accepted WebSocket connection. It composes the four substrates the
// previous stages produced:
//
//   - Stage D's wsscreen.Screen translates WebSocket frames to a
//     tcell.Screen.
//   - Stage F's BuildApp constructs a tview.Application with the three-
//     column layout, populated from the DB and (where present) from a
//     PaneSource over the daemon's PTY proxy.
//   - Stage G's KeyRouter is attached via SetInputCapture so focus moves
//     and pane-focus keystrokes route to the right argus task's /input
//     endpoint via the supplied InputPoster.
//   - Stage J's ProxyManager is adapted into a PaneSource so the same
//     ring buffers seeded at daemon startup feed every connection's
//     panes.
//
// The function blocks until ctx is cancelled (last-writer-wins supersede)
// or the WebSocket peer closes. On exit it tears down the wsscreen, the
// tview Application, and every pane subscription owned by this session;
// the daemon-level proxy subscriptions survive.
func NewSessionFunc(database *db.DB, manager *ProxyManager, poster InputPoster, log *slog.Logger) SessionFunc {
	if log == nil {
		log = slog.Default()
	}
	return func(ctx context.Context, conn *websocket.Conn) {
		scr, err := screen.New(ctx, conn)
		if err != nil {
			log.Warn("view: wsscreen construct", "err", err)
			return
		}

		src := PaneSource(nilPaneSource{})
		if manager != nil {
			src = managerPaneSource{mgr: manager, ctx: ctx}
		}

		app, err := BuildApp(database, src)
		if err != nil {
			log.Warn("view: build app", "err", err)
			scr.Fini()
			return
		}
		defer app.Close()

		tApp := app.Application()
		// SetScreen calls scr.Init() before Run sees it; subsequent Run()
		// will use this screen rather than building a default tcell one.
		tApp.SetScreen(scr)

		focus := NewFocusMachine()
		router := &KeyRouter{
			Focus:   focus,
			Targets: app,
			Poster:  poster,
			Border:  app,
			Ctx:     ctx,
		}
		// Paint the initial RAIL border so the operator sees focus on first
		// frame.
		app.OnFocusChanged(focus.State())
		tApp.SetInputCapture(router.HandleKey)

		// Bridge ctx cancellation into tview's event loop by queueing an
		// EventError. tview's EventLoop handles EventError by calling
		// Application.Stop inline, which keeps the Stop / Run.cleanup
		// pair on the same goroutine — calling tApp.Stop directly from
		// our supervisor races with Run's unprotected a.screen=nil
		// cleanup write under -race (a known tview implementation
		// quirk).
		stopQueued, stopQueuedCancel := context.WithCancel(context.Background())
		go func() {
			select {
			case <-stopQueued.Done():
			case <-ctx.Done():
				tApp.QueueEvent(tcell.NewEventError(io.EOF))
			}
		}()

		runErr := tApp.Run()
		stopQueuedCancel()
		if runErr != nil && runErr != io.EOF {
			log.Warn("view: tview run", "err", runErr)
		}
		scr.Fini()
	}
}

// managerPaneSource adapts the daemon's ProxyManager to the PaneSource
// interface BuildApp expects. SubscribeTask reuses the long-lived
// per-task Subscription (seeded at daemon startup) and registers a new
// fan-out Listener for this session.
type managerPaneSource struct {
	mgr *ProxyManager
	ctx context.Context
}

// SubscribeTask returns the live ring snapshot and the per-listener byte
// channel for taskID. unsub releases the listener registration; the
// underlying upstream Subscription outlives the session.
func (p managerPaneSource) SubscribeTask(taskID string) ([]byte, <-chan []byte, func()) {
	if p.mgr == nil || taskID == "" {
		return nil, nil, nil
	}
	sub := p.mgr.Ensure(p.ctx, taskID)
	lst := sub.Subscribe()
	return lst.Snapshot, lst.Bytes, func() { sub.Unsubscribe(lst) }
}
