package view

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anutron/hera/internal/argus"
)

// fakeArgusViews stubs the four /api/plugins/views endpoints the Registrar
// uses: POST (register), GET (heartbeat lookup + 409 retry), DELETE.
type fakeArgusViews struct {
	mu             sync.Mutex
	registered     []argus.PluginView
	deleted        []int64
	registerCalls  int
	heartbeatCalls int
	deleteCalls    int
	nextID         int64
	// missingMode, when set, makes GET return an empty list to simulate
	// argus dropping the registration (heartbeat-miss path).
	missingMode bool
}

func (f *fakeArgusViews) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/plugins/views", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var body argus.PluginView
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.registerCalls++
			f.nextID++
			body.ID = f.nextID
			body.Scope = "hera"
			f.registered = append(f.registered, body)
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":`+strconv.FormatInt(body.ID, 10)+
				`,"scope":"hera","title":`+strconv.Quote(body.Title)+
				`,"hotkey":`+strconv.Quote(body.Hotkey)+
				`,"callback_url":`+strconv.Quote(body.CallbackURL)+`}`)
		case http.MethodGet:
			f.mu.Lock()
			f.heartbeatCalls++
			var views []argus.PluginView
			if !f.missingMode {
				views = append(views, f.registered...)
			}
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			body, _ := json.Marshal(map[string]any{"views": views})
			_, _ = w.Write(body)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/plugins/views/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		idStr := strings.TrimPrefix(r.URL.Path, "/api/plugins/views/")
		id, _ := strconv.ParseInt(idStr, 10, 64)
		f.mu.Lock()
		f.deleteCalls++
		f.deleted = append(f.deleted, id)
		next := f.registered[:0]
		for _, v := range f.registered {
			if v.ID != id {
				next = append(next, v)
			}
		}
		f.registered = next
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

// TestRegistrar_StartRegisters covers the happy-path POST on Start.
func TestRegistrar_StartRegisters(t *testing.T) {
	f := &fakeArgusViews{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	client := argus.New(srv.URL, "test-token")

	r := NewRegistrar(client, "Hera", "ctrl+h", "ws://127.0.0.1:7744/view", nil)
	r.SetHeartbeat(24 * time.Hour) // suppress ticker noise
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = r.Stop(context.Background()) }()

	f.mu.Lock()
	got := append([]argus.PluginView(nil), f.registered...)
	f.mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("registered = %d, want 1", len(got))
	}
	if got[0].Title != "Hera" || got[0].Hotkey != "ctrl+h" {
		t.Errorf("registration body = %+v", got[0])
	}
	if r.ID() != got[0].ID {
		t.Errorf("Registrar.ID() = %d, want %d", r.ID(), got[0].ID)
	}
}

// TestRegistrar_StopDeletes covers the DELETE on Stop.
func TestRegistrar_StopDeletes(t *testing.T) {
	f := &fakeArgusViews{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	client := argus.New(srv.URL, "test-token")

	r := NewRegistrar(client, "Hera", "ctrl+h", "ws://127.0.0.1:7744/view", nil)
	r.SetHeartbeat(24 * time.Hour)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	regID := r.ID()
	if err := r.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	f.mu.Lock()
	gotDel := append([]int64(nil), f.deleted...)
	f.mu.Unlock()
	if len(gotDel) != 1 || gotDel[0] != regID {
		t.Fatalf("deleted = %+v, want [%d]", gotDel, regID)
	}
	if r.ID() != 0 {
		t.Errorf("Registrar.ID() after Stop = %d, want 0", r.ID())
	}
}

// TestRegistrar_HeartbeatTickRunsAtInterval pins the ticker fires the
// heartbeat call when registration is present (no re-register).
func TestRegistrar_HeartbeatTickRunsAtInterval(t *testing.T) {
	f := &fakeArgusViews{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	client := argus.New(srv.URL, "test-token")

	r := NewRegistrar(client, "Hera", "ctrl+h", "ws://127.0.0.1:7744/view", nil)
	r.SetHeartbeat(20 * time.Millisecond)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = r.Stop(context.Background()) }()

	// Wait until at least 2 heartbeat lookups have landed.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		n := f.heartbeatCalls
		f.mu.Unlock()
		if n >= 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.mu.Lock()
	n := f.heartbeatCalls
	f.mu.Unlock()
	t.Fatalf("heartbeat fired only %d times in 2s", n)
}

// TestRegistrar_ReRegistersWhenMissing pins the recovery path: if the
// registration vanishes in argus, the next tick POSTs it again.
func TestRegistrar_ReRegistersWhenMissing(t *testing.T) {
	f := &fakeArgusViews{}
	srv := httptest.NewServer(f.handler())
	defer srv.Close()
	client := argus.New(srv.URL, "test-token")

	r := NewRegistrar(client, "Hera", "ctrl+h", "ws://127.0.0.1:7744/view", nil)
	r.SetHeartbeat(15 * time.Millisecond)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = r.Stop(context.Background()) }()

	// Flip the fake into "registration vanished" mode and wait for the
	// next tick to issue a re-POST.
	f.mu.Lock()
	f.missingMode = true
	postsBefore := f.registerCalls
	f.mu.Unlock()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		posts := f.registerCalls
		f.mu.Unlock()
		if posts > postsBefore {
			// Once re-registered, allow the new row to appear in the list
			// again so subsequent heartbeats stop re-POSTing.
			f.mu.Lock()
			f.missingMode = false
			f.mu.Unlock()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.mu.Lock()
	posts := f.registerCalls
	f.mu.Unlock()
	t.Fatalf("registrar did not re-POST after missing (registerCalls=%d, want >%d)", posts, postsBefore)
}

// TestRegistrar_StartErrorPropagates ensures Start surfaces the underlying
// register error rather than swallowing it.
func TestRegistrar_StartErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	client := argus.New(srv.URL, "test-token")

	r := NewRegistrar(client, "Hera", "ctrl+h", "ws://127.0.0.1:7744/view", nil)
	err := r.Start(context.Background())
	if err == nil {
		t.Fatalf("Start: expected error from 500 response, got nil")
	}
	var httpErr *argus.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %v, want *argus.HTTPError", err)
	}
}
