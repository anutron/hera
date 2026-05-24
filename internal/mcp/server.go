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

	httpSrv *http.Server
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

// Start begins listening on s.addr. Returns once the HTTP server has
// confirmed it can bind. Subsequent shutdown happens via Stop.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp/", s.handleCallback)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

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
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.authHeader)) == 1
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
