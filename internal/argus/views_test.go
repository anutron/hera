package argus

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRegisterView_PostsAndDecodes pins the POST wire shape and the
// decoded *PluginView. This is the canonical happy path: argus accepts
// the registration with 201 Created and returns the full row.
func TestRegisterView_PostsAndDecodes(t *testing.T) {
	var gotBody pluginViewCreateReq
	var gotMethod, gotPath, gotContentType string
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":42,"scope":"hera","title":"Hera","hotkey":"ctrl+h","callback_url":"ws://127.0.0.1:7744/view","created_at":"2026-05-26T00:00:00Z"}`)
	})
	defer srv.Close()

	view, err := c.RegisterView(context.Background(), "Hera", "ctrl+h", "ws://127.0.0.1:7744/view")
	if err != nil {
		t.Fatalf("RegisterView: %v", err)
	}
	if gotMethod != "POST" {
		t.Fatalf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/api/plugins/views" {
		t.Fatalf("path = %s, want /api/plugins/views", gotPath)
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", gotContentType)
	}
	if gotBody.Title != "Hera" || gotBody.Hotkey != "ctrl+h" || gotBody.CallbackURL != "ws://127.0.0.1:7744/view" {
		t.Fatalf("body = %+v", gotBody)
	}
	if view == nil {
		t.Fatalf("view is nil")
	}
	if view.ID != 42 {
		t.Fatalf("view.ID = %d, want 42", view.ID)
	}
	if view.Scope != "hera" || view.Title != "Hera" || view.Hotkey != "ctrl+h" || view.CallbackURL != "ws://127.0.0.1:7744/view" {
		t.Fatalf("view = %+v", view)
	}
}

// TestRegisterView_AuthHeader confirms the scope token bears the
// registration; argus derives the row's scope from this header rather
// than the body so the request identity is load-bearing.
func TestRegisterView_AuthHeader(t *testing.T) {
	var gotAuth, gotVersion string
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("X-Argus-Plugin-Version")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":1,"scope":"hera","title":"Hera","callback_url":"ws://x"}`)
	})
	defer srv.Close()

	if _, err := c.RegisterView(context.Background(), "Hera", "ctrl+h", "ws://x"); err != nil {
		t.Fatalf("RegisterView: %v", err)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("Authorization = %q, want Bearer test-token", gotAuth)
	}
	if gotVersion != "1" {
		t.Fatalf("X-Argus-Plugin-Version = %q", gotVersion)
	}
}

// TestRegisterView_409ReturnsExisting verifies the idempotency contract:
// when argus 409s on (scope, title) collision, RegisterView transparently
// resolves the existing row via GET /api/plugins/views and returns it.
// This lets the registrar call RegisterView on every heartbeat tick
// without special-casing the "already registered" path.
func TestRegisterView_409ReturnsExisting(t *testing.T) {
	var postCount, getCount int
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/plugins/views", func(w http.ResponseWriter, r *http.Request) {
		postCount++
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"view already registered for this scope/title"}`)
	})
	mux.HandleFunc("GET /api/plugins/views", func(w http.ResponseWriter, r *http.Request) {
		getCount++
		_, _ = io.WriteString(w, `{"views":[{"id":7,"scope":"hera","title":"Hera","hotkey":"ctrl+h","callback_url":"ws://127.0.0.1:7744/view"}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New(srv.URL, "tok")

	view, err := c.RegisterView(context.Background(), "Hera", "ctrl+h", "ws://127.0.0.1:7744/view")
	if err != nil {
		t.Fatalf("RegisterView on 409: %v (want nil — existing row should be returned)", err)
	}
	if view == nil || view.ID != 7 {
		t.Fatalf("view = %+v, want id=7 from existing list", view)
	}
	if postCount != 1 {
		t.Fatalf("postCount = %d, want exactly 1", postCount)
	}
	if getCount != 1 {
		t.Fatalf("getCount = %d, want exactly 1 (one follow-up list)", getCount)
	}
}

// TestRegisterView_409ButNoMatchInList surfaces a hard error when argus
// 409s but the follow-up list doesn't contain the title — that's a
// substrate violation that should be visible to the caller rather than
// silently masked.
func TestRegisterView_409ButNoMatchInList(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/plugins/views", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	})
	mux.HandleFunc("GET /api/plugins/views", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"views":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New(srv.URL, "tok")

	_, err := c.RegisterView(context.Background(), "Hera", "ctrl+h", "ws://127.0.0.1:7744/view")
	if err == nil {
		t.Fatalf("expected error when 409 follow-up list is empty, got nil")
	}
}

// TestRegisterView_NonConflictError surfaces non-2xx, non-409 responses
// to the caller as a typed HTTPError without translation. Required so
// auth failures, 5xx, etc. propagate up the registrar instead of being
// swallowed.
func TestRegisterView_NonConflictError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")

	_, err := c.RegisterView(context.Background(), "Hera", "ctrl+h", "ws://x")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	var he *HTTPError
	if !errors.As(err, &he) || he.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 HTTPError, got %v", err)
	}
}

// TestHeartbeatView_FindsRegistered is the canonical happy path: GET
// /api/plugins/views returns our id, HeartbeatView returns nil.
func TestHeartbeatView_FindsRegistered(t *testing.T) {
	var gotMethod, gotPath string
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{"views":[{"id":42,"scope":"hera","title":"Hera","callback_url":"ws://x"},{"id":43,"scope":"hera","title":"Other","callback_url":"ws://y"}]}`)
	})
	defer srv.Close()

	if err := c.HeartbeatView(context.Background(), 42); err != nil {
		t.Fatalf("HeartbeatView: %v", err)
	}
	if gotMethod != "GET" {
		t.Fatalf("method = %s", gotMethod)
	}
	if gotPath != "/api/plugins/views" {
		t.Fatalf("path = %s", gotPath)
	}
}

// TestHeartbeatView_MissingReturnsSentinel verifies the registrar can
// detect "registration vanished" via errors.Is(err, ErrPluginViewMissing)
// and re-register on the next tick.
func TestHeartbeatView_MissingReturnsSentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"views":[{"id":1,"title":"Other","callback_url":"ws://y"}]}`)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")

	err := c.HeartbeatView(context.Background(), 99)
	if !errors.Is(err, ErrPluginViewMissing) {
		t.Fatalf("expected ErrPluginViewMissing, got %v", err)
	}
}

// TestHeartbeatView_HTTPErrorPropagates surfaces auth/transport failures
// rather than translating them to ErrPluginViewMissing — the registrar
// would otherwise re-register on every tick during an outage.
func TestHeartbeatView_HTTPErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")

	err := c.HeartbeatView(context.Background(), 1)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if errors.Is(err, ErrPluginViewMissing) {
		t.Fatalf("got ErrPluginViewMissing for a 401, want raw HTTPError")
	}
	var he *HTTPError
	if !errors.As(err, &he) || he.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 HTTPError, got %v", err)
	}
}

// TestDeleteView_DELETEsExpectedPath pins the wire shape of unregistration.
func TestDeleteView_DELETEsExpectedPath(t *testing.T) {
	var gotMethod, gotPath string
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{"deleted":42}`)
	})
	defer srv.Close()

	if err := c.DeleteView(context.Background(), 42); err != nil {
		t.Fatalf("DeleteView: %v", err)
	}
	if gotMethod != "DELETE" {
		t.Fatalf("method = %s", gotMethod)
	}
	if gotPath != "/api/plugins/views/42" {
		t.Fatalf("path = %s", gotPath)
	}
}

// TestDeleteView_404IsSoftSuccess pins the idempotency contract: deleting
// an already-removed (or never-registered) id MUST NOT error. The daemon
// shutdown path calls this once and shouldn't fail if a prior shutdown
// already cleared the registration.
func TestDeleteView_404IsSoftSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"view not found"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")

	if err := c.DeleteView(context.Background(), 99); err != nil {
		t.Fatalf("DeleteView with 404: %v (want nil — idempotent)", err)
	}
}

// TestDeleteView_NonNotFoundErrorPropagates surfaces non-2xx, non-404
// failures so the caller can decide whether to retry or escalate.
func TestDeleteView_NonNotFoundErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")

	err := c.DeleteView(context.Background(), 1)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	var he *HTTPError
	if !errors.As(err, &he) || he.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 HTTPError, got %v", err)
	}
}
