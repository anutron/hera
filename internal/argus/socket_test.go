package argus

import (
	"context"
	"errors"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// FakeDaemonRPC mirrors the subset of argus's daemon RPC surface that hera
// consumes. Wire-format fidelity matters: argus's PortsResp has no JSON
// tags, so the JSON-RPC encoder emits PascalCase field names (MCPPort,
// APIPort) and our PortsClient must decode them the same way.
//
// The type and its methods are exported so net/rpc's reflective registration
// (which requires exported types AND exported method args) accepts them.
type FakeDaemonRPC struct {
	mu      sync.Mutex
	api     int
	mcp     int
	pingErr error
}

// FakeEmpty is the no-args request placeholder mirroring argus's
// daemon.Empty.
type FakeEmpty struct{}

// FakePortsResp mirrors argus's daemon.PortsResp wire shape.
type FakePortsResp struct {
	MCPPort int
	APIPort int
}

// FakePongResp mirrors argus's daemon.PongResp wire shape.
type FakePongResp struct {
	OK bool
}

func (f *FakeDaemonRPC) Ports(_ *FakeEmpty, resp *FakePortsResp) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	resp.MCPPort = f.mcp
	resp.APIPort = f.api
	return nil
}

func (f *FakeDaemonRPC) Ping(_ *FakeEmpty, resp *FakePongResp) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pingErr != nil {
		return f.pingErr
	}
	resp.OK = true
	return nil
}

// startFakeDaemonSocket binds a unix socket and serves JSON-RPC
// connections following argus's dispatch-byte convention: the first byte
// on the wire is 'R' for an RPC connection, then jsonrpc serves on the
// rest of the stream.
//
// The socket lives under /tmp/heraNNN (not t.TempDir) because macOS caps
// sun_path at 104 chars and /var/folders/... paths overflow.
func startFakeDaemonSocket(t *testing.T, svc *FakeDaemonRPC) (sockPath string, stop func()) {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", "hera-sock-")
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
		ln.Close()
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
				defer c.Close()
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
		ln.Close()
		wg.Wait()
	}
	return sockPath, stop
}

func TestPortsClient_Ports_DecodesPascalCaseWireShape(t *testing.T) {
	svc := &FakeDaemonRPC{api: 7745, mcp: 7743}
	sock, stop := startFakeDaemonSocket(t, svc)
	defer stop()

	client := NewPortsClient(sock)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	api, mcp, err := client.Ports(ctx)
	if err != nil {
		t.Fatalf("Ports: %v", err)
	}
	if api != 7745 {
		t.Fatalf("api = %d, want 7745", api)
	}
	if mcp != 7743 {
		t.Fatalf("mcp = %d, want 7743", mcp)
	}
}

func TestPortsClient_Ports_DialError(t *testing.T) {
	client := NewPortsClient("/this/path/should/not/exist/daemon.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if _, _, err := client.Ports(ctx); err == nil {
		t.Fatal("Ports: expected dial error, got nil")
	}
}

func TestPortsClient_Ping_OK(t *testing.T) {
	svc := &FakeDaemonRPC{}
	sock, stop := startFakeDaemonSocket(t, svc)
	defer stop()

	client := NewPortsClient(sock)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestPortsClient_Ping_ServerError(t *testing.T) {
	svc := &FakeDaemonRPC{pingErr: errors.New("daemon wedged")}
	sock, stop := startFakeDaemonSocket(t, svc)
	defer stop()

	client := NewPortsClient(sock)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx); err == nil {
		t.Fatal("Ping: expected server-side error, got nil")
	}
}

func TestPortsClient_Ping_NoSocket(t *testing.T) {
	client := NewPortsClient("/this/path/should/not/exist/daemon.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx); err == nil {
		t.Fatal("Ping: expected dial error when socket is missing, got nil")
	}
}
