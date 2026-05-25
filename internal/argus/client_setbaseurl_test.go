package argus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestClient_SetBaseURL_RacesAreClean exercises baseURL mutation while many
// HTTP-issuing methods read it concurrently. With race detector enabled
// (`go test -race`), the test must not report a data race. Pre-implementation
// (no SetBaseURL, baseURL is plain string field) the test fails to compile;
// add SetBaseURL but skip the mutex and the race detector trips.
func TestClient_SetBaseURL_RacesAreClean(t *testing.T) {
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tasks":[]}`))
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tasks":[]}`))
	}))
	defer srvB.Close()

	c := New(srvA.URL, "tok")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var done atomic.Bool

	// Readers: fire ListTasks repeatedly until the test signals done.
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !done.Load() {
				if _, err := c.ListTasks(ctx); err != nil && ctx.Err() == nil {
					t.Errorf("ListTasks: %v", err)
					return
				}
			}
		}()
	}

	// Writers: flip baseURL between the two servers repeatedly.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			urls := []string{srvA.URL, srvB.URL}
			for j := 0; j < 200; j++ {
				c.SetBaseURL(urls[j%2])
			}
		}(i)
	}

	// Run for a bounded period and signal readers to exit.
	time.Sleep(200 * time.Millisecond)
	done.Store(true)
	wg.Wait()
}

// TestClient_SetBaseURL_UpdatesRequestTarget verifies that after SetBaseURL,
// subsequent requests hit the new host. This guards against an
// implementation that reads baseURL once at construction and caches it.
func TestClient_SetBaseURL_UpdatesRequestTarget(t *testing.T) {
	var hitA, hitB atomic.Int32
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitA.Add(1)
		_, _ = w.Write([]byte(`{"tasks":[]}`))
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitB.Add(1)
		_, _ = w.Write([]byte(`{"tasks":[]}`))
	}))
	defer srvB.Close()

	c := New(srvA.URL, "tok")
	ctx := context.Background()

	if _, err := c.ListTasks(ctx); err != nil {
		t.Fatalf("ListTasks(A): %v", err)
	}
	c.SetBaseURL(srvB.URL)
	if _, err := c.ListTasks(ctx); err != nil {
		t.Fatalf("ListTasks(B): %v", err)
	}

	if hitA.Load() != 1 {
		t.Fatalf("srvA hits = %d, want 1", hitA.Load())
	}
	if hitB.Load() != 1 {
		t.Fatalf("srvB hits = %d, want 1", hitB.Load())
	}

	// BaseURL accessor SHALL reflect the new value.
	if got := c.BaseURL(); got != srvB.URL {
		t.Fatalf("BaseURL() = %q, want %q", got, srvB.URL)
	}
}

// TestClient_SetBaseURL_TrimsTrailingSlash mirrors the constructor's
// normalization so callers don't accidentally produce "http://host//api/...".
func TestClient_SetBaseURL_TrimsTrailingSlash(t *testing.T) {
	c := New("http://example.test", "tok")
	c.SetBaseURL("http://example.test:7745/")
	if got := c.BaseURL(); got != "http://example.test:7745" {
		t.Fatalf("BaseURL() = %q, want trailing slash trimmed", got)
	}
}
