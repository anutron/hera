package events

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/db"
)

// Handler reacts to argus events. Implementations should be cheap; the
// dispatcher invokes every registered handler synchronously per event.
//
// Handler implementations MUST NOT block – use goroutines for slow work.
type Handler interface {
	HandleEvent(ctx context.Context, ev argus.Event)
}

// HandlerFunc adapts a plain function to the Handler interface.
type HandlerFunc func(ctx context.Context, ev argus.Event)

// HandleEvent implements Handler.
func (f HandlerFunc) HandleEvent(ctx context.Context, ev argus.Event) { f(ctx, ev) }

// Subscriber connects hera to argus's SSE event stream, persists the
// cursor across daemon restarts, and dispatches each event to every
// registered handler.
type Subscriber struct {
	client *argus.Client
	db     *db.DB
	log    *slog.Logger

	mu       sync.RWMutex
	handlers []Handler
}

// NewSubscriber constructs a Subscriber.
func NewSubscriber(client *argus.Client, database *db.DB, log *slog.Logger) *Subscriber {
	if log == nil {
		log = slog.Default()
	}
	return &Subscriber{client: client, db: database, log: log}
}

// Register adds a handler to the dispatch list. Safe to call before or
// after Run; handlers added mid-run are picked up by subsequent events.
func (s *Subscriber) Register(h Handler) {
	s.mu.Lock()
	s.handlers = append(s.handlers, h)
	s.mu.Unlock()
}

// snapshotHandlers returns a copy of the current handler slice so a long
// callback doesn't hold the lock.
func (s *Subscriber) snapshotHandlers() []Handler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Handler, len(s.handlers))
	copy(out, s.handlers)
	return out
}

// Run starts the SSE subscription. Loads the cursor from the DB, calls
// argus.Client.StreamEvents, dispatches each event, and persists the
// cursor after each delivery. Blocks until ctx is canceled.
func (s *Subscriber) Run(ctx context.Context) error {
	cursor, err := s.db.EventCursor.Get(ctx)
	if err != nil {
		return fmt.Errorf("events.Run: load cursor: %w", err)
	}
	s.log.Info("event subscriber starting", "since", cursor)

	return s.client.StreamEvents(ctx, cursor, func(ev argus.Event) {
		for _, h := range s.snapshotHandlers() {
			h.HandleEvent(ctx, ev)
		}
		if ev.ID > 0 {
			if err := s.db.EventCursor.Set(ctx, ev.ID); err != nil {
				s.log.Warn("cursor update failed", "id", ev.ID, "err", err)
			}
		}
	})
}
