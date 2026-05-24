package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// helper: start a server on :0 and return its base URL + cleanup func.
func startTestServer(t *testing.T, auth string) (*Server, string) {
	t.Helper()
	s := NewServer("127.0.0.1:0", auth, nil)
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Stop() })
	return s, "http://" + s.Addr()
}

func TestServer_HealthCheckRespondsOK(t *testing.T) {
	_, base := startTestServer(t, "Bearer secret")
	resp, err := http.Get(base + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestServer_CallbackInvokesHandler(t *testing.T) {
	s, base := startTestServer(t, "Bearer s3cret")
	s.RegisterHandler("ludwig_echo", HandlerFunc(func(ctx context.Context, input json.RawMessage) Response {
		return TextResponse("echo: " + string(input))
	}))

	body := `{"tool":"ludwig_echo","input":{"hello":true},"context":{}}`
	req, _ := http.NewRequest("POST", base+"/mcp/ludwig_echo", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer s3cret")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out Response
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.IsError {
		t.Fatalf("response IsError true: %+v", out)
	}
	if len(out.Content) != 1 || out.Content[0].Text != `echo: {"hello":true}` {
		t.Fatalf("content = %+v", out.Content)
	}
}

func TestServer_RejectsWrongAuth(t *testing.T) {
	_, base := startTestServer(t, "Bearer s3cret")
	body := `{"tool":"ludwig_x","input":{}}`
	req, _ := http.NewRequest("POST", base+"/mcp/ludwig_x", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer wrong")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestServer_ReturnsNotFoundForUnknownTool(t *testing.T) {
	_, base := startTestServer(t, "Bearer s3cret")
	body := `{"tool":"ludwig_x","input":{}}`
	req, _ := http.NewRequest("POST", base+"/mcp/ludwig_x", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer s3cret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	var out Response
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if !out.IsError {
		t.Fatalf("unknown tool should return IsError")
	}
}

func TestServer_RejectsEnvelopeMismatch(t *testing.T) {
	s, base := startTestServer(t, "Bearer s3cret")
	s.RegisterHandler("ludwig_a", HandlerFunc(func(ctx context.Context, input json.RawMessage) Response {
		return TextResponse("ok")
	}))
	body := `{"tool":"ludwig_b","input":{}}` // path says A, envelope says B
	req, _ := http.NewRequest("POST", base+"/mcp/ludwig_a", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer s3cret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestServer_RejectsGET(t *testing.T) {
	_, base := startTestServer(t, "Bearer s3cret")
	resp, err := http.Get(base + "/mcp/ludwig_send")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestGenerateAuthHeader(t *testing.T) {
	a, err := GenerateAuthHeader()
	if err != nil {
		t.Fatalf("GenerateAuthHeader: %v", err)
	}
	b, err := GenerateAuthHeader()
	if err != nil {
		t.Fatalf("GenerateAuthHeader 2: %v", err)
	}
	if a == b {
		t.Fatalf("two generated headers should differ")
	}
	if len(a) < len("Bearer ")+10 {
		t.Fatalf("header too short: %q", a)
	}
}

// silence import.
var _ = io.Discard
