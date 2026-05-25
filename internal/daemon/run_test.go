package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/config"
	"github.com/anutron/hera/internal/db"
)

// fakeArgusForDaemon stubs every endpoint daemon.Start hits during boot:
//   - GET /api/events/stream (SSE, no events)
//   - POST /api/mcp/tools
//   - DELETE /api/mcp/tools/{name}
//   - GET /api/tasks (resolver fallback)
type fakeArgusForDaemon struct {
	mu                  sync.Mutex
	registered          []string
	unregister          []string
	settingsRegistered  []string
	settingsUnregister  []string
	streamReqs          int
	streamClose         chan struct{}
}

func (f *fakeArgusForDaemon) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/events/stream", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.streamReqs++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Block until the test signals close; this mimics a real SSE
		// channel staying open for the daemon's lifetime.
		select {
		case <-f.streamClose:
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("/api/mcp/tools", func(w http.ResponseWriter, r *http.Request) {
		var body argus.MCPTool
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.registered = append(f.registered, body.Name)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"name":"`+body.Name+`","scope":"hera"}`)
	})
	mux.HandleFunc("/api/mcp/tools/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/api/mcp/tools/")
		f.mu.Lock()
		f.unregister = append(f.unregister, name)
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/plugins/settings/sections", func(w http.ResponseWriter, r *http.Request) {
		var body argus.SettingsSectionDefinition
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.settingsRegistered = append(f.settingsRegistered, body.Name)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"name":"`+body.Name+`"}`)
	})
	mux.HandleFunc("/api/plugins/settings/sections/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/api/plugins/settings/sections/")
		f.mu.Lock()
		f.settingsUnregister = append(f.settingsUnregister, name)
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"tasks":[]}`)
	})
	return mux
}

func TestDaemonStart_RegistersAllFiveToolsAndCleansUp(t *testing.T) {
	fake := &fakeArgusForDaemon{streamClose: make(chan struct{})}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	defer close(fake.streamClose) // drain SSE handler so HTTP server can shut down

	// Build a temp config (state dir + listen on :0).
	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "api-token"), []byte("test-token\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	cfg := &config.Config{
		StateDir:        stateDir,
		ArgusBaseURL:    srv.URL,
		ListenAddr:      "127.0.0.1:0",
		IdleDebounce:    100 * time.Millisecond,
		MCPHeartbeat:    24 * time.Hour, // skip heartbeat noise during this test
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	d, err := Start(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(context.Background())

	// Wait until all six tools have registered.
	want := []string{"hera_new_orchestrator", "hera_join", "hera_send", "hera_inbox", "hera_mark_read", "hera_status"}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fake.mu.Lock()
		n := len(fake.registered)
		fake.mu.Unlock()
		if n >= len(want) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	fake.mu.Lock()
	got := append([]string(nil), fake.registered...)
	fake.mu.Unlock()
	if len(got) < len(want) {
		t.Fatalf("registered %d tools, want %d: %+v", len(got), len(want), got)
	}
	seen := map[string]bool{}
	for _, n := range got {
		seen[n] = true
	}
	for _, n := range want {
		if !seen[n] {
			t.Fatalf("missing registration for %q (got %+v)", n, got)
		}
	}

	// Shut down and verify all tools are unregistered.
	d.Stop(context.Background())
	fake.mu.Lock()
	gotUnreg := append([]string(nil), fake.unregister...)
	fake.mu.Unlock()
	if len(gotUnreg) != len(want) {
		t.Fatalf("unregistered %d tools, want %d", len(gotUnreg), len(want))
	}
}

// TestDaemonStart_PersistedSettingsOverrideDefaults exercises the Stage 1.6
// integration contract: rows in the config table override the Default() values
// of IdleDebounce and AutoInjectEnabled before Tracker and Injector are
// instantiated. It also asserts the SettingsRegistrar registered the section
// on Start and unregistered it on Stop.
func TestDaemonStart_PersistedSettingsOverrideDefaults(t *testing.T) {
	fake := &fakeArgusForDaemon{streamClose: make(chan struct{})}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	defer close(fake.streamClose)

	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "api-token"), []byte("test-token\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}

	// Pre-populate the config table with persisted values that diverge
	// from Default(): debounce 5s (vs default 2s), auto-inject off (vs
	// default true).
	cfg := &config.Config{
		StateDir:     stateDir,
		ArgusBaseURL: srv.URL,
		ListenAddr:   "127.0.0.1:0",
		IdleDebounce: 2 * time.Second,
		MCPHeartbeat: 24 * time.Hour,
		AutoInjectEnabled: true, // Default — LoadPersistedSettings should flip this to false.
	}
	database, err := db.Open(cfg.StatePath())
	if err != nil {
		t.Fatalf("pre-seed db open: %v", err)
	}
	ctx0 := context.Background()
	if err := database.Config.Set(ctx0, config.KeyIdleDebounceSeconds, "5"); err != nil {
		t.Fatalf("pre-seed debounce: %v", err)
	}
	if err := database.Config.Set(ctx0, config.KeyAutoInjectEnabled, "false"); err != nil {
		t.Fatalf("pre-seed auto-inject: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("pre-seed db close: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	d, err := Start(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(context.Background())

	// Persisted debounce wins: cfg.IdleDebounce was overwritten.
	if cfg.IdleDebounce != 5*time.Second {
		t.Errorf("cfg.IdleDebounce = %v, want 5s", cfg.IdleDebounce)
	}
	if cfg.AutoInjectEnabled {
		t.Errorf("cfg.AutoInjectEnabled = true, want false (persisted)")
	}

	// SettingsRegistrar instantiated and section registered.
	if d.SettingsRegistrar == nil {
		t.Fatal("d.SettingsRegistrar is nil")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fake.mu.Lock()
		n := len(fake.settingsRegistered)
		fake.mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	fake.mu.Lock()
	gotReg := append([]string(nil), fake.settingsRegistered...)
	fake.mu.Unlock()
	if len(gotReg) < 1 || gotReg[0] == "" {
		t.Fatalf("settings-section not registered, got %+v", gotReg)
	}

	// Shut down and verify unregister.
	d.Stop(context.Background())
	fake.mu.Lock()
	gotUnreg := append([]string(nil), fake.settingsUnregister...)
	fake.mu.Unlock()
	if len(gotUnreg) < 1 {
		t.Fatalf("settings-section not unregistered, got %+v", gotUnreg)
	}
}

func TestDaemonStart_TokenMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	stateDir := t.TempDir()
	// no api-token file
	cfg := &config.Config{
		StateDir:     stateDir,
		ArgusBaseURL: srv.URL,
		ListenAddr:   "127.0.0.1:0",
		IdleDebounce: 100 * time.Millisecond,
		MCPHeartbeat: 24 * time.Hour,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := Start(ctx, cfg, nil)
	if err == nil {
		t.Fatalf("expected token-missing error, got nil")
	}
	if !strings.Contains(err.Error(), "api-token") {
		t.Fatalf("error didn't reference token: %v", err)
	}
}
