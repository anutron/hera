package view

import (
	"context"
	"log/slog"

	"github.com/coder/websocket"

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

		// When ctx fires (Stop or supersede), gracefully stop the tview
		// event loop. tApp.Run returns shortly after.
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			<-ctx.Done()
			tApp.Stop()
		}()

		if err := tApp.Run(); err != nil {
			log.Warn("view: tview run", "err", err)
		}
		// Drain the supervisor goroutine so we don't leave it dangling when
		// the peer closed the conn before ctx fired.
		select {
		case <-stopped:
		default:
			tApp.Stop()
			<-stopped
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
