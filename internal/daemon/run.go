package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/config"
	"github.com/anutron/hera/internal/db"
	"github.com/anutron/hera/internal/events"
	"github.com/anutron/hera/internal/idle"
	"github.com/anutron/hera/internal/inject"
	"github.com/anutron/hera/internal/mcp"
	"github.com/anutron/hera/internal/settings"
	"github.com/anutron/hera/internal/view"
)

// Plugin-view registration parameters. Title is shown in argus's UI;
// the hotkey is the argus-side keyboard shortcut the operator presses
// to open hera-view (tentative `Ctrl-H` per design D8 / Open Questions).
const (
	viewTitle  = "Hera"
	viewHotkey = "ctrl+h"
)

// Daemon bundles every running component so the caller can introspect or
// shut them down individually. The Run helper composes one and returns
// it (mostly useful for tests; production callers use Run() and forget).
type Daemon struct {
	Cfg               *config.Config
	Log               *slog.Logger
	DB                *db.DB
	Argus             *argus.Client
	Ports             *argus.PortsClient
	IdleTrack         *idle.Tracker
	Injector          *inject.Injector
	DeliveryWatcher   *inject.DeliveryWatcher
	MCPServer         *mcp.Server
	Registrar         *mcp.Registrar
	SettingsRegistrar *settings.Registrar
	ViewRegistrar     *view.Registrar
	ViewServer        *view.Server
	ViewProxy         *view.ProxyManager
	viewProxyCancel   context.CancelFunc
	Watcher           *argus.Watcher
	Subscriber        *events.Subscriber

	periodicCancel context.CancelFunc
	periodicDone   chan struct{}
	doorbellCancel context.CancelFunc
	doorbellDone   chan struct{}
}

// Start assembles hera and brings every subsystem up. Returns the live
// Daemon ready for Run to call. Use only when you want to inspect each
// subsystem (tests, custom orchestration). For normal operation, call
// Run(ctx, cfg, log).
func Start(ctx context.Context, cfg *config.Config, log *slog.Logger) (*Daemon, error) {
	if cfg == nil {
		cfg = config.Default()
	}
	if log == nil {
		log = slog.Default()
	}

	if err := cfg.EnsureStateDir(); err != nil {
		return nil, fmt.Errorf("hera: state dir: %w", err)
	}
	token, err := cfg.LoadToken()
	if err != nil {
		return nil, err
	}
	database, err := db.Open(cfg.StatePath())
	if err != nil {
		return nil, fmt.Errorf("hera: open db: %w", err)
	}

	// Override Config defaults with persisted settings (if any) before
	// instantiating Tracker and Injector, so they see the saved values.
	if err := LoadPersistedSettings(ctx, cfg, database.Config); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("hera: load persisted settings: %w", err)
	}

	// Discover argus's REST port before constructing the HTTP client.
	// argus picks its REST port dynamically via bindWithRetry, so the
	// daemon socket is the only authoritative source on every boot.
	// A failure here is fatal: hera cannot operate without argus.
	ports := argus.NewPortsClient(cfg.ArgusSocketPath)
	discoverCtx, discoverCancel := context.WithTimeout(ctx, 5*time.Second)
	apiPort, _, err := ports.Ports(discoverCtx)
	discoverCancel()
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("hera: argus socket Ports: %w", err)
	}
	argusBaseURL := fmt.Sprintf("http://127.0.0.1:%d", apiPort)

	client := argus.New(argusBaseURL, token)

	tracker := idle.NewWithDebounce(cfg.IdleDebounce)
	injector := inject.New(client, tracker)
	injector.SetAutoInjectEnabled(cfg.AutoInjectEnabled)
	dw := inject.NewDeliveryWatcher(
		database.Messages,
		database.Bindings,
		client,
		cfg.NudgeAfter,
		cfg.NudgeEvery,
		cfg.MaxNudges,
		log,
	)
	resolver := mcp.NewResolver(client, database)

	auth, err := mcp.GenerateAuthHeader()
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("hera: generate mcp auth: %w", err)
	}

	mcpSrv := mcp.NewServer(cfg.ListenAddr, auth, log)

	// Mount the plugin-view WebSocket route on the same listener as MCP
	// callbacks. Mount MUST happen before mcpSrv.Start, since Start is
	// what builds the ServeMux and binds the listener.
	//
	// The session runner is filled in below once the proxy manager exists.
	viewSrv := view.NewServer(log, nil)
	mcpSrv.Mount("/view", viewSrv.Handler())

	if err := mcpSrv.Start(ctx); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("hera: start mcp server: %w", err)
	}

	// Wire handlers.
	mcpSrv.RegisterHandler("hera_new_orchestrator", mcp.NewNewOrchestratorHandler(resolver, database, client))
	mcpSrv.RegisterHandler("hera_join", mcp.NewJoinHandler(resolver, database, client))
	mcpSrv.RegisterHandler("hera_send", mcp.NewSendHandler(resolver, database, injector))
	mcpSrv.RegisterHandler("hera_inbox", mcp.NewInboxHandler(resolver, database))
	mcpSrv.RegisterHandler("hera_mark_read", mcp.NewMarkReadHandler(resolver, database))
	mcpSrv.RegisterHandler("hera_status", mcp.NewStatusHandler(resolver, database, client))
	mcpSrv.RegisterHandler("settings_save", mcp.NewSettingsSaveHandler(database.Config, tracker, injector))

	// CallbackBaseURL is the actual bound address (honors :0).
	callback := "http://" + mcpSrv.Addr()
	registrar := mcp.NewRegistrar(client, callback, auth, log)
	registrar.SetHeartbeat(cfg.MCPHeartbeat)
	for _, def := range toolDefinitions() {
		registrar.Add(def)
	}
	if err := registrar.Start(ctx); err != nil {
		_ = mcpSrv.Stop()
		_ = database.Close()
		return nil, fmt.Errorf("hera: register tools: %w", err)
	}

	// Register hera's settings-section with argus on the same callback
	// listener + shared secret as the MCP tools. The section definition
	// (callback URL, fields, descriptions) lives in settings.HeraSection.
	settingsReg := settings.NewRegistrar(client, log)
	settingsReg.SetHeartbeat(cfg.MCPHeartbeat)
	settingsReg.Add(settings.HeraSection(auth))
	if err := settingsReg.Start(ctx); err != nil {
		unregCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = registrar.Stop(unregCtx)
		cancel()
		_ = mcpSrv.Stop()
		_ = database.Close()
		return nil, fmt.Errorf("hera: register settings section: %w", err)
	}

	// Register the plugin view with argus on the same callback listener.
	// Callback is the ws:// scheme on the same address; argus dials this
	// URL on hotkey press to open the per-connection rendering session.
	viewCallback := "ws://" + mcpSrv.Addr() + "/view"
	viewReg := view.NewRegistrar(client, viewTitle, viewHotkey, viewCallback, log)
	viewReg.SetHeartbeat(cfg.MCPHeartbeat)
	if err := viewReg.Start(ctx); err != nil {
		unregCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = settingsReg.Stop(unregCtx)
		_ = registrar.Stop(unregCtx)
		cancel()
		_ = mcpSrv.Stop()
		_ = database.Close()
		return nil, fmt.Errorf("hera: register plugin view: %w", err)
	}

	// Walk live bindings and seed the PTY proxy with one
	// snapshot+SSE subscription per binding. Subscriptions outlive any
	// single view session — they buffer bytes into in-memory rings the
	// per-connection runner reads from.
	proxyCtx, proxyCancel := context.WithCancel(context.Background())
	viewProxy := view.NewProxyManager(proxyCtx, client, log)
	live, err := database.Bindings.ListLive(ctx)
	if err != nil {
		proxyCancel()
		unregCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = viewReg.Stop(unregCtx)
		_ = settingsReg.Stop(unregCtx)
		_ = registrar.Stop(unregCtx)
		cancel()
		_ = mcpSrv.Stop()
		_ = database.Close()
		return nil, fmt.Errorf("hera: list live bindings: %w", err)
	}
	taskIDs := make([]string, 0, len(live))
	for _, b := range live {
		taskIDs = append(taskIDs, b.ArgusTaskID)
	}
	viewProxy.Seed(taskIDs)

	// Poll argus's task list into a state cache so the rail can render each
	// task's real status / idle / needs-input / archived without blocking
	// the tview loop. Daemon-lifetime (bound to proxyCtx); the per-session
	// runner reads it through the PaneSource.
	argusState := view.NewArgusStateCache(client, view.DefaultArgusPollInterval, log)
	go argusState.Run(proxyCtx)

	// Now that the proxy manager exists, swap the per-connection session
	// runner so each accepted WebSocket gets a real wsscreen + tview
	// surface (Stages D + F + G stitched together).
	viewSrv.SetRunner(view.NewSessionFunc(database, viewProxy, client, argusState, log))

	resyncHandler := events.NewResyncHandler(client, database, log)
	if err := resyncHandler.Reconcile(ctx); err != nil {
		log.Warn("boot reconcile failed", "err", err)
	}
	subscriber := events.NewSubscriber(client, database, log)
	subscriber.Register(events.NewAdoptHandler(client, database, log))
	subscriber.Register(resyncHandler)
	subscriber.Register(tracker) // tracker implements events.Handler

	// Subscriber runs in its own goroutine; Run() blocks on ctx in main.
	go func() {
		if err := subscriber.Run(ctx); err != nil && ctx.Err() == nil {
			log.Warn("event subscriber exited", "err", err)
		}
	}()

	// Defensive periodic reconciler — fires Reconcile on its own ticker
	// in addition to the SSE path so a silently-missed archive event
	// still gets caught within one tick. Lifecycle is tied to its own
	// context so Stop can cancel + wait for clean exit independent of
	// the parent ctx.
	periodicCtx, periodicCancel := context.WithCancel(context.Background())
	periodicDone := make(chan struct{})
	periodic := events.NewPeriodicReconciler(resyncHandler, cfg.ReconcileInterval, log)
	go func() {
		defer close(periodicDone)
		periodic.Run(periodicCtx)
	}()

	// Delivery watcher — re-nudges unread idle_submit messages with a
	// non-duplicating doorbell until the recipient confirms receipt via
	// read_at or the nudge cap is reached.
	doorbellCtx, doorbellCancel := context.WithCancel(context.Background())
	doorbellDone := make(chan struct{})
	go func() {
		defer close(doorbellDone)
		dw.Run(doorbellCtx)
	}()

	// Wire recovery: the watcher fires on pid-mtime change or socket-ping
	// failure; the registrar heartbeat fires the same callback as a
	// passive fallback on 404 responses.
	recover := argus.RecoverFunc(ports, client, registrar, settingsReg, log)
	registrar.SetOnHeartbeat404(recover)

	watcher := &argus.Watcher{
		PidPath:   cfg.ArgusPIDPath,
		Ping:      ports.Ping,
		Interval:  argus.DefaultWatcherInterval,
		OnRestart: recover,
		Log:       log,
	}
	watcher.Start(ctx)

	log.Info("hera ready",
		"argus_base_url", argusBaseURL,
		"mcp_addr", mcpSrv.Addr(),
		"state", cfg.StatePath(),
	)

	return &Daemon{
		Cfg: cfg, Log: log, DB: database, Argus: client, Ports: ports,
		IdleTrack: tracker, Injector: injector, DeliveryWatcher: dw,
		MCPServer: mcpSrv, Registrar: registrar,
		SettingsRegistrar: settingsReg, Watcher: watcher,
		Subscriber:      subscriber,
		ViewRegistrar:   viewReg,
		ViewServer:      viewSrv,
		ViewProxy:       viewProxy,
		viewProxyCancel: proxyCancel,
		periodicCancel:  periodicCancel,
		periodicDone:    periodicDone,
		doorbellCancel:  doorbellCancel,
		doorbellDone:    doorbellDone,
	}, nil
}

// Stop gracefully shuts every subsystem down in reverse order. Safe to
// call multiple times. The unregister loop is bounded to 10 seconds so a
// stuck argus doesn't block shutdown indefinitely; the MCP server and DB
// teardown have their own internal bounds.
func (d *Daemon) Stop(ctx context.Context) {
	if d == nil {
		return
	}
	// Stop the watcher first: once it can no longer fire OnRestart, no
	// further ForceReregister calls race with the registrars' own Stop
	// teardown.
	if d.Watcher != nil {
		stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		d.Watcher.Stop(stopCtx)
		cancel()
	}
	if d.periodicCancel != nil {
		d.periodicCancel()
		if d.periodicDone != nil {
			select {
			case <-d.periodicDone:
			case <-time.After(5 * time.Second):
				d.Log.Warn("periodic reconciler did not exit within 5s")
			}
		}
	}
	if d.doorbellCancel != nil {
		d.doorbellCancel()
		if d.doorbellDone != nil {
			select {
			case <-d.doorbellDone:
			case <-time.After(5 * time.Second):
				d.Log.Warn("delivery watcher did not exit within 5s")
			}
		}
	}
	if d.ViewRegistrar != nil {
		unregCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_ = d.ViewRegistrar.Stop(unregCtx)
		cancel()
	}
	if d.ViewServer != nil {
		d.ViewServer.Stop()
	}
	if d.ViewProxy != nil {
		d.ViewProxy.Close()
	}
	if d.viewProxyCancel != nil {
		d.viewProxyCancel()
	}
	if d.SettingsRegistrar != nil {
		unregCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_ = d.SettingsRegistrar.Stop(unregCtx)
		cancel()
	}
	if d.Registrar != nil {
		unregCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		_ = d.Registrar.Stop(unregCtx)
		cancel()
	}
	if d.MCPServer != nil {
		_ = d.MCPServer.Stop()
	}
	if d.DB != nil {
		_ = d.DB.Close()
	}
	d.Log.Info("hera stopped")
}

// Run brings hera up, writes its PID file, blocks until ctx is canceled
// (typically by SIGINT/SIGTERM), then gracefully shuts down.
func Run(ctx context.Context, cfg *config.Config, log *slog.Logger) error {
	d, err := Start(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer d.Stop(context.Background())

	if err := os.WriteFile(cfg.PIDPath(), []byte(fmt.Sprintf("%d", os.Getpid())), 0o644); err != nil {
		log.Warn("write pidfile", "path", cfg.PIDPath(), "err", err)
	}
	defer func() { _ = os.Remove(cfg.PIDPath()) }()

	<-ctx.Done()
	return nil
}

// toolDefinitions returns the six hera_* tool registrations.
func toolDefinitions() []mcp.ToolDefinition {
	return []mcp.ToolDefinition{
		{
			Name:        "hera_new_orchestrator",
			Description: "Bootstrap a new hera orchestrator from the calling argus task. Creates the orchestrator, a coordinator role with the given name, and a binding tying this argus task to that role. This is the canonical 'be an orchestrator' entry point.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cwd":                   map[string]any{"type": "string", "description": "Caller's worktree path (use $PWD)"},
					"name":                  map[string]any{"type": "string", "description": "Orchestrator name (e.g., the project / feature being coordinated)"},
					"coordinator_role_name": map[string]any{"type": "string", "description": "Name for the coordinator role under the new orchestrator (e.g., 'coord' or 'foo-coordinator')"},
					"mission":               map[string]any{"type": "string", "description": "(optional) Coordinator's mission, free-form prose"},
					"constraints":           map[string]any{"type": "string", "description": "(optional) Coordinator's constraints, free-form prose"},
				},
				"required": []string{"cwd", "name", "coordinator_role_name"},
			},
		},
		{
			Name:        "hera_join",
			Description: "Claim an existing hera role or attach a new one for the calling argus task. Claim mode: hera_join(cwd) returns the task's single live binding; hera_join(cwd, orchestrator=X) returns the binding for orchestrator X (required when the task holds 2+ live bindings). Attach mode: hera_join(cwd, orchestrator + role_name + kind=worker|freelance + optional mission/constraints/status) creates a new role under the orchestrator and binds it. To bootstrap a new orchestrator, use hera_new_orchestrator instead.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cwd":          map[string]any{"type": "string", "description": "Caller's worktree path (use $PWD)"},
					"orchestrator": map[string]any{"type": "string", "description": "(optional in claim mode for tasks with exactly one binding; required for tasks with 2+ bindings or for attach mode) The orchestrator to claim from or attach to."},
					"role_name":    map[string]any{"type": "string", "description": "(attach mode only) Self-chosen role name"},
					"kind":         map[string]any{"type": "string", "enum": []string{"worker", "freelance"}, "description": "(attach mode only) Role kind. coordinator is not accepted here — use hera_new_orchestrator."},
					"mission":      map[string]any{"type": "string", "description": "(optional, attach mode) Role mission, free-form prose"},
					"constraints":  map[string]any{"type": "string", "description": "(optional, attach mode) Role constraints, free-form prose"},
					"status":       map[string]any{"type": "string", "enum": []string{"idle", "working", "blocked", "done"}, "description": "(optional, attach mode) Initial role status"},
				},
				"required": []string{"cwd"},
			},
		},
		{
			Name:        "hera_send",
			Description: "Send a message to another hera role within the same orchestrator. Worker and freelance senders default-route to the orchestrator's coordinator if `to` is omitted. Coordinator senders MUST supply an explicit `to` (talking to the human happens in the coordinator's own Claude pane, not via hera_send).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cwd":          map[string]any{"type": "string", "description": "Caller's worktree path (use $PWD)"},
					"body":         map[string]any{"type": "string", "description": "Message body"},
					"to":           map[string]any{"type": "string", "description": "(optional for worker/freelance, required for coordinator) Recipient role name within the same orchestrator"},
					"in_reply_to":  map[string]any{"type": "integer", "description": "(optional) Message id this is a reply to"},
					"orchestrator": map[string]any{"type": "string", "description": "(required when the caller's argus task holds 2+ live bindings; optional when it holds exactly one) The orchestrator whose binding identifies the sender role for this call. The recipient is resolved within the same orchestrator."},
				},
				"required": []string{"cwd", "body"},
			},
		},
		{
			Name:        "hera_inbox",
			Description: "List unread messages addressed to the caller's role, oldest first.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cwd":          map[string]any{"type": "string", "description": "Caller's worktree path (use $PWD)"},
					"orchestrator": map[string]any{"type": "string", "description": "(required when the caller's argus task holds 2+ live bindings; optional when it holds exactly one) The orchestrator whose binding identifies the calling role."},
				},
				"required": []string{"cwd"},
			},
		},
		{
			Name:        "hera_mark_read",
			Description: "Mark one or more inbox messages as read for the caller's role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cwd":          map[string]any{"type": "string", "description": "Caller's worktree path (use $PWD)"},
					"message_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Inbox message ids returned by hera_inbox"},
					"orchestrator": map[string]any{"type": "string", "description": "(required when the caller's argus task holds 2+ live bindings; optional when it holds exactly one) The orchestrator whose binding identifies the calling role."},
				},
				"required": []string{"cwd", "message_ids"},
			},
		},
		{
			Name:        "hera_status",
			Description: "Set the caller role's status. Status is also mirrored to argus task_meta as meta:hera.thread_status.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cwd":          map[string]any{"type": "string", "description": "Caller's worktree path (use $PWD)"},
					"status":       map[string]any{"type": "string", "enum": []string{"idle", "working", "blocked", "done"}, "description": "New role status"},
					"orchestrator": map[string]any{"type": "string", "description": "(required when the caller's argus task holds 2+ live bindings; optional when it holds exactly one) The orchestrator whose binding identifies the calling role."},
				},
				"required": []string{"cwd", "status"},
			},
		},
	}
}
