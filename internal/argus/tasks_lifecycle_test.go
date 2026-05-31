package argus

import (
	"context"
	"io"
	"net/http"
	"testing"
)

// TestClient_DeleteTask asserts DELETE /api/tasks/{id} is issued with the
// task id path-escaped. Argus cleans the worktree + branch server-side; hera
// only needs the call to land.
func TestClient_DeleteTask(t *testing.T) {
	var gotMethod, gotPath string
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"status":"deleted"}`)
	})
	defer srv.Close()

	if err := c.DeleteTask(context.Background(), "T1"); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	if gotMethod != "DELETE" {
		t.Fatalf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/api/tasks/T1" {
		t.Fatalf("path = %q, want /api/tasks/T1", gotPath)
	}
}

// TestClient_DeleteTask_Error surfaces a non-2xx (other than 404) as an error.
func TestClient_DeleteTask_Error(t *testing.T) {
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"boom"}`)
	})
	defer srv.Close()

	if err := c.DeleteTask(context.Background(), "T1"); err == nil {
		t.Fatalf("DeleteTask: want error on 500, got nil")
	}
}

// TestClient_DeleteTask_NotFound treats a 404 as success: deletion is
// idempotent, so a task argus already removed is "already gone" and must
// not abort a `^d` cascade or `^r` prune.
func TestClient_DeleteTask_NotFound(t *testing.T) {
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"task not found"}`)
	})
	defer srv.Close()

	if err := c.DeleteTask(context.Background(), "missing"); err != nil {
		t.Fatalf("DeleteTask: want nil on 404 (idempotent), got %v", err)
	}
}

// TestClient_SetTaskStatus asserts POST /api/tasks/{id}/status carries the
// requested status string and returns argus's resolved status.
func TestClient_SetTaskStatus(t *testing.T) {
	var gotPath, gotBody string
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = io.WriteString(w, `{"status":"in_review"}`)
	})
	defer srv.Close()

	got, err := c.SetTaskStatus(context.Background(), "T1", "in_review")
	if err != nil {
		t.Fatalf("SetTaskStatus: %v", err)
	}
	if gotPath != "/api/tasks/T1/status" {
		t.Fatalf("path = %q", gotPath)
	}
	if !contains(gotBody, `"in_review"`) {
		t.Fatalf("body = %q, want it to carry in_review", gotBody)
	}
	if got != "in_review" {
		t.Fatalf("returned status = %q, want in_review", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
