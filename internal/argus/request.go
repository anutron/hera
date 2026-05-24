package argus

import (
	"bytes"
	"context"
	"net/http"
)

// newRequest is a small helper that wraps http.NewRequestWithContext for
// callers that supply raw bytes (vs JSON).
func newRequest(ctx context.Context, method, url string, body []byte) (*http.Request, error) {
	var r *bytes.Reader
	if body != nil {
		r = bytes.NewReader(body)
		return http.NewRequestWithContext(ctx, method, url, r)
	}
	return http.NewRequestWithContext(ctx, method, url, nil)
}
