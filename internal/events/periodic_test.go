package events

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

// periodicEnv wires a counting argus stub + hera DB + ResyncHandler for
// the periodic-reconciler tests. The hits counter increments every time
// the stub serves GET /api/tasks, which Reconcile hits once per tick.
type periodicEnv struct {
	client  *argus.Client
	db      *db.DB
	handler *ResyncHandler
	hits    *int32
}

func setupPeriodic(t *testing.T) *periodicEnv {
	t.Helper()
	var hits int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_ = json.NewEncoder(w).Encode(struct {
			Tasks []argus.Task `json:"tasks"`
		}{Tasks: nil})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	dbPath := filepath.Join(t.TempDir(), "hera.sqlite")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	client := argus.New(srv.URL, "tok")
	handler := NewResyncHandler(client, database, nil)
	return &periodicEnv{client: client, db: database, handler: handler, hits: &hits}
}

func TestPeriodic_TickFiresReconcile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	e := setupPeriodic(t)
	p := NewPeriodicReconciler(e.handler, 50*time.Millisecond, nil)

	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(e.hits) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	got := atomic.LoadInt32(e.hits)
	if got < 2 {
		t.Fatalf("expected at least 2 reconcile ticks, got %d", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatalf("PeriodicReconciler.Run did not exit within 1s of cancel")
	}
}

func TestPeriodic_ContextCancelExitsCleanly(t *testing.T) {
	e := setupPeriodic(t)
	// 1 hour interval so the goroutine is parked in select <-ctx.Done()
	// rather than mid-tick; verifies the cancel-path is responsive.
	p := NewPeriodicReconciler(e.handler, 1*time.Hour, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("PeriodicReconciler.Run did not exit within 500ms of cancel")
	}
}

func TestPeriodic_IntervalRespected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	e := setupPeriodic(t)
	p := NewPeriodicReconciler(e.handler, 200*time.Millisecond, nil)

	done := make(chan struct{})
	go func() { defer close(done); p.Run(ctx) }()

	time.Sleep(550 * time.Millisecond)
	cancel()
	<-done

	got := atomic.LoadInt32(e.hits)
	if got < 2 {
		t.Fatalf("expected at least 2 ticks in 550ms at 200ms interval, got %d", got)
	}
	if got > 5 {
		t.Fatalf("expected at most 5 ticks in 550ms at 200ms interval, got %d (interval not respected)", got)
	}
}

func TestPeriodic_NonPositiveIntervalUsesDefault(t *testing.T) {
	e := setupPeriodic(t)
	p := NewPeriodicReconciler(e.handler, 0, nil)
	if p.interval != DefaultReconcileInterval {
		t.Fatalf("interval=0 should fall back to DefaultReconcileInterval, got %v", p.interval)
	}
	p = NewPeriodicReconciler(e.handler, -1*time.Second, nil)
	if p.interval != DefaultReconcileInterval {
		t.Fatalf("interval<0 should fall back to DefaultReconcileInterval, got %v", p.interval)
	}
}
