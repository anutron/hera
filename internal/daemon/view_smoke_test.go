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

	// liveTaskIDs are reported by GET /api/tasks as active, non-archived so
	// the rail renders them as selectable agent rows.
	liveTaskIDs []string

	// What we actually assert on:
	inputs map[string][][]byte // taskID -> ordered payloads received
}

func newFakeArgusForSmoke() *fakeArgusForSmoke {
	return &fakeArgusForSmoke{
		streamClose:    make(chan struct{}),
		taskStreamHold: make(chan struct{}),
		inputs:         make(map[string][][]byte),
		// Default to reporting the seedCoordAndWorker pair as live so the rail
		// renders the worker as an active, selectable agent row.
		liveTaskIDs: []string{"task-coord", "task-worker"},
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
		// Report the seeded coord+worker tasks as live, non-archived so the
		// argus state cache marks them active and the rail renders the worker
		// as a selectable agent row (not Dead/archived). Without this the
		// cache (Ready, but with no entry) would mark every bound row Dead and
		// hide it, breaking the Enter-into-pane navigation under test.
		f.mu.Lock()
		ids := f.liveTaskIDs
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		var sb strings.Builder
		sb.WriteString(`{"tasks":[`)
		for i, id := range ids {
			if i > 0 {
				sb.WriteString(",")
			}
			sb.WriteString(`{"id":` + strconv.Quote(id) +
				`,"name":` + strconv.Quote(id) +
				`,"status":"in_progress","idle":false,"archived":false,"project":"smoke"}`)
		}
		sb.WriteString(`]}`)
		_, _ = io.WriteString(w, sb.String())
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
		case path != "" && !strings.Contains(path, "/"):
			// Bare /api/tasks/{id} — hera calls this from
			// findInitialSelection to filter out completed tasks. The fake
			// has no per-task state machine, so report every seeded task
			// as still in_progress.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w,
				`{"id":`+strconv.Quote(path)+
					`,"name":`+strconv.Quote(path)+
					`,"status":"in_progress"}`)
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
	text   chan []byte
	done   chan error
	cancel context.CancelFunc
}

func newFrameReader(conn *websocket.Conn) *frameReader {
	ctx, cancel := context.WithCancel(context.Background())
	fr := &frameReader{
		conn:   conn,
		binary: make(chan []byte, 64),
		text:   make(chan []byte, 64),
		done:   make(chan error, 1),
		cancel: cancel,
	}
	go func() {
		defer close(fr.binary)
		defer close(fr.text)
		defer close(fr.done)
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				fr.done <- err
				return
			}
			switch typ {
			case websocket.MessageBinary:
				fr.binary <- data
			case websocket.MessageText:
				select {
				case fr.text <- data:
				default:
				}
			}
		}
	}()
	return fr
}

// drainText collects text (control) frames from conn until `quiet` elapses
// without a frame. Returns each frame's raw bytes in arrival order.
func (fr *frameReader) drainText(quiet time.Duration) [][]byte {
	var out [][]byte
	for {
		select {
		case data, ok := <-fr.text:
			if !ok {
				return out
			}
			out = append(out, data)
		case <-time.After(quiet):
			return out
		}
	}
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
	defer func() { _ = conn.CloseNow() }()
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

// TestViewSmoke_EnterIntoPaneThenTypeForwards is the end-to-end regression for
// the reported E1 bug: the operator presses Enter on the rail selection to step
// INTO a pane (the in-argus way in, since argus eats the Cmd/Ctrl-arrow ladder),
// then types a printable key. That key MUST be forwarded to the focused pane's
// bound argus task /input endpoint. The earlier smoke test only exercised the
// Ctrl-Right focus ladder; this drives the Enter path that real operators use.
func TestViewSmoke_EnterIntoPaneThenTypeForwards(t *testing.T) {
	d, fake, _ := smokeTestDaemon(t, seedCoordAndWorker(t))

	conn := dialView(t, d)
	defer func() { _ = conn.CloseNow() }()
	reader := newFrameReader(conn)
	defer reader.stop()
	ctx := context.Background()

	if err := conn.Write(ctx, websocket.MessageText,
		[]byte(`{"type":"resize","cols":120,"rows":40}`)); err != nil {
		t.Fatalf("write resize: %v", err)
	}
	// Let the initial render + rail selection settle so CurrentRef is set.
	_ = reader.drainBinary(750*time.Millisecond, 64*1024)

	// Move the rail cursor down to the worker row (the orchestrator header is
	// the initial cursor; 'j' steps onto its nested worker). This is the
	// reported scenario: "select an AGENT row and press Enter".
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("j")); err != nil {
		t.Fatalf("write j: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Enter (CR, 0x0d) from RAIL on the worker selection steps into the AGENT
	// pane bound to "task-worker".
	if err := conn.Write(ctx, websocket.MessageBinary, []byte{'\r'}); err != nil {
		t.Fatalf("write enter: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Now type a literal 'Z'. It MUST be forwarded to the focused pane's PTY.
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("Z")); err != nil {
		t.Fatalf("write Z: %v", err)
	}

	if !waitFor(2*time.Second, func() bool {
		return fake.totalInputs() >= 1
	}) {
		t.Fatalf("Enter-into-pane then 'Z' forwarded NO byte to any /input endpoint "+
			"(total inputs: %d) — keystrokes are not reaching the focused pane's PTY (E1)",
			fake.totalInputs())
	}

	// The 'Z' must have landed on the worker task (the initial agent selection),
	// not been dropped or misrouted.
	got := fake.inputsFor("task-worker")
	if len(got) == 0 {
		t.Fatalf("'Z' was not forwarded to task-worker; inputs=coord:%d worker:%d",
			len(fake.inputsFor("task-coord")), len(fake.inputsFor("task-worker")))
	}
	if !bytes.Equal(got[len(got)-1], []byte("Z")) {
		t.Errorf("last worker input payload = %q, want \"Z\"", got[len(got)-1])
	}
}

// TestViewSmoke_LastWriterWinsClosesPrior pins the Stage E + K contract
// from the operator's perspective: a second WebSocket connection
// supersedes the first; reading on the prior connection MUST return an
// error within a bounded window.
func TestViewSmoke_LastWriterWinsClosesPrior(t *testing.T) {
	d, _, _ := smokeTestDaemon(t, seedCoordAndWorker(t))

	first := dialView(t, d)
	defer func() { _ = first.CloseNow() }()
	firstReader := newFrameReader(first)
	defer firstReader.stop()

	// Let the first session render at least once so the path is fully
	// established before we supersede it.
	_ = firstReader.drainBinary(300*time.Millisecond, 4096)

	second := dialView(t, d)
	defer func() { _ = second.CloseNow() }()

	// firstReader's read loop sees the supersede close as an error on
	// `done`. Wait up to 2s for it.
	select {
	case <-firstReader.done:
		// prior conn closed — last-writer-wins satisfied
	case <-time.After(2 * time.Second):
		t.Fatal("prior WebSocket did not close after a second connection arrived")
	}
}

// TestViewSmoke_FocusFeedbackReflectsState drives the focus ladder over raw
// key bytes and asserts hera advertises a focus-aware hotkeys control frame to
// argus on connect and on every focus change (D12, key-surrender contract).
// Hera renders no bottom bar of its own; argus draws the plugin-mode status
// bar from these frames. The frame is a TEXT envelope of shape
// {"type":"hotkeys","items":[...]} whose items reflect the current focus
// state.
func TestViewSmoke_FocusFeedbackReflectsState(t *testing.T) {
	d, _, _ := smokeTestDaemon(t, seedCoordAndWorker(t))
	conn := dialView(t, d)
	defer func() { _ = conn.CloseNow() }()
	reader := newFrameReader(conn)
	defer reader.stop()
	ctx := context.Background()

	if err := conn.Write(ctx, websocket.MessageText,
		[]byte(`{"type":"resize","cols":120,"rows":40}`)); err != nil {
		t.Fatalf("write resize: %v", err)
	}

	// hera MUST NOT render its own bottom-bar focus label anywhere on the
	// surface — that chrome belongs to argus now.
	rendered := reader.drainBinary(750*time.Millisecond, 64*1024)
	for _, label := range []string{"[RAIL]", "[COORD]", "[AGENT]"} {
		if bytes.Contains(rendered, []byte(label)) {
			t.Fatalf("surface must not render hera's own bottom-bar label %q: %q",
				label, trim(rendered, 240))
		}
	}

	// On connect hera pushes a RAIL hotkeys frame (rail-specific bindings).
	initialKeys := mustHotkeyLabels(t, reader.drainText(750*time.Millisecond))
	if !strings.Contains(initialKeys, "move") || !strings.Contains(initialKeys, "argus") {
		t.Fatalf("initial hotkeys frame does not reflect RAIL focus: %q", initialKeys)
	}

	// Ctrl-Right (CSI 1;5 C) advances RAIL → COORD. hera MUST push a new
	// hotkeys frame reflecting COORD focus (a "coord PTY" binding, and no
	// rail-only "move").
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("\x1b[1;5C")); err != nil {
		t.Fatalf("write ctrl-right: %v", err)
	}
	coordKeys := mustHotkeyLabels(t, reader.drainText(750*time.Millisecond))
	if !strings.Contains(coordKeys, "coord PTY") {
		t.Fatalf("after Ctrl-Right the hotkeys frame does not reflect COORD focus: %q", coordKeys)
	}
}

// mustHotkeyLabels decodes the LAST hotkeys-type control frame in frames and
// returns its key:label pairs flattened into a single string for substring
// assertions. Fails the test when no hotkeys frame is present.
func mustHotkeyLabels(t *testing.T, frames [][]byte) string {
	t.Helper()
	var last string
	found := false
	for _, f := range frames {
		var env struct {
			Type  string `json:"type"`
			Items []struct {
				Key   string `json:"key"`
				Label string `json:"label"`
				Bar   bool   `json:"bar"`
			} `json:"items"`
		}
		if err := json.Unmarshal(f, &env); err != nil {
			continue
		}
		if env.Type != "hotkeys" {
			continue
		}
		found = true
		var sb strings.Builder
		for _, it := range env.Items {
			sb.WriteString(it.Key)
			sb.WriteString(":")
			sb.WriteString(it.Label)
			sb.WriteString(" ")
		}
		last = sb.String()
	}
	if !found {
		t.Fatalf("no hotkeys control frame found in %d text frame(s)", len(frames))
	}
	return last
}

// TestViewSmoke_EscFromRailReleasesToArgus proves the key-surrender release
// contract end-to-end (D12): Esc while focus is RAIL emits a {"type":"release"}
// TEXT control frame over the view WebSocket and forwards NO byte to any task's
// /input endpoint.
func TestViewSmoke_EscFromRailReleasesToArgus(t *testing.T) {
	d, fake, _ := smokeTestDaemon(t, seedCoordAndWorker(t))
	conn := dialView(t, d)
	defer func() { _ = conn.CloseNow() }()
	reader := newFrameReader(conn)
	defer reader.stop()
	ctx := context.Background()

	_ = reader.drainBinary(300*time.Millisecond, 4096) // initial render
	_ = reader.drainText(300 * time.Millisecond)       // initial hotkeys frame

	before := fake.totalInputs()
	// Esc (0x1b) while focus is RAIL hands the keyboard back to argus.
	if err := conn.Write(ctx, websocket.MessageBinary, []byte{0x1b}); err != nil {
		t.Fatalf("write esc: %v", err)
	}

	frames := reader.drainText(750 * time.Millisecond)
	gotRelease := false
	for _, f := range frames {
		var env struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(f, &env); err == nil && env.Type == "release" {
			gotRelease = true
		}
	}
	if !gotRelease {
		t.Fatalf("Esc in RAIL must emit a {\"type\":\"release\"} frame; got %d text frame(s)", len(frames))
	}
	if got := fake.totalInputs(); got != before {
		t.Fatalf("Esc in RAIL must NOT be forwarded to a PTY; got %d new input(s)", got-before)
	}
}

// TestViewSmoke_RailResumesAfterReturn proves the operator is not stuck after
// stepping into a pane: Ctrl-Q from COORD returns focus to RAIL, and a
// subsequent 'j' navigates the rail rather than being forwarded to a PTY.
func TestViewSmoke_RailResumesAfterReturn(t *testing.T) {
	d, fake, _ := smokeTestDaemon(t, seedCoordAndWorker(t))
	conn := dialView(t, d)
	defer func() { _ = conn.CloseNow() }()
	reader := newFrameReader(conn)
	defer reader.stop()
	ctx := context.Background()

	_ = reader.drainBinary(300*time.Millisecond, 4096) // initial render

	// Step into COORD, then escape back to RAIL with Ctrl-Q (0x11).
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("\x1b[1;5C")); err != nil {
		t.Fatalf("write ctrl-right: %v", err)
	}
	time.Sleep(120 * time.Millisecond)
	if err := conn.Write(ctx, websocket.MessageBinary, []byte{0x11}); err != nil {
		t.Fatalf("write ctrl-q: %v", err)
	}
	time.Sleep(120 * time.Millisecond)

	// In RAIL focus, 'j' moves the cursor and MUST NOT reach any /input.
	before := fake.totalInputs()
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("j")); err != nil {
		t.Fatalf("write j: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if got := fake.totalInputs(); got != before {
		t.Fatalf("'j' after returning to RAIL was forwarded to a PTY (%d new input(s)) "+
			"— focus did not return to RAIL", got-before)
	}
}

// TestViewSmoke_ShiftArrowScrollIntercepted proves the ⇧↑ scroll key (D15) is
// decoded and intercepted end-to-end through the real wsscreen: stepping into a
// pane then sending the Shift-Up CSI sequence (\x1b[1;2A) forwards NO byte to
// any task's /input endpoint (scroll is consumed, never forwarded to the PTY).
func TestViewSmoke_ShiftArrowScrollIntercepted(t *testing.T) {
	d, fake, _ := smokeTestDaemon(t, seedCoordAndWorker(t))
	conn := dialView(t, d)
	defer func() { _ = conn.CloseNow() }()
	reader := newFrameReader(conn)
	defer reader.stop()
	ctx := context.Background()

	_ = reader.drainBinary(300*time.Millisecond, 4096) // initial render

	// Step into COORD so a pane is focused (Shift-Up only scrolls a focused
	// pane; in RAIL it's a no-op).
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("\x1b[1;5C")); err != nil {
		t.Fatalf("write ctrl-right: %v", err)
	}
	time.Sleep(120 * time.Millisecond)

	// Send Shift-Up (CSI 1;2 A). tcell parses this as KeyUp + ModShift, which
	// the KeyRouter intercepts as a scroll — it MUST NOT reach the PTY.
	before := fake.totalInputs()
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("\x1b[1;2A")); err != nil {
		t.Fatalf("write shift-up: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	if got := fake.totalInputs(); got != before {
		t.Fatalf("Shift-Up in a pane must be intercepted (scroll), not forwarded to a PTY; got %d new input(s)", got-before)
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
