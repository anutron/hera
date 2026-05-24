package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
			http.Error(w, "method", 405)
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
			http.Error(w, "method", 405)
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
