package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/anutron/hera/internal/argus"
)

// DefaultHeartbeat is how often Registrar re-POSTs each tool registration.
// Argus's idle sweep defaults to 10 minutes; re-registering at half that
// keeps a comfortable margin.
const DefaultHeartbeat = 5 * time.Minute

// ToolDefinition is one tool hera wants to register with argus.
type ToolDefinition struct {
	Name        string         // must be force-prefixed "hera_"
	Description string         // ≥ 10 chars
	InputSchema map[string]any // JSON Schema (object) describing inputs
}

// Registrar owns the lifecycle of hera's tool registrations with argus:
// register on start, heartbeat on a ticker, unregister on stop.
type Registrar struct {
	client      *argus.Client
	callbackURL string
	authHeader  string
	heartbeat   time.Duration
	log         *slog.Logger

	mu    sync.Mutex
	tools []ToolDefinition
	stop  chan struct{}
}

// NewRegistrar constructs a Registrar. callbackBaseURL is the URL prefix
// hera's MCP server hosts (e.g. "http://127.0.0.1:7744"); the per-tool
// URL becomes "<callbackBaseURL>/mcp/<tool-name>".
func NewRegistrar(client *argus.Client, callbackBaseURL, authHeader string, log *slog.Logger) *Registrar {
	if log == nil {
		log = slog.Default()
	}
	return &Registrar{
		client:      client,
		callbackURL: callbackBaseURL,
		authHeader:  authHeader,
		heartbeat:   DefaultHeartbeat,
		log:         log,
	}
}

// SetHeartbeat overrides the default heartbeat duration. Useful in tests.
func (r *Registrar) SetHeartbeat(d time.Duration) {
	r.mu.Lock()
	r.heartbeat = d
	r.mu.Unlock()
}

// Add records a tool that should be registered when Start is called.
// Adding after Start is safe; the next heartbeat will register the new
// tool.
func (r *Registrar) Add(def ToolDefinition) {
	r.mu.Lock()
	r.tools = append(r.tools, def)
	r.mu.Unlock()
}

// Tools returns the list of tools the registrar is managing.
func (r *Registrar) Tools() []ToolDefinition {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ToolDefinition, len(r.tools))
	copy(out, r.tools)
	return out
}

// Start performs the initial registration and launches the heartbeat
// goroutine. Returns when initial registration completes.
func (r *Registrar) Start(ctx context.Context) error {
	if err := r.registerAll(ctx); err != nil {
		return err
	}
	r.mu.Lock()
	r.stop = make(chan struct{})
	stop := r.stop
	hb := r.heartbeat
	r.mu.Unlock()

	go func() {
		ticker := time.NewTicker(hb)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				if err := r.registerAll(ctx); err != nil {
					r.log.Warn("heartbeat re-register failed", "err", err)
				}
			}
		}
	}()
	return nil
}

// Stop halts the heartbeat goroutine and DELETEs every registered tool
// from argus.
func (r *Registrar) Stop(ctx context.Context) error {
	r.mu.Lock()
	if r.stop != nil {
		close(r.stop)
		r.stop = nil
	}
	tools := append([]ToolDefinition(nil), r.tools...)
	r.mu.Unlock()

	var firstErr error
	for _, t := range tools {
		if err := r.client.UnregisterTool(ctx, t.Name); err != nil {
			r.log.Warn("unregister failed", "tool", t.Name, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		} else {
			r.log.Info("unregistered", "tool", t.Name)
		}
	}
	return firstErr
}

// registerAll POSTs every tool registration to argus. Idempotent on the
// argus side (re-POST refreshes the LastSeenAt heartbeat).
func (r *Registrar) registerAll(ctx context.Context) error {
	r.mu.Lock()
	tools := append([]ToolDefinition(nil), r.tools...)
	r.mu.Unlock()

	for _, t := range tools {
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		_, err := r.client.RegisterTool(ctx, argus.MCPTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
			CallbackURL: r.callbackURL + "/mcp/" + t.Name,
			AuthHeader:  r.authHeader,
		})
		if err != nil {
			return fmt.Errorf("register %q: %w", t.Name, err)
		}
		r.log.Debug("registered", "tool", t.Name)
	}
	return nil
}
