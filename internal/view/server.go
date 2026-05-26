// Package view hosts the hera-view plugin-view WebSocket surface.
//
// Stage E (this file) mounts a single HTTP route — GET /view — on the
// daemon's existing :7744 listener and shepherds each accepted upgrade
// into a per-connection rendering session. The session itself is the
// caller's responsibility (injected as a SessionFunc); Stage J wires the
// real one (Stage-D wsscreen + Stage-F tview.Application). Tests inject a
// noop runner so the lifecycle (upgrade, last-writer-wins, teardown) is
// verifiable without the TUI stack.
package view

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/coder/websocket"
)

// SessionFunc is the per-connection rendering entry point. It receives a
// context that is cancelled when the connection is superseded (last-
// writer-wins) or when Stop is called, and the live WebSocket conn that
// the session reads keystrokes/control envelopes from and writes ANSI
// frames to. The function MUST return when ctx is Done (or when the conn
// has been closed by the remote).
type SessionFunc func(ctx context.Context, conn *websocket.Conn)

// Server owns the /view WebSocket route and the single-active-session
// reference. New upgrades supersede the prior session (last-writer-wins).
//
// A Server is safe for concurrent use. The zero value is not usable — use
// NewServer.
type Server struct {
	log    *slog.Logger
	runner SessionFunc

	mu     sync.Mutex
	active *session
}

// session is a single live WebSocket rendering session.
type session struct {
	conn   *websocket.Conn
	cancel context.CancelFunc
	done   chan struct{}
}

// NewServer constructs a Server with the given logger and per-connection
// runner. A nil runner is replaced by a default that exits on either ctx
// cancellation or peer close — useful for tests and for daemon wiring
// that hasn't installed the real Stage-D/F session yet.
func NewServer(log *slog.Logger, runner SessionFunc) *Server {
	if log == nil {
		log = slog.Default()
	}
	if runner == nil {
		runner = defaultRunner
	}
	return &Server{log: log, runner: runner}
}

// defaultRunner discards incoming frames and returns on either ctx
// cancellation or peer close. It exists so the route is usable in tests
// (and in the daemon during early bring-up) without the real wsscreen +
// tview.Application stack wired in.
func defaultRunner(ctx context.Context, conn *websocket.Conn) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}()
	select {
	case <-ctx.Done():
	case <-done:
	}
}

// Handler returns the http.Handler for /view. Caller mounts it on the
// existing :7744 ServeMux (see internal/daemon/run.go).
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(s.handleUpgrade)
}

// handleUpgrade accepts a WebSocket upgrade and starts a per-connection
// rendering session, superseding any prior active session.
func (s *Server) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// argus's plugin-view dial omits an Origin header; loopback-only
		// listener so skipping the check is safe.
		InsecureSkipVerify: true,
	})
	if err != nil {
		s.log.Warn("view: websocket accept failed", "err", err)
		return
	}

	sessCtx, cancel := context.WithCancel(context.Background())
	sess := &session{
		conn:   conn,
		cancel: cancel,
		done:   make(chan struct{}),
	}

	// Swap the active reference, then tear down the prior session if any.
	s.mu.Lock()
	prior := s.active
	s.active = sess
	s.mu.Unlock()

	if prior != nil {
		// Cancel the prior ctx so its runner exits, then wait for the
		// session's defer chain (which CloseNow's the conn) to finish.
		// A graceful close-frame handshake would block up to 5s when the
		// peer isn't actively reading; CloseNow tears the underlying
		// conn down immediately, which is what last-writer-wins needs.
		prior.cancel()
		<-prior.done
	}

	go s.runSession(sessCtx, sess)
}

// runSession drives the injected runner against the session conn and
// guarantees teardown when the runner returns or ctx is cancelled.
func (s *Server) runSession(ctx context.Context, sess *session) {
	defer close(sess.done)
	defer sess.cancel()
	defer func() {
		// CloseNow tears down the underlying conn without waiting on the
		// close handshake, which would block when the peer is not
		// actively reading (e.g., during last-writer-wins teardown).
		_ = sess.conn.CloseNow()
	}()
	s.runner(ctx, sess.conn)
}

// Stop closes the active session, if any. Safe to call multiple times.
// Returns once the active session goroutine has fully torn down.
func (s *Server) Stop() {
	s.mu.Lock()
	cur := s.active
	s.active = nil
	s.mu.Unlock()
	if cur == nil {
		return
	}
	cur.cancel()
	<-cur.done
}
