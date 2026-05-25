// Package settings owns the lifecycle of hera's settings-section
// registrations with argus: register on daemon start, heartbeat on a
// ticker, unregister on graceful shutdown.
//
// The Registrar here mirrors mcp.Registrar field-for-field on purpose (see
// design D2). The duplication is cheaper than a generic abstraction while
// only two registrars exist (tools and settings).
package settings

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/anutron/hera/internal/argus"
)

// DefaultHeartbeat is how often the registrar re-POSTs each settings
// section registration. Argus's idle sweep defaults to 10 minutes; half
// that keeps a comfortable margin (same value as mcp.DefaultHeartbeat).
const DefaultHeartbeat = 5 * time.Minute

// HeraSectionName is the locked section name hera registers under. Pinned
// because the spec's "Exactly one settings-section registered on startup"
// scenario asserts on it and the substrate UI keys off of it.
const HeraSectionName = "hera"

// HeraCallbackURL is the locked callback URL the substrate POSTs into
// when the operator saves the settings form. Routes to the settings_save
// handler on the existing 7744 MCP listener.
const HeraCallbackURL = "http://127.0.0.1:7744/mcp/settings_save"

// HeraSection returns the canonical hera settings-section definition.
// The field descriptions are LOCKED per tasks.md task 3.4 — they describe
// both what each field does AND the operational impact of changing it,
// per the spec's "Settings field descriptions explain impact" requirement.
// authHeader is the per-session shared secret hera shares with argus for
// callback auth.
func HeraSection(authHeader string) argus.SettingsSectionDefinition {
	minZero := 0
	maxSixty := 60
	return argus.SettingsSectionDefinition{
		Name:        HeraSectionName,
		Type:        "form",
		CallbackURL: HeraCallbackURL,
		AuthHeader:  authHeader,
		Fields: []argus.SettingField{
			{
				Name: "idle_debounce_seconds",
				Type: "int",
				Description: "Seconds an agent's session must stay quiet before hera auto-submits any messages waiting in its input buffer. " +
					"**Lower** = faster delivery once an agent goes quiet, but higher risk of submitting while the agent is still working between bursts. " +
					"**Higher** = more padding before submit, at the cost of slower message delivery. " +
					"**0** submits on the first quiet event. " +
					"**60** is the ceiling — past that you're working around a substrate bug, not tuning UX. " +
					"Default 2 reproduces v1 behavior.",
				Default: 2,
				Min:     &minZero,
				Max:     &maxSixty,
			},
			{
				Name: "auto_inject_enabled",
				Type: "bool",
				Description: "When **on**, hera auto-submits cross-agent messages (presses Enter for you) once the recipient agent's session has been quiet for the debounce above. " +
					"When **off**, every message is left sitting in the recipient's input buffer for you to read and submit manually — same as how busy sessions are already handled. " +
					"Turn off when you want to QA every cross-agent message before it lands. " +
					"Default on reproduces v1 behavior.",
				Default: true,
			},
		},
	}
}

// Registrar owns the lifecycle of hera's settings-section registrations
// with argus. Mirrors mcp.Registrar field-for-field by design (D2).
type Registrar struct {
	client    *argus.Client
	heartbeat time.Duration
	log       *slog.Logger

	mu       sync.Mutex
	sections []argus.SettingsSectionDefinition
	stop     chan struct{}
	wg       sync.WaitGroup // tracks the heartbeat goroutine so Stop can wait
}

// NewRegistrar constructs a Registrar. The client is the argus HTTP
// client that already carries hera's scope token. log is optional; nil
// falls back to slog.Default().
//
// Unlike mcp.Registrar, the callback URL and auth header live inside each
// SettingsSectionDefinition (because they're part of the wire payload, not
// derived per-tool), so they're not constructor params here.
func NewRegistrar(client *argus.Client, log *slog.Logger) *Registrar {
	if log == nil {
		log = slog.Default()
	}
	return &Registrar{
		client:    client,
		heartbeat: DefaultHeartbeat,
		log:       log,
	}
}

// SetHeartbeat overrides the default heartbeat duration. Useful in tests.
func (r *Registrar) SetHeartbeat(d time.Duration) {
	r.mu.Lock()
	r.heartbeat = d
	r.mu.Unlock()
}

// Add records a settings-section that should be registered when Start
// is called. Adding after Start is safe; the next heartbeat will
// register the new section.
func (r *Registrar) Add(section argus.SettingsSectionDefinition) {
	r.mu.Lock()
	r.sections = append(r.sections, section)
	r.mu.Unlock()
}

// Sections returns the list of sections the registrar is managing.
func (r *Registrar) Sections() []argus.SettingsSectionDefinition {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]argus.SettingsSectionDefinition, len(r.sections))
	copy(out, r.sections)
	return out
}

// Start performs the initial registration and launches the heartbeat
// goroutine. Returns once initial registration completes.
func (r *Registrar) Start(ctx context.Context) error {
	if err := r.registerAll(ctx); err != nil {
		return err
	}
	r.mu.Lock()
	r.stop = make(chan struct{})
	stop := r.stop
	hb := r.heartbeat
	r.mu.Unlock()

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
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
					r.log.Warn("settings heartbeat re-register failed", "err", err)
				}
			}
		}
	}()
	return nil
}

// Stop halts the heartbeat goroutine, waits for any in-flight re-register
// to complete, and then DELETEs every registered section from argus. The
// passed ctx bounds the unregister loop; callers should supply a short
// deadline (e.g., 10s) so a stuck argus doesn't block shutdown.
func (r *Registrar) Stop(ctx context.Context) error {
	r.mu.Lock()
	if r.stop != nil {
		close(r.stop)
		r.stop = nil
	}
	sections := append([]argus.SettingsSectionDefinition(nil), r.sections...)
	r.mu.Unlock()

	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		// Caller's deadline elapsed before the heartbeat exited; proceed
		// with unregister anyway. A leftover heartbeat tick would only
		// happen if argus is unresponsive, in which case the DELETE loop
		// below will also drain quickly via ctx.
	}

	var firstErr error
	for _, s := range sections {
		if err := r.client.UnregisterSettingsSection(ctx, s.Name); err != nil {
			r.log.Warn("settings unregister failed", "section", s.Name, "err", err)
			if firstErr == nil {
				firstErr = err
			}
		} else {
			r.log.Info("settings unregistered", "section", s.Name)
		}
	}
	return firstErr
}

// registerAll POSTs every section registration to argus. Idempotent on
// the argus side (re-POST refreshes the heartbeat).
func (r *Registrar) registerAll(ctx context.Context) error {
	r.mu.Lock()
	sections := append([]argus.SettingsSectionDefinition(nil), r.sections...)
	r.mu.Unlock()

	for _, s := range sections {
		if _, err := r.client.RegisterSettingsSection(ctx, s); err != nil {
			return fmt.Errorf("register settings-section %q: %w", s.Name, err)
		}
		r.log.Debug("settings registered", "section", s.Name)
	}
	return nil
}
