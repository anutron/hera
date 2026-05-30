package view

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestSession_RailRefresherSubscribedAndStoppedOnClose asserts that a
// live websocket session subscribes to the DAO broadcaster (so DAO
// writes drive rail refreshes) and tears the subscription down when
// the session closes.
func TestSession_RailRefresherSubscribedAndStoppedOnClose(t *testing.T) {
	d := openTestDB(t)
	runner := NewSessionFunc(d, nil, nil, nil, slog.Default())
	srv := NewServer(nil, runner)
	t.Cleanup(srv.Stop)

	_, wsURL := newTestHTTPServer(t, srv)

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer dialCancel()

	conn, _, err := websocket.Dial(dialCtx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })

	// Wait for the runner to bootstrap (NewRailRefresher.Subscribe).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if d.Events.SubscriberCount() >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := d.Events.SubscriberCount(); got < 1 {
		t.Fatalf("no broadcaster subscribers after session bootstrap; want >= 1")
	}

	// Close the conn to trigger the runner's teardown chain (rail.Stop
	// unsubscribes from the broadcaster).
	_ = conn.CloseNow()

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if d.Events.SubscriberCount() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("rail refresher not unsubscribed after session close; subs = %d", d.Events.SubscriberCount())
}
