package argus

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// Ralph reviewer flagged: the existing TestClient_StreamEvents only
// checks that `since=` is non-empty. Spec scenario "Restart resumes from
// cursor" specifically requires the URL to include `since=<cursor-value>`.
// Verify by asserting the exact value transmitted.
func TestStreamEvents_SinceCarriesCursorValue(t *testing.T) {
	var gotSince string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSince = r.URL.Query().Get("since")
		w.Header().Set("Content-Type", "text/event-stream")
		// Send one event then EOF so streamOnce returns cleanly. The
		// outer reconnect loop would normally re-dial, but the test
		// cancels ctx after the first delivery.
		b, _ := json.Marshal(Event{ID: 100, Type: "task.renamed"})
		_, _ = io.WriteString(w, "event: task.renamed\ndata: "+string(b)+"\n\n")
	}))
	defer srv.Close()

	c := New(srv.URL, "tok")

	var mu sync.Mutex
	var seen int
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = c.StreamEvents(ctx, 1234, func(ev Event) {
			mu.Lock()
			seen++
			mu.Unlock()
			cancel()
		})
		close(done)
	}()
	<-done

	if gotSince != "1234" {
		t.Fatalf("since= value = %q, want %q", gotSince, "1234")
	}
	mu.Lock()
	defer mu.Unlock()
	if seen == 0 {
		t.Fatalf("no events delivered")
	}
}

func TestStreamEvents_SinceZeroAlwaysEmitted(t *testing.T) {
	// since=0 is the new behavior — always emit the param, including the
	// zero cursor case, so request shape is uniform.
	var sawSince bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("since") {
			sawSince = true
		}
		w.Header().Set("Content-Type", "text/event-stream")
		// Close immediately so the test exits.
	}))
	defer srv.Close()

	c := New(srv.URL, "tok")
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	_ = c.StreamEvents(ctx, 0, func(ev Event) {})

	if !sawSince {
		t.Fatalf("expected since= param to be present even when sinceID=0")
	}
}
