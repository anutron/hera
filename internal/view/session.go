package view

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/coder/websocket"
	"github.com/gdamore/tcell/v2"

	"github.com/anutron/argus-sdk/pluginview"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
	"github.com/anutron/hera/internal/view/ops"
)

// NewSessionFunc returns a SessionFunc that drives one hera-view session
// per accepted WebSocket connection. It composes the four substrates the
// previous stages produced:
//
//   - argus-sdk/pluginview translates WebSocket frames to a
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
// or the WebSocket peer closes. On exit it tears down the pluginview, the
// tview Application, and every pane subscription owned by this session;
// the daemon-level proxy subscriptions survive.
func NewSessionFunc(database *db.DB, manager *ProxyManager, client *argus.Client, states *ArgusStateCache, log *slog.Logger) SessionFunc {
	if log == nil {
		log = slog.Default()
	}
	return func(ctx context.Context, conn *websocket.Conn) {
		scr, err := pluginview.New(ctx, conn)
		if err != nil {
			log.Warn("view: pluginview construct", "err", err)
			return
		}

		src := PaneSource(nilPaneSource{})
		if manager != nil {
			src = managerPaneSource{mgr: manager, ctx: ctx, states: states}
		}

		app, err := BuildApp(database, src)
		if err != nil {
			log.Warn("view: build app", "err", err)
			scr.Fini()
			return
		}
		defer app.Close()

		// Bind the key-surrender control sender to this session's conn
		// (D12). coder/websocket's Conn.Write serializes writers, so these
		// TEXT control frames coexist with the SDK's binary surface writes
		// on the same conn without an extra mutex. The App pushes a
		// focus-aware hotkeys frame on connect + every focus change; the
		// router sends release on Esc-from-RAIL; the mutation bridge sends
		// help on ?-from-RAIL.
		control := newViewControl(ctx, conn)
		app.SetControl(control)

		// Subscribe the rail to the DAO broadcaster so any orchestrator /
		// role / binding write triggers a debounced rail refresh on this
		// session's tview event loop. RepopulateRail wraps the rebuild in
		// QueueUpdateDraw, so the refresher's goroutine can fire it
		// without crossing the tview-single-threaded boundary itself.
		rail := NewRailRefresher(database.Events, app.RepopulateRail)
		defer rail.Stop()

		// Also repaint the rail when argus task state changes (status /
		// idle / needs-input / archive) so it tracks argus reality live,
		// not only hera DB events. The cache coalesces + polls, so an
		// undebounced repaint per signal is fine.
		if states != nil {
			stateCh, cancelStateSub := states.Subscribe()
			defer cancelStateSub()
			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					case <-stateCh:
						app.RepopulateRail()
					}
				}
			}()
		}

		tApp := app.Application()
		// SetScreen calls scr.Init() before Run sees it; subsequent Run()
		// will use this screen rather than building a default tcell one.
		tApp.SetScreen(scr)

		// Build the rail-mutation surface: ops.Service over a *db.DB
		// adapter + argus client adapter, wrapped by a bridge that
		// drives input / confirm / help modals through the App.
		opsService := ops.NewService(
			newDBAdapter(database),
			newArgusAdapter(client),
			ops.ExecWorktreeRemover{},
			slogLogger{log: log},
		)
		bridge := newMutationBridge(ctx, app, app, opsService, opsService.ListAll, app, control, log)

		focus := NewFocusMachine()
		// Let the App flip the focus machine's coordPresent flag when it
		// enters/leaves freelance (full-width) mode.
		app.SetFocusMachine(focus)
		router := &KeyRouter{
			Focus:      focus,
			Targets:    app,
			Poster:     client,
			Mutations:  bridge,
			Border:     app,
			RailSelect: app,
			Modal:      app,
			Control:    control,
			Ctx:        ctx,
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
	mgr    *ProxyManager
	ctx    context.Context
	states *ArgusStateCache
}

// TaskState satisfies view.TaskStateProvider, exposing the daemon's argus
// state cache to the rail so icons + archived hiding reflect argus reality.
func (p managerPaneSource) TaskState(taskID string) (ArgusTaskState, bool) {
	if p.states == nil || taskID == "" {
		return ArgusTaskState{}, false
	}
	return p.states.Get(taskID)
}

// StatesReady reports whether the argus state cache has completed its first
// poll, so a cache miss can be safely treated as "task gone" rather than
// "cache cold". Returns false (not ready) when no cache is wired.
func (p managerPaneSource) StatesReady() bool {
	return p.states != nil && p.states.Ready()
}

// LiveTasks satisfies view.FreelanceProvider, exposing the cache's full
// argus task snapshot so the rail can compute the Freelance section. Returns
// nil when no cache is wired (the rail then renders no freelancers).
func (p managerPaneSource) LiveTasks() []ArgusTaskInfo {
	if p.states == nil {
		return nil
	}
	return p.states.List()
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

// TaskSize delegates to the proxy manager, which queries argus for the
// worker PTY's cols/rows.
func (p managerPaneSource) TaskSize(taskID string) (int, int) {
	if p.mgr == nil || taskID == "" {
		return 0, 0
	}
	return p.mgr.TaskSize(p.ctx, taskID)
}

// ResizeTask delegates to the proxy manager, which asks argus to resize
// the worker PTY to (cols, rows). The manager dispatches the call on a
// goroutine bound by p.ctx, so Draw paths stay non-blocking.
func (p managerPaneSource) ResizeTask(taskID string, cols, rows int) {
	if p.mgr == nil || taskID == "" {
		return
	}
	p.mgr.ResizeTask(p.ctx, taskID, cols, rows)
}

// IsTaskAlive delegates to the proxy manager, which calls argus's
// GET /api/tasks/{id} and classifies the returned Status. Satisfies
// view.TaskAliveChecker so findInitialSelection can filter recently-
// completed bindings from initial pane selection.
func (p managerPaneSource) IsTaskAlive(taskID string) bool {
	if p.mgr == nil || taskID == "" {
		return false
	}
	return p.mgr.IsTaskAlive(p.ctx, taskID)
}

// slogLogger adapts *slog.Logger to the ops.Logger interface. The ops
// package only needs Printf; we map every audit-log line to a single
// Info-level event under the "view.ops" prefix.
type slogLogger struct{ log *slog.Logger }

func (l slogLogger) Printf(format string, args ...any) {
	if l.log == nil {
		return
	}
	l.log.Info("view.ops", "msg", fmt.Sprintf(format, args...))
}
