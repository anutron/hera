package argus

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestGetTaskOutput_ReturnsBodyAndTotal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks/t1/output" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Output-Total", "1042")
		_, _ = io.WriteString(w, "hello world")
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")

	snap, err := c.GetTaskOutput(context.Background(), "t1")
	if err != nil {
		t.Fatalf("GetTaskOutput: %v", err)
	}
	if string(snap.Data) != "hello world" {
		t.Fatalf("data = %q", string(snap.Data))
	}
	if snap.Total != 1042 {
		t.Fatalf("total = %d", snap.Total)
	}
}

func TestGetTaskOutput_404TreatedAsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"no output available"}`)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")

	snap, err := c.GetTaskOutput(context.Background(), "t1")
	if err != nil {
		t.Fatalf("GetTaskOutput on 404: %v", err)
	}
	if len(snap.Data) != 0 {
		t.Fatalf("expected empty data, got %q", string(snap.Data))
	}
	if snap.Total != 0 {
		t.Fatalf("expected total=0, got %d", snap.Total)
	}
}

func TestGetTaskOutput_500Surfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `boom`, http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")

	_, err := c.GetTaskOutput(context.Background(), "t1")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestStreamTaskOutput_DecodesBase64Chunks(t *testing.T) {
	chunks := [][]byte{
		[]byte("hello "),
		[]byte("world\n"),
		[]byte("\x1b[31mred\x1b[0m"),
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("since"); got != "42" {
			t.Errorf("since = %q, want 42", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for _, c := range chunks {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", base64.StdEncoding.EncodeToString(c))
			flusher.Flush()
		}
		// Send a clipboard event the proxy should ignore.
		_, _ = fmt.Fprintf(w, "event: clipboard\ndata: {\"text\":\"ignored\"}\n\n")
		flusher.Flush()
		// Send exit so handler returns cleanly.
		_, _ = fmt.Fprintf(w, "event: exit\ndata: {}\n\n")
		flusher.Flush()
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")

	var mu sync.Mutex
	var got [][]byte
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := c.StreamTaskOutput(ctx, "t1", 42, func(p []byte) {
		mu.Lock()
		cp := make([]byte, len(p))
		copy(cp, p)
		got = append(got, cp)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("StreamTaskOutput: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != len(chunks) {
		t.Fatalf("got %d chunks, want %d: %q", len(got), len(chunks), got)
	}
	for i, want := range chunks {
		if string(got[i]) != string(want) {
			t.Fatalf("chunk %d = %q, want %q", i, string(got[i]), string(want))
		}
	}
}

func TestStreamTaskOutput_IgnoresKeepalivesAndUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		_, _ = fmt.Fprintf(w, ": ping\n\n")
		flusher.Flush()
		_, _ = fmt.Fprintf(w, "data: %s\n\n", base64.StdEncoding.EncodeToString([]byte("x")))
		flusher.Flush()
		_, _ = fmt.Fprintf(w, "event: exit\ndata: {}\n\n")
		flusher.Flush()
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")

	var got []byte
	err := c.StreamTaskOutput(context.Background(), "t1", 0, func(p []byte) {
		got = append(got, p...)
	})
	if err != nil {
		t.Fatalf("StreamTaskOutput: %v", err)
	}
	if string(got) != "x" {
		t.Fatalf("got = %q", string(got))
	}
}

func TestStreamTaskOutput_ContextCancelExits(t *testing.T) {
	hang := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		flusher.Flush()
		<-hang
	}))
	defer srv.Close()
	defer close(hang)
	c := New(srv.URL, "tok")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	err := c.StreamTaskOutput(ctx, "t1", 0, func(p []byte) {})
	if err == nil {
		t.Fatalf("expected context error, got nil")
	}
}

// TestStreamTaskOutput_HTTPErrorReturned regresses the "argus 500" path so the
// proxy can decide to retry vs. give up. A non-2xx response must surface as a
// non-nil error rather than being swallowed.
func TestStreamTaskOutput_HTTPErrorReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok")

	err := c.StreamTaskOutput(context.Background(), "t1", 0, func(p []byte) {})
	if err == nil {
		t.Fatalf("expected error on 503")
	}
	_ = strconv.Itoa(0) // keep strconv imported elsewhere if used
}
