package argus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// newTestServerAndClient returns an httptest.Server backed by handler, and
// a *Client pointed at it.
func newTestServerAndClient(handler http.HandlerFunc) (*httptest.Server, *Client) {
	srv := httptest.NewServer(handler)
	c := New(srv.URL, "test-token")
	return srv, c
}

func TestClient_SendsAuthAndVersion(t *testing.T) {
	var gotAuth, gotVersion string
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("X-Argus-Plugin-Version")
		_, _ = w.Write([]byte(`{"tasks":[]}`))
	})
	defer srv.Close()

	if _, err := c.ListTasks(context.Background()); err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotVersion != "1" {
		t.Fatalf("X-Argus-Plugin-Version = %q", gotVersion)
	}
}

func TestClient_ListTasks(t *testing.T) {
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"tasks":[{"id":"t1","name":"first","project":"p","status":"in_progress","worktree_path":"/tmp/wt"}]}`)
	})
	defer srv.Close()

	tasks, err := c.ListTasks(context.Background())
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("got %d tasks, want 1", len(tasks))
	}
	if tasks[0].ID != "t1" || tasks[0].WorktreePath != "/tmp/wt" {
		t.Fatalf("unexpected task: %+v", tasks[0])
	}
}

func TestClient_GetTask(t *testing.T) {
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/tasks/") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"id":"t1","name":"first","project":"p","status":"in_progress","worktree_path":"/tmp/wt"}`)
	})
	defer srv.Close()

	task, err := c.GetTask(context.Background(), "t1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.ID != "t1" {
		t.Fatalf("task.ID = %q", task.ID)
	}
}

func TestClient_PutTaskMeta(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	if err := c.PutTaskMeta(context.Background(), "t1", "role", "coordinator"); err != nil {
		t.Fatalf("PutTaskMeta: %v", err)
	}
	if gotMethod != "PUT" {
		t.Fatalf("method = %s", gotMethod)
	}
	if gotPath != "/api/tasks/t1/meta" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotBody["key"] != "role" || gotBody["value"] != "coordinator" {
		t.Fatalf("body = %+v", gotBody)
	}
}

func TestClient_GetTaskMeta_WithNamespace(t *testing.T) {
	var gotQuery string
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"entries":[{"namespace":"hera","key":"role","value":"worker","updated_at":"2026-05-24T00:00:00Z"}]}`)
	})
	defer srv.Close()

	entries, err := c.GetTaskMeta(context.Background(), "t1", "hera")
	if err != nil {
		t.Fatalf("GetTaskMeta: %v", err)
	}
	if gotQuery != "namespace=hera" {
		t.Fatalf("query = %q", gotQuery)
	}
	if len(entries) != 1 || entries[0].Key != "role" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestClient_PostTaskInput(t *testing.T) {
	var gotBody []byte
	var gotContentType string
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		_, _ = io.WriteString(w, `{"status":"ok","bytes":12}`)
	})
	defer srv.Close()

	n, err := c.PostTaskInput(context.Background(), "t1", []byte("hello world\n"))
	if err != nil {
		t.Fatalf("PostTaskInput: %v", err)
	}
	if n != 12 {
		t.Fatalf("n = %d, want 12", n)
	}
	if string(gotBody) != "hello world\n" {
		t.Fatalf("body = %q", string(gotBody))
	}
	if gotContentType != "text/plain" {
		t.Fatalf("Content-Type = %q", gotContentType)
	}
}

func TestClient_RegisterTool(t *testing.T) {
	var gotBody MCPTool
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"name":"hera_send","scope":"hera"}`)
	})
	defer srv.Close()

	tool := MCPTool{
		Name:        "hera_send",
		Description: "Send a message",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"cwd":  map[string]any{"type": "string"},
				"body": map[string]any{"type": "string"},
			},
		},
		CallbackURL: "http://127.0.0.1:7744/mcp/hera_send",
		AuthHeader:  "Bearer secret",
	}
	resp, err := c.RegisterTool(context.Background(), tool)
	if err != nil {
		t.Fatalf("RegisterTool: %v", err)
	}
	if resp.Name != "hera_send" {
		t.Fatalf("resp.Name = %s", resp.Name)
	}
	if gotBody.AuthHeader != "Bearer secret" {
		t.Fatalf("auth header not propagated")
	}
}

func TestClient_UnregisterTool(t *testing.T) {
	var gotMethod, gotPath string
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	if err := c.UnregisterTool(context.Background(), "hera_send"); err != nil {
		t.Fatalf("UnregisterTool: %v", err)
	}
	if gotMethod != "DELETE" {
		t.Fatalf("method = %s", gotMethod)
	}
	if gotPath != "/api/mcp/tools/hera_send" {
		t.Fatalf("path = %s", gotPath)
	}
}

func TestClient_DoJSON_Errors(t *testing.T) {
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad token"}`, http.StatusUnauthorized)
	})
	defer srv.Close()

	_, err := c.ListTasks(context.Background())
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 in error, got %v", err)
	}
}

func TestClient_StreamEvents(t *testing.T) {
	// Build a small SSE payload with three events plus a keep-alive comment.
	events := []string{
		"event: task.created\ndata: " + mustJSON(Event{ID: 1, Type: "task.created", TaskID: "t1"}) + "\n\n",
		":ping\n",
		"event: link.created\ndata: " + mustJSON(Event{ID: 2, Type: "link.created", TaskID: "t2"}) + "\n\n",
		"event: session.idle\ndata: " + mustJSON(Event{ID: 3, Type: "session.idle", TaskID: "t1"}) + "\n\n",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("since") == "" {
			t.Errorf("expected since= param")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, e := range events {
			_, _ = io.WriteString(w, e)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")

	var got []Event
	var mu sync.Mutex
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// StreamEvents blocks until ctx is canceled. We run it in a goroutine,
	// cancel after the handler hangs up.
	done := make(chan struct{})
	go func() {
		_ = c.StreamEvents(ctx, 1, func(ev Event) {
			mu.Lock()
			got = append(got, ev)
			mu.Unlock()
			if len(got) >= len(events)-1 { // -1 for the ":ping" comment
				cancel()
			}
		})
		close(done)
	}()

	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(got) < 3 {
		t.Fatalf("got %d events, want at least 3: %+v", len(got), got)
	}
	if got[0].Type != "task.created" || got[0].TaskID != "t1" {
		t.Fatalf("event[0] = %+v", got[0])
	}
	if got[1].Type != "link.created" {
		t.Fatalf("event[1] = %+v", got[1])
	}
	if got[2].Type != "session.idle" {
		t.Fatalf("event[2] = %+v", got[2])
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("marshal: %v", err))
	}
	return string(b)
}
