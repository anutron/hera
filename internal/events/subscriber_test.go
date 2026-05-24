package events

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

func TestSubscriber_AdvancesCursor(t *testing.T) {
	// Build a 3-event SSE stream.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify since= is included.
		if r.URL.Query().Get("since") == "" {
			t.Errorf("missing since= param")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for i := int64(1); i <= 3; i++ {
			b, _ := json.Marshal(argus.Event{ID: i, Type: "task.renamed", TaskID: "t"})
			_, _ = io.WriteString(w, "event: task.renamed\ndata: "+string(b)+"\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()

	dbPath := filepath.Join(t.TempDir(), "events.sqlite")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()

	// Seed the cursor so the subscriber sends ?since=
	if err := database.EventCursor.Set(context.Background(), 1); err != nil {
		t.Fatalf("seed cursor: %v", err)
	}

	client := argus.New(srv.URL, "tok")
	sub := NewSubscriber(client, database, nil)

	var seen []int64
	var mu sync.Mutex
	sub.Register(HandlerFunc(func(ctx context.Context, ev argus.Event) {
		mu.Lock()
		seen = append(seen, ev.ID)
		mu.Unlock()
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = sub.Run(ctx)
		close(done)
	}()

	// Wait for all three events to be observed or timeout.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(seen)
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(seen) < 3 {
		t.Fatalf("saw only %d events: %+v", len(seen), seen)
	}

	cursor, err := database.EventCursor.Get(context.Background())
	if err != nil {
		t.Fatalf("EventCursor.Get: %v", err)
	}
	if cursor < 3 {
		t.Fatalf("cursor = %d, want >=3", cursor)
	}
}

func TestSubscriber_MultipleHandlersFanOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		b, _ := json.Marshal(argus.Event{ID: 1, Type: "task.created", TaskID: "t1"})
		_, _ = io.WriteString(w, "event: task.created\ndata: "+string(b)+"\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer srv.Close()

	dbPath := filepath.Join(t.TempDir(), "events.sqlite")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer database.Close()

	client := argus.New(srv.URL, "tok")
	sub := NewSubscriber(client, database, nil)

	var saw1, saw2 bool
	var mu sync.Mutex
	sub.Register(HandlerFunc(func(ctx context.Context, ev argus.Event) {
		mu.Lock()
		saw1 = true
		mu.Unlock()
	}))
	sub.Register(HandlerFunc(func(ctx context.Context, ev argus.Event) {
		mu.Lock()
		saw2 = true
		mu.Unlock()
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = sub.Run(ctx)
		close(done)
	}()

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		ok := saw1 && saw2
		mu.Unlock()
		if ok {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if !saw1 || !saw2 {
		t.Fatalf("not all handlers fired: saw1=%v saw2=%v", saw1, saw2)
	}
}
