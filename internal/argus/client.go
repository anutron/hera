package argus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// PluginVersion is the contract version hera sends with every request via
// the X-Argus-Plugin-Version header.
const PluginVersion = "1"

// DefaultBaseURL is the standard argus daemon HTTP base.
const DefaultBaseURL = "http://127.0.0.1:7743"

// Client is a typed HTTP client for argus's REST API.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New constructs a Client. baseURL is the daemon's HTTP root (no trailing
// slash). token is the scope token loaded from ~/.hera/api-token.
func New(baseURL, token string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	return &Client{
		baseURL: baseURL,
		token:   token,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// BaseURL returns the configured base URL (for log/diagnostic use).
func (c *Client) BaseURL() string { return c.baseURL }

// doJSON issues an HTTP request with JSON body (optional) and parses a JSON
// response (optional) into out. Returns the HTTP status code and any error.
func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) (int, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("argus: marshal body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return 0, fmt.Errorf("argus: new request: %w", err)
	}
	c.applyAuth(req)
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("argus: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return resp.StatusCode, fmt.Errorf("argus: %s %s: HTTP %d: %s", method, path, resp.StatusCode, bytes.TrimSpace(errBody))
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.StatusCode, fmt.Errorf("argus: decode response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

// applyAuth sets the Authorization and version headers on an outgoing
// request.
func (c *Client) applyAuth(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Argus-Plugin-Version", PluginVersion)
}

// withTokenQuery returns a URL with ?token=<token> appended to support SSE
// callers (some clients can't set headers).
func (c *Client) withTokenQuery(path string) string {
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return c.baseURL + path
	}
	q := u.Query()
	q.Set("token", c.token)
	u.RawQuery = q.Encode()
	return u.String()
}
