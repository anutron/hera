package db

import "sync"

// EntityType identifies which table a write event came from. Only the
// tables the rail UI watches are represented.
type EntityType string

const (
	EntityOrchestrator EntityType = "orchestrator"
	EntityRole         EntityType = "role"
	EntityBinding      EntityType = "binding"
)

// EventOp identifies the kind of mutation.
type EventOp string

const (
	OpInsert EventOp = "insert"
	OpUpdate EventOp = "update"
	OpDelete EventOp = "delete"
)

// Event describes a single DAO write on a watched table.
//
// The rail subscriber treats every event as "something changed; refresh
// the tree." The fields are exposed for callers that want to filter or
// log, not because the rail needs them.
type Event struct {
	Entity EntityType
	Op     EventOp
	ID     int64
}

// Broadcaster is an in-process pub/sub used by DAOs to announce writes
// to subscribers like the rail UI. It is safe for concurrent use.
//
// Each subscriber gets a buffered channel. When a subscriber's buffer is
// full, Emit drops the event for that subscriber rather than blocking.
// Producers (DAO write paths) therefore never block on a slow consumer.
// The rail consumer debounces and re-reads from the DB anyway, so a
// dropped event only matters if no further event ever arrives — which
// the rail UI's "rebuild from current DB state" model tolerates.
type Broadcaster struct {
	mu     sync.Mutex
	subs   map[int]chan Event
	nextID int
	closed bool
	bufSz  int
}

// NewBroadcaster returns a ready-to-use Broadcaster. Subscriber buffers
// default to 16 events each.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		subs:  make(map[int]chan Event),
		bufSz: 16,
	}
}

// Subscribe registers a new subscriber and returns its receive-only
// channel along with a cancel func that unsubscribes and closes the
// channel. The cancel is idempotent.
//
// If the broadcaster has been Closed, Subscribe returns an already-closed
// channel and a no-op cancel.
func (b *Broadcaster) Subscribe() (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		ch := make(chan Event)
		close(ch)
		return ch, func() {}
	}
	id := b.nextID
	b.nextID++
	ch := make(chan Event, b.bufSz)
	b.subs[id] = ch
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if existing, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(existing)
		}
	}
}

// Emit delivers the event to every current subscriber. Subscribers whose
// buffers are full have the event dropped silently. Emit never blocks.
// Emitting on a Closed broadcaster is a no-op.
func (b *Broadcaster) Emit(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default:
			// drop
		}
	}
}

// SubscriberCount returns the number of currently-registered
// subscribers. Intended for tests that need to assert
// subscribe/unsubscribe lifecycle without observing emitted events.
func (b *Broadcaster) SubscriberCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// Close releases every current subscriber's channel and marks the
// broadcaster as closed. Subsequent Emit calls are no-ops; subsequent
// Subscribe calls return an already-closed channel. Close is idempotent.
func (b *Broadcaster) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for id, ch := range b.subs {
		close(ch)
		delete(b.subs, id)
	}
}
