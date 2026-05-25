package argus

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type stubReregistrar struct {
	calls atomic.Int32
	err   error
}

func (s *stubReregistrar) ForceReregister(_ context.Context) error {
	s.calls.Add(1)
	return s.err
}

func TestRecover_HappyPath_SetsBaseURLAndMarksHealthy(t *testing.T) {
	svc := &FakeDaemonRPC{api: 7752, mcp: 7751}
	sock, stop := startFakeDaemonSocket(t, svc)
	defer stop()

	client := New("http://127.0.0.1:1", "tok")
	ports := NewPortsClient(sock)
	mcpReg := &stubReregistrar{}
	setReg := &stubReregistrar{}

	SetLinkState(LinkHealthy)
	SetLinkError(nil)
	t.Cleanup(func() {
		SetLinkState(LinkHealthy)
		SetLinkError(nil)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	Recover(ctx, ports, client, mcpReg, setReg, nil)

	if got := client.BaseURL(); got != "http://127.0.0.1:7752" {
		t.Fatalf("BaseURL = %q, want http://127.0.0.1:7752", got)
	}
	if mcpReg.calls.Load() != 1 {
		t.Fatalf("mcp ForceReregister calls = %d, want 1", mcpReg.calls.Load())
	}
	if setReg.calls.Load() != 1 {
		t.Fatalf("settings ForceReregister calls = %d, want 1", setReg.calls.Load())
	}
	if got := GetLinkState(); got != LinkHealthy {
		t.Fatalf("link state = %v, want healthy", got)
	}
	if err := LinkLastError(); err != nil {
		t.Fatalf("LastError = %v, want nil", err)
	}
}

func TestRecover_PortsFailure_LinkDownWithWrappedError(t *testing.T) {
	client := New("http://127.0.0.1:1", "tok")
	ports := NewPortsClient("/nonexistent/argus.sock")
	mcpReg := &stubReregistrar{}
	setReg := &stubReregistrar{}

	SetLinkState(LinkHealthy)
	SetLinkError(nil)
	t.Cleanup(func() {
		SetLinkState(LinkHealthy)
		SetLinkError(nil)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	Recover(ctx, ports, client, mcpReg, setReg, nil)

	if got := GetLinkState(); got != LinkDown {
		t.Fatalf("link state = %v, want down", got)
	}
	if err := LinkLastError(); err == nil {
		t.Fatal("LastError = nil, want wrapped socket error")
	}
	// Re-registrars must NOT have been invoked after port discovery failed.
	if mcpReg.calls.Load() != 0 || setReg.calls.Load() != 0 {
		t.Fatalf("re-registrars called despite ports failure: mcp=%d settings=%d",
			mcpReg.calls.Load(), setReg.calls.Load())
	}
}

func TestRecover_MCPFailure_LinkDownAndSettingsSkipped(t *testing.T) {
	svc := &FakeDaemonRPC{api: 7752, mcp: 7751}
	sock, stop := startFakeDaemonSocket(t, svc)
	defer stop()

	client := New("http://127.0.0.1:1", "tok")
	ports := NewPortsClient(sock)
	mcpReg := &stubReregistrar{err: errors.New("boom")}
	setReg := &stubReregistrar{}

	SetLinkState(LinkHealthy)
	SetLinkError(nil)
	t.Cleanup(func() {
		SetLinkState(LinkHealthy)
		SetLinkError(nil)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	Recover(ctx, ports, client, mcpReg, setReg, nil)

	if got := client.BaseURL(); got != "http://127.0.0.1:7752" {
		t.Fatalf("BaseURL should still update before mcp reregister: %q", got)
	}
	if got := GetLinkState(); got != LinkDown {
		t.Fatalf("link state = %v, want down", got)
	}
	if setReg.calls.Load() != 0 {
		t.Fatalf("settings re-register should not run after mcp failed, got %d", setReg.calls.Load())
	}
}

func TestRecover_OrderingPortsBeforeRegistrars(t *testing.T) {
	svc := &FakeDaemonRPC{api: 7799, mcp: 7798}
	sock, stop := startFakeDaemonSocket(t, svc)
	defer stop()

	client := New("http://127.0.0.1:1", "tok")
	ports := NewPortsClient(sock)

	// Order check: when ForceReregister fires on the mcp side, the client
	// baseURL MUST already reflect the discovered port. The stub captures
	// the URL at call time and asserts it has been swapped.
	var observed string
	mcpReg := &orderingReregistrar{client: client, recordTo: &observed}
	setReg := &stubReregistrar{}

	SetLinkState(LinkHealthy)
	SetLinkError(nil)
	t.Cleanup(func() {
		SetLinkState(LinkHealthy)
		SetLinkError(nil)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	Recover(ctx, ports, client, mcpReg, setReg, nil)

	if observed != "http://127.0.0.1:7799" {
		t.Fatalf("at mcp re-register time client baseURL = %q, want discovered URL", observed)
	}
}

type orderingReregistrar struct {
	client   *Client
	recordTo *string
}

func (o *orderingReregistrar) ForceReregister(_ context.Context) error {
	*o.recordTo = o.client.BaseURL()
	return nil
}
