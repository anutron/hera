package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/anutron/ludwig/internal/argus"
	"github.com/anutron/ludwig/internal/config"
	"github.com/anutron/ludwig/internal/db"
	"github.com/anutron/ludwig/internal/events"
	"github.com/anutron/ludwig/internal/idle"
	"github.com/anutron/ludwig/internal/inject"
	"github.com/anutron/ludwig/internal/mcp"
)

// Daemon bundles every running component so the caller can introspect or
// shut them down individually. The Run helper composes one and returns
// it (mostly useful for tests; production callers use Run() and forget).
type Daemon struct {
	Cfg        *config.Config
	Log        *slog.Logger
	DB         *db.DB
	Argus      *argus.Client
	IdleTrack  *idle.Tracker
	Injector   *inject.Injector
	MCPServer  *mcp.Server
	Registrar  *mcp.Registrar
	Subscriber *events.Subscriber
}

// Start assembles ludwig and brings every subsystem up. Returns the live
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
		return nil, fmt.Errorf("ludwig: state dir: %w", err)
	}
	token, err := cfg.LoadToken()
	if err != nil {
		return nil, err
	}
	database, err := db.Open(cfg.StatePath())
	if err != nil {
		return nil, fmt.Errorf("ludwig: open db: %w", err)
	}

	client := argus.New(cfg.ArgusBaseURL, token)

	tracker := idle.NewWithDebounce(cfg.IdleDebounce)
	injector := inject.New(client, tracker)
	resolver := mcp.NewResolver(client, database)

	auth, err := mcp.GenerateAuthHeader()
	if err != nil {
		database.Close()
		return nil, fmt.Errorf("ludwig: generate mcp auth: %w", err)
	}

	mcpSrv := mcp.NewServer(cfg.ListenAddr, auth, log)
	if err := mcpSrv.Start(ctx); err != nil {
		database.Close()
		return nil, fmt.Errorf("ludwig: start mcp server: %w", err)
	}

	// Wire handlers.
	mcpSrv.RegisterHandler("ludwig_join", mcp.NewJoinHandler(resolver, database))
	mcpSrv.RegisterHandler("ludwig_send", mcp.NewSendHandler(resolver, database, injector))
	mcpSrv.RegisterHandler("ludwig_inbox", mcp.NewInboxHandler(resolver, database))
	mcpSrv.RegisterHandler("ludwig_mark_read", mcp.NewMarkReadHandler(resolver, database))
	mcpSrv.RegisterHandler("ludwig_status", mcp.NewStatusHandler(resolver, database, client))

	// CallbackBaseURL is the actual bound address (honors :0).
	callback := "http://" + mcpSrv.Addr()
	registrar := mcp.NewRegistrar(client, callback, auth, log)
	registrar.SetHeartbeat(cfg.MCPHeartbeat)
	for _, def := range toolDefinitions() {
		registrar.Add(def)
	}
	if err := registrar.Start(ctx); err != nil {
		_ = mcpSrv.Stop()
		database.Close()
		return nil, fmt.Errorf("ludwig: register tools: %w", err)
	}

	subscriber := events.NewSubscriber(client, database, log)
	subscriber.Register(events.NewAdoptHandler(client, database, log))
	subscriber.Register(events.NewResyncHandler(client, database, log))
	subscriber.Register(tracker) // tracker implements events.Handler

	// Subscriber runs in its own goroutine; Run() blocks on ctx in main.
	go func() {
		if err := subscriber.Run(ctx); err != nil && ctx.Err() == nil {
			log.Warn("event subscriber exited", "err", err)
		}
	}()

	log.Info("ludwig ready",
		"argus_base_url", cfg.ArgusBaseURL,
		"mcp_addr", mcpSrv.Addr(),
		"state", cfg.StatePath(),
	)

	return &Daemon{
		Cfg: cfg, Log: log, DB: database, Argus: client,
		IdleTrack: tracker, Injector: injector,
		MCPServer: mcpSrv, Registrar: registrar, Subscriber: subscriber,
	}, nil
}

// Stop gracefully shuts every subsystem down in reverse order. Safe to
// call multiple times.
func (d *Daemon) Stop(ctx context.Context) {
	if d == nil {
		return
	}
	if d.Registrar != nil {
		_ = d.Registrar.Stop(ctx)
	}
	if d.MCPServer != nil {
		_ = d.MCPServer.Stop()
	}
	if d.DB != nil {
		_ = d.DB.Close()
	}
	d.Log.Info("ludwig stopped")
}

// Run brings ludwig up, writes its PID file, blocks until ctx is canceled
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
	defer os.Remove(cfg.PIDPath())

	<-ctx.Done()
	return nil
}

// toolDefinitions returns the five ludwig_* tool registrations.
func toolDefinitions() []mcp.ToolDefinition {
	return []mcp.ToolDefinition{
		{
			Name:        "ludwig_join",
			Description: "Claim or create a ludwig role for the calling argus task. Bare call (cwd only) claims an existing binding; extended call (orchestrator + role_name + kind + optional mission/constraints/status) attaches as a freelance or coordinator.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cwd":          map[string]any{"type": "string", "description": "Caller's worktree path (use $PWD)"},
					"orchestrator": map[string]any{"type": "string", "description": "(optional) Orchestrator to attach to / create"},
					"role_name":    map[string]any{"type": "string", "description": "(optional) Self-chosen role name"},
					"kind":         map[string]any{"type": "string", "enum": []string{"worker", "freelance", "coordinator"}, "description": "(optional) Role kind"},
					"mission":      map[string]any{"type": "string", "description": "(optional) Role mission, free-form prose"},
					"constraints":  map[string]any{"type": "string", "description": "(optional) Role constraints, free-form prose"},
					"status":       map[string]any{"type": "string", "enum": []string{"idle", "working", "blocked", "done"}, "description": "(optional) Initial role status"},
				},
				"required": []string{"cwd"},
			},
		},
		{
			Name:        "ludwig_send",
			Description: "Send a message to another ludwig role. Default routing: worker/freelance → coordinator of same orchestrator; coordinator → user pseudo-recipient.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cwd":         map[string]any{"type": "string", "description": "Caller's worktree path (use $PWD)"},
					"body":        map[string]any{"type": "string", "description": "Message body"},
					"to":          map[string]any{"type": "string", "description": "(optional) Recipient role name within the same orchestrator, or 'user'"},
					"in_reply_to": map[string]any{"type": "integer", "description": "(optional) Message id this is a reply to"},
				},
				"required": []string{"cwd", "body"},
			},
		},
		{
			Name:        "ludwig_inbox",
			Description: "List unread messages addressed to the caller's role, oldest first.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cwd": map[string]any{"type": "string", "description": "Caller's worktree path (use $PWD)"},
				},
				"required": []string{"cwd"},
			},
		},
		{
			Name:        "ludwig_mark_read",
			Description: "Mark one or more inbox messages as read for the caller's role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cwd":         map[string]any{"type": "string", "description": "Caller's worktree path (use $PWD)"},
					"message_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Inbox message ids returned by ludwig_inbox"},
				},
				"required": []string{"cwd", "message_ids"},
			},
		},
		{
			Name:        "ludwig_status",
			Description: "Set the caller role's status. Status is also mirrored to argus task_meta as meta:ludwig.thread_status.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"cwd":    map[string]any{"type": "string", "description": "Caller's worktree path (use $PWD)"},
					"status": map[string]any{"type": "string", "enum": []string{"idle", "working", "blocked", "done"}, "description": "New role status"},
				},
				"required": []string{"cwd", "status"},
			},
		},
	}
}
