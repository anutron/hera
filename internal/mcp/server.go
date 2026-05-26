package mcp

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Server hosts the HTTP listener that argus POSTs MCP tool callbacks into.
// Each registered tool has a handler invoked on POST /mcp/<tool-name>.
type Server struct {
	addr       string
	authHeader string // exact "Authorization: <value>" expected on inbound requests
	log        *slog.Logger

	mu       sync.RWMutex
	handlers map[string]Handler

	// extraMounts holds additional routes mounted by callers via Mount()
	// before Start. The plugin-view WebSocket lives here so it shares the
	// same listener as the MCP tool callbacks.
	extraMounts []extraMount

	httpSrv *http.Server
}

type extraMount struct {
	pattern string
	handler http.Handler
}

// NewServer constructs a server. authHeader is the full header value (e.g.,
// "Bearer abcdef") that incoming requests MUST present.
func NewServer(addr, authHeader string, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		addr:       addr,
		authHeader: authHeader,
		log:        log,
		handlers:   map[string]Handler{},
	}
}

// RegisterHandler binds a tool name to its handler. Replaces any prior
// binding for the same name.
func (s *Server) RegisterHandler(name string, h Handler) {
	s.mu.Lock()
	s.handlers[name] = h
	s.mu.Unlock()
}

// Mount registers an additional HTTP route on the listener. MUST be called
// before Start; routes registered after Start are ignored. Used by the
// daemon to attach the plugin-view WebSocket (`/view`) to the same
// listener as MCP callbacks.
func (s *Server) Mount(pattern string, h http.Handler) {
	s.mu.Lock()
	s.extraMounts = append(s.extraMounts, extraMount{pattern: pattern, handler: h})
	s.mu.Unlock()
}

// Start begins listening on s.addr. Returns once the HTTP server has
// confirmed it can bind. Subsequent shutdown happens via Stop.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp/", s.handleCallback)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	s.mu.RLock()
	for _, m := range s.extraMounts {
		mux.Handle(m.pattern, m.handler)
	}
	s.mu.RUnlock()

	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("mcp.Server: listen: %w", err)
	}
	s.addr = ln.Addr().String() // honor :0 → actual port

	s.httpSrv = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.log.Warn("mcp listener exited with error", "err", err)
		}
	}()
	s.log.Info("mcp server listening", "addr", s.addr)
	return nil
}

// Addr returns the bound listener address (helpful when starting on :0).
func (s *Server) Addr() string { return s.addr }

// CallbackBaseURL returns "http://<addr>" – used when constructing the
// callback_url for tool registrations.
func (s *Server) CallbackBaseURL() string {
	return "http://" + s.addr
}

// Stop gracefully shuts the HTTP server down with a 5s deadline.
func (s *Server) Stop() error {
	if s.httpSrv == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.httpSrv.Shutdown(ctx)
}

// handleCallback dispatches an inbound MCP tool invocation to its handler.
func (s *Server) handleCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Auth: constant-time compare against the configured header.
	if !s.authCheck(r) {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse("hera: invalid auth_header on MCP callback"))
		return
	}

	// Tool name comes from the URL path: /mcp/<name>
	name := strings.TrimPrefix(r.URL.Path, "/mcp/")
	if name == "" || strings.Contains(name, "/") {
		writeJSON(w, http.StatusNotFound, ErrorResponse("hera: unknown tool"))
		return
	}

	s.mu.RLock()
	h, ok := s.handlers[name]
	s.mu.RUnlock()
	if !ok {
		writeJSON(w, http.StatusNotFound, ErrorResponse("hera: unknown tool "+name))
		return
	}

	var env CallbackEnvelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse("hera: invalid envelope: "+err.Error()))
		return
	}
	if env.Tool != "" && env.Tool != name {
		writeJSON(w, http.StatusBadRequest, ErrorResponse("hera: envelope tool mismatch"))
		return
	}

	resp := h.Handle(r.Context(), env.Input)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) authCheck(r *http.Request) bool {
	got := r.Header.Get("Authorization")
	// subtle.ConstantTimeCompare returns 0 immediately if lengths differ –
	// that's a timing side-channel that distinguishes "wrong length" from
	// "wrong content". Not exploitable for us (the auth header is a fixed
	// 32-hex Bearer token), but pad both sides to a fixed length so the
	// compare is always constant-time regardless of input shape.
	const padLen = 256
	gotPad := make([]byte, padLen)
	wantPad := make([]byte, padLen)
	copy(gotPad, got)
	copy(wantPad, s.authHeader)
	// Constant-time content compare + constant-time length compare.
	eqContent := subtle.ConstantTimeCompare(gotPad, wantPad)
	eqLen := subtle.ConstantTimeEq(int32(len(got)), int32(len(s.authHeader)))
	return eqContent&eqLen == 1
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// GenerateAuthHeader produces a random "Bearer <hex>" header value for
// use as the shared secret between argus and hera.
func GenerateAuthHeader() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "Bearer " + hex.EncodeToString(buf), nil
}
