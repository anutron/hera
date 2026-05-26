package view

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/anutron/hera/internal/argus"
)

// DefaultHeartbeat is how often Registrar re-POSTs the plugin-view
// registration. Argus's idle sweep defaults to 10 minutes; re-registering
// at half that keeps a comfortable margin (mirrors mcp.DefaultHeartbeat).
const DefaultHeartbeat = 5 * time.Minute

// Registrar owns the lifecycle of hera's plugin-view registration with
// argus: register on Start, heartbeat on a ticker (re-register on miss),
// unregister on Stop.
//
// The heartbeat shape is GET /api/plugins/views (scope-filtered) checking
// for the registered id; ErrPluginViewMissing surfaces when the row has
// vanished, in which case Registrar re-POSTs to restore it. This matches
// the mcp.Registrar's heartbeat-then-recover shape.
type Registrar struct {
	client      *argus.Client
	title       string
	hotkey      string
	callbackURL string
	heartbeat   time.Duration
	log         *slog.Logger

	mu   sync.Mutex
	id   int64
	stop chan struct{}
	wg   sync.WaitGroup
}

// NewRegistrar constructs a Registrar. title is shown in argus's UI;
// hotkey is the argus-side keyboard shortcut to open the view; callbackURL
// is the ws:// URL argus dials on hotkey press.
func NewRegistrar(client *argus.Client, title, hotkey, callbackURL string, log *slog.Logger) *Registrar {
	if log == nil {
		log = slog.Default()
	}
	return &Registrar{
		client:      client,
		title:       title,
		hotkey:      hotkey,
		callbackURL: callbackURL,
		heartbeat:   DefaultHeartbeat,
		log:         log,
	}
}

// SetHeartbeat overrides the default heartbeat interval. Useful in tests.
func (r *Registrar) SetHeartbeat(d time.Duration) {
	r.mu.Lock()
	r.heartbeat = d
	r.mu.Unlock()
}

// ID returns the registered view's id. Zero before Start completes.
func (r *Registrar) ID() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.id
}

// Start performs the initial registration with argus and launches the
// heartbeat goroutine. Returns once initial registration completes.
func (r *Registrar) Start(ctx context.Context) error {
	view, err := r.client.RegisterView(ctx, r.title, r.hotkey, r.callbackURL)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.id = view.ID
	r.stop = make(chan struct{})
	stop := r.stop
	hb := r.heartbeat
	r.mu.Unlock()

	r.log.Info("registered plugin view",
		"id", view.ID,
		"title", r.title,
		"hotkey", r.hotkey,
		"callback_url", r.callbackURL,
	)

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
				r.tick(ctx)
			}
		}
	}()
	return nil
}

// tick runs one heartbeat cycle. On ErrPluginViewMissing it re-registers
// with argus so the view reappears in the registry; on other errors it
// logs and waits for the next tick.
func (r *Registrar) tick(ctx context.Context) {
	r.mu.Lock()
	id := r.id
	r.mu.Unlock()
	err := r.client.HeartbeatView(ctx, id)
	if err == nil {
		return
	}
	if errors.Is(err, argus.ErrPluginViewMissing) {
		view, regErr := r.client.RegisterView(ctx, r.title, r.hotkey, r.callbackURL)
		if regErr != nil {
			r.log.Warn("re-register plugin view failed", "err", regErr)
			return
		}
		r.mu.Lock()
		r.id = view.ID
		r.mu.Unlock()
		r.log.Info("re-registered plugin view after missing", "id", view.ID)
		return
	}
	r.log.Warn("plugin view heartbeat failed", "err", err)
}

// Stop halts the heartbeat goroutine and DELETEs the registration. The
// passed ctx bounds the unregister; callers should supply a short
// deadline so a stuck argus doesn't block shutdown.
func (r *Registrar) Stop(ctx context.Context) error {
	r.mu.Lock()
	if r.stop != nil {
		close(r.stop)
		r.stop = nil
	}
	id := r.id
	r.id = 0
	r.mu.Unlock()

	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}

	if id == 0 {
		return nil
	}
	if err := r.client.DeleteView(ctx, id); err != nil {
		r.log.Warn("delete plugin view failed", "id", id, "err", err)
		return err
	}
	r.log.Info("unregistered plugin view", "id", id)
	return nil
}
