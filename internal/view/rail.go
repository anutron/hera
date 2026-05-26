// Package view holds the in-process pieces of the hera-view plugin
// surface that do not depend on argus, tview, or a live WebSocket. The
// pieces that DO depend on tview live in later-stage files; the rail
// refresher here is intentionally tview-agnostic so it can be wired
// into the tview Application by Stage F or tested in isolation.
package view

import (
	"sync"
	"time"

	"github.com/anutron/hera/internal/db"
)

// DefaultRailDebounce is the default debounce window for coalescing
// bursts of DAO events into a single rail refresh. The hera-view spec
// requires the rail to refresh within ~100 ms of any DAO write.
const DefaultRailDebounce = 100 * time.Millisecond

// RailRefresher subscribes to a db.Broadcaster and invokes the supplied
// refresh callback at most once per debounce window. Bursts of events
// are coalesced into a single callback invocation.
//
// The refresh callback runs on the refresher's own goroutine. In
// production the rail subscriber wraps this around tview's
// QueueUpdateDraw so the actual re-render happens on the tview event
// loop; in tests it can capture invocation counts directly.
//
// The refresher does NOT poll the database. It only invokes refresh in
// response to a DAO event landing on its subscription channel, which
// satisfies the spec's "no polling timer" requirement.
type RailRefresher struct {
	debounce time.Duration
	refresh  func()

	sub    <-chan db.Event
	cancel func()
	done   chan struct{}
	stop   sync.Once
}

// NewRailRefresher subscribes to broadcaster and starts a goroutine
// that coalesces events into refresh callbacks using the default
// debounce window.
func NewRailRefresher(broadcaster *db.Broadcaster, refresh func()) *RailRefresher {
	return NewRailRefresherWith(broadcaster, refresh, DefaultRailDebounce)
}

// NewRailRefresherWith is NewRailRefresher with an explicit debounce
// window. Useful in tests.
func NewRailRefresherWith(broadcaster *db.Broadcaster, refresh func(), debounce time.Duration) *RailRefresher {
	sub, cancel := broadcaster.Subscribe()
	r := &RailRefresher{
		debounce: debounce,
		refresh:  refresh,
		sub:      sub,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	go r.run()
	return r
}

func (r *RailRefresher) run() {
	defer close(r.done)

	// timer is created lazily on the first event so an idle refresher
	// never schedules anything.
	var (
		pending bool
		timer   *time.Timer
		timerC  <-chan time.Time
	)
	for {
		select {
		case _, ok := <-r.sub:
			if !ok {
				if timer != nil {
					timer.Stop()
				}
				return
			}
			if !pending {
				pending = true
				if timer == nil {
					timer = time.NewTimer(r.debounce)
				} else {
					timer.Reset(r.debounce)
				}
				timerC = timer.C
			}
			// If already pending, drop the event — the next timer
			// tick will fire one refresh that picks up the latest
			// DB state for everything since the previous fire.
		case <-timerC:
			timerC = nil
			pending = false
			r.refresh()
		}
	}
}

// Stop unsubscribes from the broadcaster and waits for the runner
// goroutine to exit. Stop is safe to call multiple times.
func (r *RailRefresher) Stop() {
	r.stop.Do(func() {
		r.cancel()
		<-r.done
	})
}
