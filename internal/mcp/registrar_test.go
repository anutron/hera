package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anutron/hera/internal/argus"
)

// stub argus that records POST /api/mcp/tools and DELETE /api/mcp/tools/x.
type fakeRegistry struct {
	mu          sync.Mutex
	registered  []string
	unregister  []string
	authHeaders []string
}

func (f *fakeRegistry) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mcp/tools", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		var body argus.MCPTool
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.registered = append(f.registered, body.Name)
		f.authHeaders = append(f.authHeaders, body.AuthHeader)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"name":"`+body.Name+`","scope":"hera"}`)
	})
	mux.HandleFunc("/api/mcp/tools/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/api/mcp/tools/")
		f.mu.Lock()
		f.unregister = append(f.unregister, name)
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func TestRegistrar_StartRegistersAllToolsAndHeartbeats(t *testing.T) {
	fake := &fakeRegistry{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	client := argus.New(srv.URL, "tok")

	r := NewRegistrar(client, "http://127.0.0.1:9000", "Bearer test-secret", nil)
	r.Add(ToolDefinition{
		Name:        "hera_send",
		Description: "Send a hera message",
		InputSchema: map[string]any{"type": "object"},
	})
	r.Add(ToolDefinition{
		Name:        "hera_inbox",
		Description: "Read inbox for the calling role",
		InputSchema: map[string]any{"type": "object"},
	})

	r.SetHeartbeat(50 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Initial registration: both tools should appear.
	fake.mu.Lock()
	if len(fake.registered) < 2 {
		t.Fatalf("expected ≥2 registrations after Start, got %d", len(fake.registered))
	}
	if fake.authHeaders[0] != "Bearer test-secret" {
		t.Fatalf("auth header = %q", fake.authHeaders[0])
	}
	initialCount := len(fake.registered)
	fake.mu.Unlock()

	// Heartbeat: wait one tick.
	time.Sleep(120 * time.Millisecond)

	fake.mu.Lock()
	if len(fake.registered) <= initialCount {
		t.Fatalf("expected heartbeat to re-register, got count %d (was %d)",
			len(fake.registered), initialCount)
	}
	fake.mu.Unlock()

	// Shutdown: tools should be DELETEd.
	if err := r.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	fake.mu.Lock()
	if len(fake.unregister) != 2 {
		t.Fatalf("expected 2 unregister calls, got %d", len(fake.unregister))
	}
	fake.mu.Unlock()
}

// TestRegistrar_ForceReregister_FiresImmediately checks that ForceReregister
// POSTs every registered tool synchronously, without waiting for the
// heartbeat tick. The recovery routine calls this after argus restarts to
// repopulate the new daemon's tool catalog right away.
func TestRegistrar_ForceReregister_FiresImmediately(t *testing.T) {
	fake := &fakeRegistry{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	client := argus.New(srv.URL, "tok")

	r := NewRegistrar(client, "http://127.0.0.1:9000", "Bearer test-secret", nil)
	r.Add(ToolDefinition{
		Name:        "hera_send",
		Description: "Send a hera message",
		InputSchema: map[string]any{"type": "object"},
	})
	r.Add(ToolDefinition{
		Name:        "hera_inbox",
		Description: "Read inbox for the calling role",
		InputSchema: map[string]any{"type": "object"},
	})
	// Suppress the heartbeat goroutine for this test so we can attribute
	// every register POST to the explicit ForceReregister call.
	r.SetHeartbeat(time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = r.Stop(context.Background()) }()

	fake.mu.Lock()
	initial := len(fake.registered)
	fake.mu.Unlock()
	if initial != 2 {
		t.Fatalf("expected 2 registrations after Start, got %d", initial)
	}

	if err := r.ForceReregister(ctx); err != nil {
		t.Fatalf("ForceReregister: %v", err)
	}

	fake.mu.Lock()
	after := len(fake.registered)
	names := append([]string(nil), fake.registered[initial:]...)
	fake.mu.Unlock()
	if after != initial+2 {
		t.Fatalf("expected 2 new registrations after ForceReregister, got %d (total %d)", after-initial, after)
	}
	got := map[string]bool{names[0]: true, names[1]: true}
	if !got["hera_send"] || !got["hera_inbox"] {
		t.Fatalf("expected both tools re-registered, got %v", names)
	}
}

// TestRegistrar_Heartbeat404FiresCallback proves the passive-fallback
// path: a heartbeat re-register that lands on a 404 (argus has restarted
// to a new REST port and the old registration is gone) invokes the
// OnHeartbeat404 callback so the daemon can run argus.Recover.
func TestRegistrar_Heartbeat404FiresCallback(t *testing.T) {
	mux := http.NewServeMux()
	var calls int32
	mux.HandleFunc("/api/mcp/tools", func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			// Initial registration on Start succeeds.
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"name":"hera_send","scope":"hera"}`)
			return
		}
		// Subsequent heartbeat re-registers 404 to simulate the
		// "argus restarted, our slot is gone" failure mode.
		http.Error(w, "not found", http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := argus.New(srv.URL, "tok")

	r := NewRegistrar(client, "http://127.0.0.1:9000", "Bearer test-secret", nil)
	r.Add(ToolDefinition{Name: "hera_send", Description: "Send a hera message"})

	var fired atomic.Int32
	r.SetOnHeartbeat404(func(ctx context.Context) { fired.Add(1) })
	r.SetHeartbeat(40 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
		_ = r.Stop(stopCtx)
		stopCancel()
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fired.Load() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := fired.Load(); got < 1 {
		t.Fatalf("OnHeartbeat404 fired %d times after 404 heartbeat, want ≥1", got)
	}
}

// TestRegistrar_ForceReregister_ReturnsErrorOnArgusFailure verifies that a
// failing POST surfaces back to the caller so the recovery routine can
// transition link state to `down`.
func TestRegistrar_ForceReregister_ReturnsErrorOnArgusFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/mcp/tools", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	client := argus.New(srv.URL, "tok")

	r := NewRegistrar(client, "http://127.0.0.1:9000", "Bearer test-secret", nil)
	r.Add(ToolDefinition{Name: "hera_send", Description: "Send a hera message"})
	r.SetHeartbeat(time.Hour)

	// Don't call Start; we only want to assert ForceReregister surfaces
	// the POST failure. Start would otherwise return the same error.
	err := r.ForceReregister(context.Background())
	if err == nil {
		t.Fatalf("ForceReregister: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "hera_send") {
		t.Fatalf("error should name the failing tool, got %q", err.Error())
	}
}
