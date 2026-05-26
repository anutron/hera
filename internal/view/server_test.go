package view

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// newTestHTTPServer wires the given Server's /view handler onto an
// httptest.Server and returns it plus the ws:// URL for /view.
func newTestHTTPServer(t *testing.T, srv *Server) (*httptest.Server, string) {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/view", srv.Handler())
	httpsrv := httptest.NewServer(mux)
	t.Cleanup(httpsrv.Close)
	wsURL := "ws" + strings.TrimPrefix(httpsrv.URL, "http") + "/view"
	return httpsrv, wsURL
}

// TestServer_UpgradeAccepted asserts the /view route completes a WebSocket
// upgrade for a valid request.
func TestServer_UpgradeAccepted(t *testing.T) {
	srv := NewServer(nil, nil)
	t.Cleanup(srv.Stop)

	_, wsURL := newTestHTTPServer(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })
}

// TestServer_SecondConnectionClosesFirst asserts that when a second
// WebSocket upgrade arrives while a prior session is active, the prior
// connection is closed before the new one is served. Asserts last-writer-
// wins per Stage-E spec scenarios.
func TestServer_SecondConnectionClosesFirst(t *testing.T) {
	srv := NewServer(nil, nil)
	t.Cleanup(srv.Stop)

	_, wsURL := newTestHTTPServer(t, srv)

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer dialCancel()

	conn1, _, err := websocket.Dial(dialCtx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	t.Cleanup(func() { _ = conn1.CloseNow() })

	conn2, _, err := websocket.Dial(dialCtx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	t.Cleanup(func() { _ = conn2.CloseNow() })

	// Reading on conn1 must observe a close — the server superseded it.
	readCtx, readCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer readCancel()
	_, _, err = conn1.Read(readCtx)
	if err == nil {
		t.Fatalf("expected close error on conn1, got nil")
	}
	if status := websocket.CloseStatus(err); status == -1 {
		// Some races surface as a plain transport error rather than a clean
		// close frame; either is acceptable so long as the read terminated.
		t.Logf("conn1 read returned non-close error (still acceptable): %v", err)
	}

	// conn2 should still be readable until its own deadline (no traffic,
	// but the read should NOT terminate immediately). Use a short deadline.
	c2Ctx, c2Cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer c2Cancel()
	_, _, err = conn2.Read(c2Ctx)
	if err == nil {
		t.Fatalf("conn2 unexpectedly delivered a message")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		// If conn2 also got closed, that's a bug — last-writer-wins means
		// the latest connection is the survivor.
		if status := websocket.CloseStatus(err); status != -1 {
			t.Fatalf("conn2 was closed by server with status %v: %v", status, err)
		}
	}
}

// TestServer_RunnerInvokedPerConnection verifies the injected SessionFunc
// is invoked exactly once per accepted upgrade, and runs to completion when
// the connection is superseded (last-writer-wins triggers ctx cancel).
func TestServer_RunnerInvokedPerConnection(t *testing.T) {
	var (
		mu          sync.Mutex
		startedCh   = make(chan struct{}, 2)
		finishedCh  = make(chan struct{}, 2)
		invocations int
	)
	runner := func(ctx context.Context, conn *websocket.Conn) {
		mu.Lock()
		invocations++
		mu.Unlock()
		startedCh <- struct{}{}
		<-ctx.Done()
		finishedCh <- struct{}{}
	}
	srv := NewServer(nil, runner)
	t.Cleanup(srv.Stop)

	_, wsURL := newTestHTTPServer(t, srv)

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer dialCancel()

	conn1, _, err := websocket.Dial(dialCtx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	t.Cleanup(func() { _ = conn1.CloseNow() })
	waitChan(t, startedCh, "runner 1 start")

	conn2, _, err := websocket.Dial(dialCtx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	t.Cleanup(func() { _ = conn2.CloseNow() })

	// Superseded runner must finish.
	waitChan(t, finishedCh, "runner 1 finish")
	waitChan(t, startedCh, "runner 2 start")

	mu.Lock()
	got := invocations
	mu.Unlock()
	if got != 2 {
		t.Fatalf("runner invocations: got %d, want 2", got)
	}
}

func waitChan(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for %s", label)
	}
}
