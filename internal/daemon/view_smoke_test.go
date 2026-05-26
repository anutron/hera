package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/config"
	"github.com/anutron/hera/internal/db"
)

// fakeArgusForSmoke is a purpose-built argus stub for the Stage K smoke
// test. It covers every endpoint daemon.Start touches at boot plus the
// /api/tasks/{id}/input endpoint the per-connection KeyRouter forwards
// pane-focus keystrokes to.
type fakeArgusForSmoke struct {
	mu sync.Mutex

	// MCP tool + settings-section + plugin-view registration (drained,
	// not asserted — covered by run_test.go).
	streamClose chan struct{}

	viewID int64

	// PTY proxy seeding state.
	taskStreamHold chan struct{}

	// What we actually assert on:
	inputs map[string][][]byte // taskID -> ordered payloads received
}

func newFakeArgusForSmoke() *fakeArgusForSmoke {
	return &fakeArgusForSmoke{
		streamClose:    make(chan struct{}),
		taskStreamHold: make(chan struct{}),
		inputs:         make(map[string][][]byte),
	}
}

func (f *fakeArgusForSmoke) inputsFor(taskID string) [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	src := f.inputs[taskID]
	out := make([][]byte, len(src))
	for i, p := range src {
		c := make([]byte, len(p))
		copy(c, p)
		out[i] = c
	}
	return out
}

func (f *fakeArgusForSmoke) totalInputs() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, lst := range f.inputs {
		n += len(lst)
	}
	return n
}

func (f *fakeArgusForSmoke) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/events/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		select {
		case <-f.streamClose:
		case <-r.Context().Done():
		}
	})
	mux.HandleFunc("/api/mcp/tools", func(w http.ResponseWriter, r *http.Request) {
		var body argus.MCPTool
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"name":"`+body.Name+`","scope":"hera"}`)
	})
	mux.HandleFunc("/api/mcp/tools/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/plugins/settings/sections", func(w http.ResponseWriter, r *http.Request) {
		var body argus.SettingsSectionDefinition
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"name":"`+body.Name+`"}`)
	})
	mux.HandleFunc("/api/plugins/settings/sections/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"tasks":[]}`)
	})
	mux.HandleFunc("/api/plugins/views", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var body argus.PluginView
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.viewID++
			id := f.viewID
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w,
				`{"id":`+strconv.FormatInt(id, 10)+
					`,"scope":"hera","title":`+strconv.Quote(body.Title)+
					`,"hotkey":`+strconv.Quote(body.Hotkey)+
					`,"callback_url":`+strconv.Quote(body.CallbackURL)+`}`)
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"views":[]}`)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/plugins/views/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
		switch {
		case strings.HasSuffix(path, "/output"):
			w.Header().Set("X-Output-Total", "0")
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(path, "/stream"):
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			select {
			case <-f.taskStreamHold:
			case <-r.Context().Done():
			}
		case strings.HasSuffix(path, "/input"):
			taskID := strings.TrimSuffix(path, "/input")
			body, _ := io.ReadAll(r.Body)
			f.mu.Lock()
			f.inputs[taskID] = append(f.inputs[taskID], body)
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"status":"ok","bytes":`+strconv.Itoa(len(body))+`}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return mux
}

// smokeTestDaemon brings up a fresh daemon pointing at fake argus and
// returns the test-ready handles. Caller is responsible for d.Stop and
// closing fake.streamClose / fake.taskStreamHold.
func smokeTestDaemon(t *testing.T, preSeed func(*db.DB)) (*Daemon, *fakeArgusForSmoke, *httptest.Server) {
	t.Helper()
	fake := newFakeArgusForSmoke()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(func() {
		close(fake.streamClose)
		close(fake.taskStreamHold)
		srv.Close()
	})

	apiPort := extractPort(t, srv.URL)
	sockSvc := &FakeArgusSocketRPC{apiPort: apiPort}
	sockPath, stopSock := startFakeArgusSocket(t, sockSvc)
	t.Cleanup(stopSock)

	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "api-token"), []byte("test-token\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	pidPath := filepath.Join(stateDir, "fake-argus.pid")
	if err := os.WriteFile(pidPath, []byte("1\n"), 0o644); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	cfg := &config.Config{
		StateDir:        stateDir,
		ArgusBaseURL:    srv.URL,
		ListenAddr:      "127.0.0.1:0",
		IdleDebounce:    100 * time.Millisecond,
		MCPHeartbeat:    24 * time.Hour,
		ArgusSocketPath: sockPath,
		ArgusPIDPath:    pidPath,
	}

	if preSeed != nil {
		database, err := db.Open(cfg.StatePath())
		if err != nil {
			t.Fatalf("preSeed db open: %v", err)
		}
		preSeed(database)
		if err := database.Close(); err != nil {
			t.Fatalf("preSeed db close: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	d, err := Start(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { d.Stop(context.Background()) })
	return d, fake, srv
}

// seedCoordAndWorker seeds the smallest binding fixture BuildApp's
// bindInitialSelection picks up: one orchestrator with a coord role
// bound to "task-coord" and a worker role bound to "task-worker".
func seedCoordAndWorker(t *testing.T) func(*db.DB) {
	t.Helper()
	return func(database *db.DB) {
		ctx := context.Background()
		orch, err := database.Orchestrators.Create(ctx, "smoke")
		if err != nil {
			t.Fatalf("seed orch: %v", err)
		}
		coordRole, err := database.Roles.Create(ctx, db.CreateRoleInput{
			OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "smoke",
		})
		if err != nil {
			t.Fatalf("seed coord role: %v", err)
		}
		workerRole, err := database.Roles.Create(ctx, db.CreateRoleInput{
			OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "smoke",
		})
		if err != nil {
			t.Fatalf("seed worker role: %v", err)
		}
		if _, err := database.Bindings.Create(ctx, db.CreateBindingInput{
			RoleID: coordRole.ID, ArgusTaskID: "task-coord", WorktreePath: "/tmp/coord",
		}); err != nil {
			t.Fatalf("seed coord binding: %v", err)
		}
		if _, err := database.Bindings.Create(ctx, db.CreateBindingInput{
			RoleID: workerRole.ID, ArgusTaskID: "task-worker", WorktreePath: "/tmp/worker",
		}); err != nil {
			t.Fatalf("seed worker binding: %v", err)
		}
	}
}

func dialView(t *testing.T, d *Daemon) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	url := "ws://" + d.MCPServer.Addr() + "/view"
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial /view: %v", err)
	}
	return conn
}

// frameReader runs a single conn.Read loop in a goroutine and surfaces
// binary frames on its channel. It's the test-side analogue of argus's
// connector reading from the plugin. coder/websocket poisons the
// connection when the ctx passed into Read is cancelled, so we MUST use
// a long-lived ctx for reads and gate completion via a separate timer.
type frameReader struct {
	conn   *websocket.Conn
	binary chan []byte
	done   chan error
	cancel context.CancelFunc
}

func newFrameReader(conn *websocket.Conn) *frameReader {
	ctx, cancel := context.WithCancel(context.Background())
	fr := &frameReader{
		conn:   conn,
		binary: make(chan []byte, 64),
		done:   make(chan error, 1),
		cancel: cancel,
	}
	go func() {
		defer close(fr.binary)
		defer close(fr.done)
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				fr.done <- err
				return
			}
			if typ != websocket.MessageBinary {
				continue
			}
			fr.binary <- data
		}
	}()
	return fr
}

// stop tears down the reader. Safe to call multiple times.
func (fr *frameReader) stop() { fr.cancel() }

// drainBinary blocks reading binary frames from conn until `quiet`
// elapses without a frame, or `cap` bytes have accumulated. Returns the
// concatenated bytes. Unlike a per-read context, this keeps the
// underlying WebSocket alive for subsequent writes.
func (fr *frameReader) drainBinary(quiet time.Duration, cap int) []byte {
	var combined bytes.Buffer
	for {
		select {
		case data, ok := <-fr.binary:
			if !ok {
				return combined.Bytes()
			}
			combined.Write(data)
			if combined.Len() >= cap {
				return combined.Bytes()
			}
		case <-time.After(quiet):
			return combined.Bytes()
		}
	}
}

// TestViewSmoke_RenderAndKeyRouting is the end-to-end Stage K smoke
// test. It spins the daemon up in-process against a fake argus, opens a
// WebSocket against /view, sends a resize envelope, asserts outbound
// binary frames carry well-formed ANSI, then drives a Ctrl-Right key
// (RAIL → COORD focus) followed by a literal 'x' and asserts the daemon
// POSTs the 'x' byte to the coord task's /input endpoint.
func TestViewSmoke_RenderAndKeyRouting(t *testing.T) {
	d, fake, _ := smokeTestDaemon(t, seedCoordAndWorker(t))

	conn := dialView(t, d)
	defer conn.CloseNow()
	reader := newFrameReader(conn)
	defer reader.stop()

	ctx := context.Background()

	// 1. Send a resize envelope. tview will lay out and emit a full
	// surface; the wsscreen should turn that into one or more binary
	// frames carrying ANSI bytes.
	if err := conn.Write(ctx, websocket.MessageText,
		[]byte(`{"type":"resize","cols":80,"rows":24}`)); err != nil {
		t.Fatalf("write resize: %v", err)
	}

	// 2. Drain binary frames until quiet. The combined buffer must
	// contain at least one ESC byte and one tview-known cell — we look
	// for the 'H' from the "HERA" top-bar label as a cheap proxy for
	// "the layout actually rendered."
	rendered := reader.drainBinary(750*time.Millisecond, 64*1024)
	if len(rendered) == 0 {
		t.Fatal("no outbound binary frames after resize")
	}
	if !bytes.ContainsRune(rendered, 0x1b) {
		t.Fatalf("outbound frames contain no ANSI ESC bytes (%d bytes): %q", len(rendered), trim(rendered, 80))
	}
	if !bytes.Contains(rendered, []byte("HERA")) {
		t.Fatalf("outbound frames missing top-bar text HERA (%d bytes): %q", len(rendered), trim(rendered, 200))
	}

	// 3. Send Ctrl-Right: CSI 1;5 C. tcell parses this as KeyRight +
	// ModCtrl, which KeyRouter consumes and advances focus RAIL → COORD.
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("\x1b[1;5C")); err != nil {
		t.Fatalf("write ctrl-right: %v", err)
	}

	// 4. Send a literal 'x'. In COORD focus the router forwards it as a
	// byte to PostTaskInput against the coord-bound task ("task-coord").
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("x")); err != nil {
		t.Fatalf("write x: %v", err)
	}

	// 5. Poll for the input POST. ~2s is plenty for the tview pump to
	// process two key events under -race.
	if !waitFor(2*time.Second, func() bool {
		return len(fake.inputsFor("task-coord")) >= 1
	}) {
		t.Fatalf("no POST to /api/tasks/task-coord/input after 2s (total inputs across tasks: %d)", fake.totalInputs())
	}

	// The first input MUST be a single 'x' byte routed to the coord task,
	// not the worker — that's the AGENT focus target which would have
	// fired if focus advanced two steps instead of one.
	got := fake.inputsFor("task-coord")[0]
	if !bytes.Equal(got, []byte("x")) {
		t.Errorf("coord input payload = %q, want \"x\"", got)
	}
	if n := len(fake.inputsFor("task-worker")); n != 0 {
		t.Errorf("worker received %d inputs, want 0 (Ctrl-Right should advance only one step)", n)
	}
}

// TestViewSmoke_LastWriterWinsClosesPrior pins the Stage E + K contract
// from the operator's perspective: a second WebSocket connection
// supersedes the first; reading on the prior connection MUST return an
// error within a bounded window.
func TestViewSmoke_LastWriterWinsClosesPrior(t *testing.T) {
	d, _, _ := smokeTestDaemon(t, seedCoordAndWorker(t))

	first := dialView(t, d)
	defer first.CloseNow()
	firstReader := newFrameReader(first)
	defer firstReader.stop()

	// Let the first session render at least once so the path is fully
	// established before we supersede it.
	_ = firstReader.drainBinary(300*time.Millisecond, 4096)

	second := dialView(t, d)
	defer second.CloseNow()

	// firstReader's read loop sees the supersede close as an error on
	// `done`. Wait up to 2s for it.
	select {
	case <-firstReader.done:
		// prior conn closed — last-writer-wins satisfied
	case <-time.After(2 * time.Second):
		t.Fatal("prior WebSocket did not close after a second connection arrived")
	}
}

// trim returns the first n bytes of b with non-printable runs replaced by
// dots; used in error messages.
func trim(b []byte, n int) []byte {
	if len(b) > n {
		b = b[:n]
	}
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 32 && c < 127 {
			out[i] = c
		} else {
			out[i] = '.'
		}
	}
	return out
}

// waitFor polls fn every 20 ms until it returns true or timeout elapses.
// Returns the last fn() result.
func waitFor(timeout time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fn()
}
