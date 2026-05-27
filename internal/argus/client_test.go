package argus

import (
	"context"
	"encoding/json"
	"errors"
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

func TestClient_GetTaskSize(t *testing.T) {
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks/t1/size" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"cols":189,"rows":69}`)
	})
	defer srv.Close()

	cols, rows, err := c.GetTaskSize(context.Background(), "t1")
	if err != nil {
		t.Fatalf("GetTaskSize: %v", err)
	}
	if cols != 189 || rows != 69 {
		t.Fatalf("cols=%d rows=%d, want 189x69", cols, rows)
	}
}

func TestClient_GetTaskSize_404(t *testing.T) {
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"no active session"}`)
	})
	defer srv.Close()

	_, _, err := c.GetTaskSize(context.Background(), "t1")
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, ErrNoTaskSize) {
		t.Fatalf("err = %v, want ErrNoTaskSize", err)
	}
}

func TestClient_ResizeTask(t *testing.T) {
	var gotMethod, gotPath, gotCT string
	var gotBody resizeTaskInput
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_, _ = io.WriteString(w, `{"cols":145,"rows":50,"rerendered":true}`)
	})
	defer srv.Close()

	if err := c.ResizeTask(context.Background(), "t1", 145, 50); err != nil {
		t.Fatalf("ResizeTask: %v", err)
	}
	if gotMethod != "POST" {
		t.Fatalf("method = %s", gotMethod)
	}
	if gotPath != "/api/tasks/t1/size" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotCT != "application/json" {
		t.Fatalf("Content-Type = %q", gotCT)
	}
	if gotBody.Cols != 145 || gotBody.Rows != 50 {
		t.Fatalf("body = %+v, want {Cols:145,Rows:50}", gotBody)
	}
}

func TestClient_ResizeTask_404(t *testing.T) {
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"no active session"}`)
	})
	defer srv.Close()

	err := c.ResizeTask(context.Background(), "t1", 145, 50)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, ErrNoTaskSize) {
		t.Fatalf("err = %v, want ErrNoTaskSize", err)
	}
}

func TestClient_ResizeTask_400(t *testing.T) {
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"cols out of range"}`)
	})
	defer srv.Close()

	err := c.ResizeTask(context.Background(), "t1", 145, 50)
	if err == nil {
		t.Fatalf("expected error")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if httpErr.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", httpErr.StatusCode)
	}
}

func TestClient_ResizeTask_5xx(t *testing.T) {
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"boom"}`)
	})
	defer srv.Close()

	err := c.ResizeTask(context.Background(), "t1", 145, 50)
	if err == nil {
		t.Fatalf("expected error")
	}
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("err = %v, want *HTTPError", err)
	}
	if httpErr.StatusCode != 500 {
		t.Fatalf("status = %d, want 500", httpErr.StatusCode)
	}
}

func TestClient_ResizeTask_RejectsOutOfBoundDims(t *testing.T) {
	c := New("http://unused", "tok")
	cases := []struct {
		name       string
		cols, rows int
	}{
		{"zero cols", 0, 24},
		{"zero rows", 80, 0},
		{"negative cols", -1, 24},
		{"oversized cols", 1001, 24},
		{"oversized rows", 80, 1001},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := c.ResizeTask(context.Background(), "t1", tc.cols, tc.rows); err == nil {
				t.Fatalf("expected validation error for %dx%d", tc.cols, tc.rows)
			}
		})
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

// TestClient_PostTaskInput_BytesAsString regresses the v1 dogfood bug
// where argus encodes the `bytes` field as a JSON string (e.g.
// `{"bytes":"12"}`). The original `Bytes int` struct tag rejected this
// with "cannot unmarshal string into Go struct field
// postTaskInputResponse.bytes of type int", surfacing as a hera_send
// tool-call error even though the underlying POST succeeded. The
// flexInt-style tolerant unmarshaler keeps the call green for either
// shape on the wire.
func TestClient_PostTaskInput_BytesAsString(t *testing.T) {
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"status":"ok","bytes":"12"}`)
	})
	defer srv.Close()

	n, err := c.PostTaskInput(context.Background(), "t1", []byte("hello"))
	if err != nil {
		t.Fatalf("PostTaskInput with stringified bytes: %v", err)
	}
	if n != 12 {
		t.Fatalf("n = %d, want 12", n)
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

func TestClient_CreateTask(t *testing.T) {
	var gotMethod, gotPath, gotCT string
	var gotBody CreateTaskInput
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"t42","name":"foo-coord","status":"in_progress"}`)
	})
	defer srv.Close()

	got, err := c.CreateTask(context.Background(), CreateTaskInput{
		Project: "foo",
		Name:    "foo-coord",
		Prompt:  "hera_new_orchestrator(cwd=$PWD, name=\"foo\", coord_role_name=\"coord\")",
	}, nil)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if gotMethod != "POST" || gotPath != "/api/tasks" {
		t.Fatalf("method/path = %s %s", gotMethod, gotPath)
	}
	if gotCT != "application/json" {
		t.Fatalf("Content-Type = %q", gotCT)
	}
	if gotBody.Project != "foo" || gotBody.Name != "foo-coord" {
		t.Fatalf("body = %+v", gotBody)
	}
	if !strings.Contains(gotBody.Prompt, "hera_new_orchestrator") {
		t.Fatalf("prompt missing bootstrap: %q", gotBody.Prompt)
	}
	if got.ID != "t42" || got.Name != "foo-coord" {
		t.Fatalf("response = %+v", got)
	}
}

func TestClient_CreateTask_ValidatesProject(t *testing.T) {
	c := New("http://unused", "tok")
	_, err := c.CreateTask(context.Background(), CreateTaskInput{Prompt: "x"}, nil)
	if err == nil || !strings.Contains(err.Error(), "project is required") {
		t.Fatalf("expected project-required error, got %v", err)
	}
}

func TestClient_CreateTask_ValidatesPromptOrName(t *testing.T) {
	c := New("http://unused", "tok")
	_, err := c.CreateTask(context.Background(), CreateTaskInput{Project: "p"}, nil)
	if err == nil || !strings.Contains(err.Error(), "prompt or name is required") {
		t.Fatalf("expected prompt-or-name-required error, got %v", err)
	}
}

func TestClient_CreateTask_PutsMeta(t *testing.T) {
	var metaCalls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/tasks":
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"t1","name":"n","status":"in_progress"}`)
		case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/api/tasks/t1/meta"):
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			metaCalls = append(metaCalls, body["key"]+"="+body["value"])
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "tok")
	_, err := c.CreateTask(context.Background(), CreateTaskInput{
		Project: "p", Prompt: "go",
	}, map[string]string{"hera.role": "coordinator"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if len(metaCalls) != 1 || metaCalls[0] != "hera.role=coordinator" {
		t.Fatalf("meta calls = %v", metaCalls)
	}
}

func TestClient_CreateTask_MetaErrorReturnsTaskID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/tasks":
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"t1","name":"n","status":"in_progress"}`)
		default:
			http.Error(w, `{"error":"meta failed"}`, http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	c := New(srv.URL, "tok")
	task, err := c.CreateTask(context.Background(), CreateTaskInput{
		Project: "p", Prompt: "go",
	}, map[string]string{"k": "v"})
	if err == nil {
		t.Fatalf("expected meta-PUT error")
	}
	if task == nil || task.ID != "t1" {
		t.Fatalf("partial task id should be returned even on meta failure; got %+v", task)
	}
}

func TestClient_CreateProject(t *testing.T) {
	var gotPath string
	var gotBody CreateProjectInput
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
	})
	defer srv.Close()

	err := c.CreateProject(context.Background(), CreateProjectInput{Name: "foo", Path: "/tmp/foo"})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if gotPath != "/api/projects" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotBody.Name != "foo" || gotBody.Path != "/tmp/foo" {
		t.Fatalf("body = %+v", gotBody)
	}
}

func TestClient_CreateProject_ValidatesInput(t *testing.T) {
	c := New("http://unused", "tok")
	if err := c.CreateProject(context.Background(), CreateProjectInput{Name: "x"}); err == nil {
		t.Fatalf("expected validation error when Path empty")
	}
	if err := c.CreateProject(context.Background(), CreateProjectInput{Path: "/tmp"}); err == nil {
		t.Fatalf("expected validation error when Name empty")
	}
}

func TestClient_ArchiveTask(t *testing.T) {
	var gotMethod, gotPath string
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	if err := c.ArchiveTask(context.Background(), "t1"); err != nil {
		t.Fatalf("ArchiveTask: %v", err)
	}
	if gotMethod != "POST" {
		t.Fatalf("method = %s", gotMethod)
	}
	if gotPath != "/api/tasks/t1/archive" {
		t.Fatalf("path = %s", gotPath)
	}
}

func TestClient_UnarchiveTask(t *testing.T) {
	var gotMethod, gotPath string
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	if err := c.UnarchiveTask(context.Background(), "t1"); err != nil {
		t.Fatalf("UnarchiveTask: %v", err)
	}
	if gotMethod != "POST" {
		t.Fatalf("method = %s", gotMethod)
	}
	if gotPath != "/api/tasks/t1/unarchive" {
		t.Fatalf("path = %s", gotPath)
	}
}
