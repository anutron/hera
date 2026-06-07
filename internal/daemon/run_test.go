package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/rpc"
	"net/rpc/jsonrpc"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/config"
	"github.com/anutron/hera/internal/db"
	"github.com/coder/websocket"
)

// fakeArgusForDaemon stubs every endpoint daemon.Start hits during boot:
//   - GET /api/events/stream (SSE, no events)
//   - POST /api/mcp/tools
//   - DELETE /api/mcp/tools/{name}
//   - GET /api/tasks (resolver fallback)
//   - POST /api/plugins/views, GET, DELETE /api/plugins/views/{id}
//   - GET /api/tasks/{id}/output (snapshot for PTY proxy seeding)
//   - GET /api/tasks/{id}/stream (SSE for PTY proxy seeding)
type fakeArgusForDaemon struct {
	mu                 sync.Mutex
	registered         []string
	unregister         []string
	settingsRegistered []string
	settingsUnregister []string
	streamReqs         int
	streamClose        chan struct{}

	viewRegistered   []argus.PluginView
	viewUnregistered []int64
	nextViewID       int64

	taskOutputReqs []string // argus task ids that were snapshotted
	taskStreamReqs []string // argus task ids that opened streams
	taskStreamHold chan struct{}

	tasksListReqs int    // incremented on every GET /api/tasks
	tasksListFail bool   // when true, /api/tasks returns 500
	tasksListBody string // when non-empty, returned verbatim instead of {"tasks":[]}
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
		f.mu.Lock()
		f.tasksListReqs++
		fail := f.tasksListFail
		body := f.tasksListBody
		f.mu.Unlock()
		if fail {
			http.Error(w, `{"error":"simulated failure"}`, http.StatusInternalServerError)
			return
		}
		if body != "" {
			_, _ = io.WriteString(w, body)
			return
		}
		_, _ = io.WriteString(w, `{"tasks":[]}`)
	})
	// Plugin-view registry: hera registers + unregisters its plugin view.
	mux.HandleFunc("/api/plugins/views", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var body argus.PluginView
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.nextViewID++
			body.ID = f.nextViewID
			body.Scope = "hera"
			f.viewRegistered = append(f.viewRegistered, body)
			id := body.ID
			title := body.Title
			cb := body.CallbackURL
			hk := body.Hotkey
			f.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w,
				`{"id":`+strconv.FormatInt(id, 10)+
					`,"scope":"hera","title":`+strconv.Quote(title)+
					`,"hotkey":`+strconv.Quote(hk)+
					`,"callback_url":`+strconv.Quote(cb)+`}`)
		case http.MethodGet:
			f.mu.Lock()
			views := append([]argus.PluginView(nil), f.viewRegistered...)
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
		f.viewUnregistered = append(f.viewUnregistered, id)
		// Remove from the registered list so HeartbeatView lookups reflect deletion.
		next := f.viewRegistered[:0]
		for _, v := range f.viewRegistered {
			if v.ID != id {
				next = append(next, v)
			}
		}
		f.viewRegistered = next
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	// PTY snapshot + SSE for the proxy seeding loop.
	mux.HandleFunc("/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
		switch {
		case strings.HasSuffix(path, "/output"):
			taskID := strings.TrimSuffix(path, "/output")
			f.mu.Lock()
			f.taskOutputReqs = append(f.taskOutputReqs, taskID)
			f.mu.Unlock()
			w.Header().Set("X-Output-Total", "0")
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(path, "/stream"):
			taskID := strings.TrimSuffix(path, "/stream")
			f.mu.Lock()
			f.taskStreamReqs = append(f.taskStreamReqs, taskID)
			hold := f.taskStreamHold
			f.mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			// Keep stream open until the test signals close or the request ctx ends.
			if hold != nil {
				select {
				case <-hold:
				case <-r.Context().Done():
				}
			} else {
				<-r.Context().Done()
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	return mux
}

func TestDaemonStart_RegistersAllSevenToolsAndCleansUp(t *testing.T) {
	fake := &fakeArgusForDaemon{streamClose: make(chan struct{})}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	defer close(fake.streamClose) // drain SSE handler so HTTP server can shut down

	apiPort := extractPort(t, srv.URL)
	sockSvc := &FakeArgusSocketRPC{apiPort: apiPort}
	sockPath, stopSock := startFakeArgusSocket(t, sockSvc)
	defer stopSock()

	// Build a temp config (state dir + listen on :0).
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
		MCPHeartbeat:    24 * time.Hour, // skip heartbeat noise during this test
		ArgusSocketPath: sockPath,
		ArgusPIDPath:    pidPath,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	d, err := Start(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(context.Background())

	// Wait until all seven tools have registered.
	want := []string{"hera_new_orchestrator", "hera_join", "hera_send", "hera_inbox", "hera_mark_read", "hera_status", "hera_spawn_worker"}
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

	apiPort := extractPort(t, srv.URL)
	sockSvc := &FakeArgusSocketRPC{apiPort: apiPort}
	sockPath, stopSock := startFakeArgusSocket(t, sockSvc)
	defer stopSock()

	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "api-token"), []byte("test-token\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	pidPath := filepath.Join(stateDir, "fake-argus.pid")
	if err := os.WriteFile(pidPath, []byte("1\n"), 0o644); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	// Pre-populate the config table with persisted values that diverge
	// from Default(): debounce 5s (vs default 2s), auto-inject off (vs
	// default true).
	cfg := &config.Config{
		StateDir:          stateDir,
		ArgusBaseURL:      srv.URL,
		ListenAddr:        "127.0.0.1:0",
		IdleDebounce:      2 * time.Second,
		MCPHeartbeat:      24 * time.Hour,
		AutoInjectEnabled: true, // Default — LoadPersistedSettings should flip this to false.
		ArgusSocketPath:   sockPath,
		ArgusPIDPath:      pidPath,
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

// FakeArgusSocketRPC is the JSON-RPC service the daemon smoke test
// exports as `Daemon` over a unix socket. It mirrors argus's Daemon.Ports
// and Daemon.Ping methods so hera's startup discovery and runtime watcher
// have something to talk to.
type FakeArgusSocketRPC struct {
	mu      sync.Mutex
	apiPort int
	mcpPort int
}

type FakeSocketEmpty struct{}

type FakeSocketPortsResp struct {
	MCPPort int
	APIPort int
}

type FakeSocketPongResp struct {
	OK bool
}

func (f *FakeArgusSocketRPC) Ports(_ *FakeSocketEmpty, resp *FakeSocketPortsResp) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	resp.APIPort = f.apiPort
	resp.MCPPort = f.mcpPort
	return nil
}

func (f *FakeArgusSocketRPC) Ping(_ *FakeSocketEmpty, resp *FakeSocketPongResp) error {
	resp.OK = true
	return nil
}

// startFakeArgusSocket binds a unix socket that mimics argus's daemon
// JSON-RPC service. The first byte on every connection MUST be 'R' to
// match argus's dispatch convention; the rest is jsonrpc framing.
//
// The socket path lives under /tmp/hera-daemon-* (not t.TempDir) because
// macOS caps sun_path at 104 chars and /var/folders/... can overflow.
func startFakeArgusSocket(t *testing.T, svc *FakeArgusSocketRPC) (sockPath string, stop func()) {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", "hera-daemon-")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sockPath = filepath.Join(dir, "s")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}

	srv := rpc.NewServer()
	if err := srv.RegisterName("Daemon", svc); err != nil {
		_ = ln.Close()
		t.Fatalf("register: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func(c net.Conn) {
				defer wg.Done()
				defer func() { _ = c.Close() }()
				var prefix [1]byte
				if _, err := c.Read(prefix[:]); err != nil {
					return
				}
				if prefix[0] != 'R' {
					return
				}
				srv.ServeCodec(jsonrpc.NewServerCodec(c))
			}(conn)
		}
	}()

	stop = func() {
		_ = ln.Close()
		wg.Wait()
	}
	return sockPath, stop
}

// extractPort parses the port off an httptest server's URL.
func extractPort(t *testing.T, raw string) int {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url %q: %v", raw, err)
	}
	p, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("atoi port %q: %v", u.Port(), err)
	}
	return p
}

// TestDaemonStart_PortDiscoveryRunsBeforeMCPRegistrar asserts the Stage 7.2
// contract: startup must query the argus socket for the REST port BEFORE
// constructing argus.Client and starting the MCP registrar. The fake socket
// returns the httptest server's port; if discovery is skipped or wired
// after the registrar, the registrar's POSTs land on the wrong URL and the
// fake registry sees zero registrations.
func TestDaemonStart_PortDiscoveryRunsBeforeMCPRegistrar(t *testing.T) {
	fake := &fakeArgusForDaemon{streamClose: make(chan struct{})}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	defer close(fake.streamClose)

	apiPort := extractPort(t, srv.URL)
	sockSvc := &FakeArgusSocketRPC{apiPort: apiPort}
	sockPath, stopSock := startFakeArgusSocket(t, sockSvc)
	defer stopSock()

	stateDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(stateDir, "api-token"), []byte("test-token\n"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	pidPath := filepath.Join(stateDir, "fake-argus.pid")
	if err := os.WriteFile(pidPath, []byte("1\n"), 0o644); err != nil {
		t.Fatalf("write pid: %v", err)
	}

	// ArgusBaseURL intentionally points at a closed port. If startup
	// discovery does NOT overwrite it via SetBaseURL, the MCP registrar's
	// POSTs fail and fake.registered stays empty.
	cfg := &config.Config{
		StateDir:        stateDir,
		ArgusBaseURL:    "http://127.0.0.1:1",
		ListenAddr:      "127.0.0.1:0",
		IdleDebounce:    100 * time.Millisecond,
		MCPHeartbeat:    24 * time.Hour,
		ArgusSocketPath: sockPath,
		ArgusPIDPath:    pidPath,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	d, err := Start(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(context.Background())

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
		t.Fatalf("registered %d tools, want %d — startup discovery did not redirect the client to the discovered port: %+v", len(got), len(want), got)
	}

	// The discovered URL should now be live on the client.
	gotURL := d.Argus.BaseURL()
	wantURL := "http://127.0.0.1:" + strconv.Itoa(apiPort)
	if gotURL != wantURL {
		t.Fatalf("argus client baseURL = %q, want %q", gotURL, wantURL)
	}
}

// TestDaemonStart_PortDiscoveryFailureExitsNonZero asserts the Stage 7.2
// hard-exit contract: when the argus socket is unreachable, Start MUST
// return an error AND no MCP registrations may have been attempted (the
// registrar must not start before discovery succeeds).
func TestDaemonStart_PortDiscoveryFailureExitsNonZero(t *testing.T) {
	fake := &fakeArgusForDaemon{streamClose: make(chan struct{})}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	defer close(fake.streamClose)

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
		ArgusSocketPath: filepath.Join(stateDir, "nope.sock"), // does not exist
		ArgusPIDPath:    pidPath,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	d, err := Start(ctx, cfg, nil)
	if err == nil {
		if d != nil {
			d.Stop(context.Background())
		}
		t.Fatalf("Start: expected error from socket discovery, got nil")
	}
	if !strings.Contains(err.Error(), "Ports") && !strings.Contains(err.Error(), "ports") && !strings.Contains(err.Error(), "socket") {
		t.Fatalf("error should mention Ports/socket discovery, got %q", err.Error())
	}

	// Registrar must not have started — no MCP POSTs should have hit argus.
	time.Sleep(50 * time.Millisecond)
	fake.mu.Lock()
	gotReg := len(fake.registered)
	fake.mu.Unlock()
	if gotReg != 0 {
		t.Fatalf("registrar started despite discovery failure: %d POSTs landed", gotReg)
	}
}

// TestDaemonStart_RegistersPluginViewAndUnregisters pins Stage J's
// plugin-view registrar wireup: Start posts to /api/plugins/views and Stop
// issues a DELETE against the returned id.
func TestDaemonStart_RegistersPluginViewAndUnregisters(t *testing.T) {
	fake := &fakeArgusForDaemon{streamClose: make(chan struct{})}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	defer close(fake.streamClose)

	apiPort := extractPort(t, srv.URL)
	sockSvc := &FakeArgusSocketRPC{apiPort: apiPort}
	sockPath, stopSock := startFakeArgusSocket(t, sockSvc)
	defer stopSock()

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
		MCPHeartbeat:    24 * time.Hour, // skip heartbeat noise
		ArgusSocketPath: sockPath,
		ArgusPIDPath:    pidPath,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	d, err := Start(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(context.Background())

	// Plugin view registered.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fake.mu.Lock()
		n := len(fake.viewRegistered)
		fake.mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	fake.mu.Lock()
	gotReg := append([]argus.PluginView(nil), fake.viewRegistered...)
	fake.mu.Unlock()
	if len(gotReg) < 1 {
		t.Fatalf("plugin view not registered, got %+v", gotReg)
	}
	got := gotReg[0]
	if got.Title != "Hera" {
		t.Errorf("plugin view title = %q, want %q (BUG-034: empty title → argus HTTP 400 crash-loop)", got.Title, "Hera")
	}
	if !strings.HasSuffix(got.CallbackURL, "/view") {
		t.Errorf("plugin view callback_url = %q, want suffix /view", got.CallbackURL)
	}
	if !strings.HasPrefix(got.CallbackURL, "ws://") {
		t.Errorf("plugin view callback_url = %q, want ws:// scheme", got.CallbackURL)
	}

	// Shut down and verify DELETE landed.
	d.Stop(context.Background())
	fake.mu.Lock()
	gotUnreg := append([]int64(nil), fake.viewUnregistered...)
	fake.mu.Unlock()
	if len(gotUnreg) < 1 {
		t.Fatalf("plugin view not unregistered on Stop, got %+v", gotUnreg)
	}
	if gotUnreg[0] != got.ID {
		t.Errorf("unregistered id = %d, want %d", gotUnreg[0], got.ID)
	}
}

// TestDaemonStart_SeedsProxyForLiveBindings pins Stage J's PTY proxy
// seeding wireup: every live binding present in the DB at startup should
// trigger a snapshot fetch and a stream subscription against argus.
func TestDaemonStart_SeedsProxyForLiveBindings(t *testing.T) {
	fake := &fakeArgusForDaemon{
		streamClose:    make(chan struct{}),
		taskStreamHold: make(chan struct{}),
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	defer close(fake.streamClose)

	apiPort := extractPort(t, srv.URL)
	sockSvc := &FakeArgusSocketRPC{apiPort: apiPort}
	sockPath, stopSock := startFakeArgusSocket(t, sockSvc)
	defer stopSock()

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

	// Pre-seed two live bindings under one orchestrator + role.
	database, err := db.Open(cfg.StatePath())
	if err != nil {
		t.Fatalf("pre-seed open: %v", err)
	}
	ctx0 := context.Background()
	orch, err := database.Orchestrators.Create(ctx0, "demo")
	if err != nil {
		t.Fatalf("create orch: %v", err)
	}
	r1, err := database.Roles.Create(ctx0, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "demo",
	})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}
	r2, err := database.Roles.Create(ctx0, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "demo",
	})
	if err != nil {
		t.Fatalf("create role w1: %v", err)
	}
	if _, err := database.Bindings.Create(ctx0, db.CreateBindingInput{
		RoleID: r1.ID, ArgusTaskID: "task-A", WorktreePath: "/tmp/a",
	}); err != nil {
		t.Fatalf("create binding A: %v", err)
	}
	if _, err := database.Bindings.Create(ctx0, db.CreateBindingInput{
		RoleID: r2.ID, ArgusTaskID: "task-B", WorktreePath: "/tmp/b",
	}); err != nil {
		t.Fatalf("create binding B: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("pre-seed close: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	d, err := Start(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(context.Background())

	// Wait until both bindings have been snapshotted + streamed.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		fake.mu.Lock()
		nOut := len(fake.taskOutputReqs)
		nStream := len(fake.taskStreamReqs)
		fake.mu.Unlock()
		if nOut >= 2 && nStream >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	fake.mu.Lock()
	outs := append([]string(nil), fake.taskOutputReqs...)
	streams := append([]string(nil), fake.taskStreamReqs...)
	fake.mu.Unlock()

	seenOut := map[string]bool{}
	for _, id := range outs {
		seenOut[id] = true
	}
	seenStream := map[string]bool{}
	for _, id := range streams {
		seenStream[id] = true
	}
	for _, want := range []string{"task-A", "task-B"} {
		if !seenOut[want] {
			t.Errorf("no snapshot fetch for %q (got %+v)", want, outs)
		}
		if !seenStream[want] {
			t.Errorf("no stream subscribe for %q (got %+v)", want, streams)
		}
	}

	// Release the stream-hold so the proxy goroutines can exit on Stop.
	close(fake.taskStreamHold)

	// Stop should tear down proxy subscriptions cleanly (no goroutine leak).
	d.Stop(context.Background())
}

// TestDaemonStart_EagerlySeesFreelancers verifies that tasks in argus's task
// list that have no hera binding (freelancers) are eagerly seeded into the PTY
// proxy as soon as the ArgusStateCache first polls. Without this, scrollback
// for a freelancer is limited to argus's 256 KiB snapshot at first-open rather
// than accumulating from daemon startup.
func TestDaemonStart_EagerlySeesFreelancers(t *testing.T) {
	const freelanceID = "freelance-X"
	fake := &fakeArgusForDaemon{
		streamClose:    make(chan struct{}),
		taskStreamHold: make(chan struct{}),
		// Return a single task from the /api/tasks listing. It has no hera
		// binding in the DB, so it will NOT be seeded by the Seed(taskIDs)
		// call at startup — only the eager-seeder goroutine should pick it up.
		tasksListBody: `{"tasks":[{"id":"` + freelanceID + `","name":"freelancer","project":"demo","status":"in_progress"}]}`,
	}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	defer close(fake.streamClose)

	apiPort := extractPort(t, srv.URL)
	sockSvc := &FakeArgusSocketRPC{apiPort: apiPort}
	sockPath, stopSock := startFakeArgusSocket(t, sockSvc)
	defer stopSock()

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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	d, err := Start(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(context.Background())

	// Wait until the freelancer has been snapshotted AND streamed — purely from
	// the eager-seeder, with no user navigating to the pane.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		fake.mu.Lock()
		outOK := false
		for _, id := range fake.taskOutputReqs {
			if id == freelanceID {
				outOK = true
				break
			}
		}
		streamOK := false
		for _, id := range fake.taskStreamReqs {
			if id == freelanceID {
				streamOK = true
				break
			}
		}
		fake.mu.Unlock()
		if outOK && streamOK {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	fake.mu.Lock()
	outs := append([]string(nil), fake.taskOutputReqs...)
	streams := append([]string(nil), fake.taskStreamReqs...)
	fake.mu.Unlock()

	seenOut := map[string]bool{}
	for _, id := range outs {
		seenOut[id] = true
	}
	seenStream := map[string]bool{}
	for _, id := range streams {
		seenStream[id] = true
	}
	if !seenOut[freelanceID] {
		t.Errorf("no snapshot fetch for freelancer %q — eager-seeder not wired (got %+v)", freelanceID, outs)
	}
	if !seenStream[freelanceID] {
		t.Errorf("no stream subscribe for freelancer %q — eager-seeder not wired (got %+v)", freelanceID, streams)
	}

	close(fake.taskStreamHold)
	d.Stop(context.Background())
}

// TestDaemonStart_MountsViewRouteOnMCPListener pins Stage J's /view route
// mounting on the same listener as /mcp/. A WebSocket upgrade against
// ws://<mcp-addr>/view MUST succeed (Stage E's handler accepts).
func TestDaemonStart_MountsViewRouteOnMCPListener(t *testing.T) {
	fake := &fakeArgusForDaemon{streamClose: make(chan struct{})}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	defer close(fake.streamClose)

	apiPort := extractPort(t, srv.URL)
	sockSvc := &FakeArgusSocketRPC{apiPort: apiPort}
	sockPath, stopSock := startFakeArgusSocket(t, sockSvc)
	defer stopSock()

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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	d, err := Start(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(context.Background())

	wsURL := "ws://" + d.MCPServer.Addr() + "/view"
	dialCtx, dialCancel := context.WithTimeout(ctx, 2*time.Second)
	defer dialCancel()
	conn, _, err := websocket.Dial(dialCtx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial /view: %v", err)
	}
	_ = conn.CloseNow()
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

// TestDaemonStart_BootReconcileCallsListTasks asserts that Start() calls
// GET /api/tasks synchronously (the boot reconcile) before returning.
func TestDaemonStart_BootReconcileCallsListTasks(t *testing.T) {
	fake := &fakeArgusForDaemon{streamClose: make(chan struct{})}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	defer close(fake.streamClose)

	apiPort := extractPort(t, srv.URL)
	sockSvc := &FakeArgusSocketRPC{apiPort: apiPort}
	sockPath, stopSock := startFakeArgusSocket(t, sockSvc)
	defer stopSock()

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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	d, err := Start(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer d.Stop(context.Background())

	// Boot reconcile is synchronous: by the time Start() returns, at least
	// one GET /api/tasks must have been issued.
	fake.mu.Lock()
	n := fake.tasksListReqs
	fake.mu.Unlock()
	if n < 1 {
		t.Fatalf("expected at least 1 GET /api/tasks (boot reconcile), got %d", n)
	}
}

// TestDaemonStart_BootReconcileFailure_DaemonStillStarts asserts that a
// non-200 response from GET /api/tasks at boot does not prevent startup.
func TestDaemonStart_BootReconcileFailure_DaemonStillStarts(t *testing.T) {
	fake := &fakeArgusForDaemon{streamClose: make(chan struct{}), tasksListFail: true}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	defer close(fake.streamClose)

	apiPort := extractPort(t, srv.URL)
	sockSvc := &FakeArgusSocketRPC{apiPort: apiPort}
	sockPath, stopSock := startFakeArgusSocket(t, sockSvc)
	defer stopSock()

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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	d, err := Start(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("Start must succeed even when boot reconcile fails; got: %v", err)
	}
	d.Stop(context.Background())
}
