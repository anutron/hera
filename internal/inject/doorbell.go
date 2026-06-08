package inject

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/anutron/hera/internal/db"
)

// BindingLookup resolves the live binding for a given role ID.
// *db.BindingsDAO satisfies this interface.
type BindingLookup interface {
	GetLiveByRole(ctx context.Context, roleID int64) (*db.Binding, error)
}

// NudgeStore is the subset of db.MessagesDAO used by the watcher.
type NudgeStore interface {
	UnreadIdleSubmitStale(ctx context.Context, firstCutoff, repeatCutoff time.Time, maxNudges int) ([]*db.Message, error)
	RecordNudge(ctx context.Context, messageIDs []int64) error
}

// FormatDoorbell returns the PTY bytes for a doorbell re-nudge. For a single
// unread message the TLDR is included; for multiple messages a count is shown.
// The returned string is terminated by CR so it auto-submits when the
// recipient is idle.
func FormatDoorbell(msgs []*db.Message) string {
	if len(msgs) == 1 {
		return fmt.Sprintf("[hera doorbell] msg #%d — %s — call hera_inbox\r", msgs[0].ID, msgs[0].Tldr)
	}
	return fmt.Sprintf("[hera doorbell] %d unread messages — call hera_inbox\r", len(msgs))
}

// DeliveryWatcher is a daemon-lifetime goroutine that periodically scans for
// idle_submit messages that remain unread past the configured threshold and
// re-nudges their recipients with a non-duplicating doorbell PTY write.
//
// The watcher fires one doorbell per recipient per scan (aggregating all stale
// messages for a given recipient into a single write), then records the nudge
// on each message row so the spacing and cap constraints are enforced on the
// next scan.
type DeliveryWatcher struct {
	msgs     NudgeStore
	bindings BindingLookup
	pty      PTYWriter
	after    time.Duration
	every    time.Duration
	max      int
	log      *slog.Logger
}

// NewDeliveryWatcher constructs a DeliveryWatcher. nudgeAfter is the initial
// wait after delivery before the first nudge; nudgeEvery is the spacing between
// subsequent nudges; maxNudges is the per-message cap.
func NewDeliveryWatcher(
	msgs NudgeStore,
	bindings BindingLookup,
	pty PTYWriter,
	nudgeAfter, nudgeEvery time.Duration,
	maxNudges int,
	log *slog.Logger,
) *DeliveryWatcher {
	if log == nil {
		log = slog.Default()
	}
	return &DeliveryWatcher{
		msgs:     msgs,
		bindings: bindings,
		pty:      pty,
		after:    nudgeAfter,
		every:    nudgeEvery,
		max:      maxNudges,
		log:      log,
	}
}

// Run blocks, ticking at half the nudgeEvery interval (minimum 5s), and calls
// scan on each tick. Returns when ctx is canceled.
func (w *DeliveryWatcher) Run(ctx context.Context) {
	interval := w.every / 2
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.scan(ctx); err != nil && ctx.Err() == nil {
				w.log.Warn("delivery watcher scan failed", "err", err)
			}
		}
	}
}

// scan queries for stale unread idle_submit messages and fires one doorbell
// per recipient. All stale message IDs for a recipient are recorded in a
// single RecordNudge call.
func (w *DeliveryWatcher) scan(ctx context.Context) error {
	now := time.Now()
	firstCutoff := now.Add(-w.after)
	repeatCutoff := now.Add(-w.every)

	msgs, err := w.msgs.UnreadIdleSubmitStale(ctx, firstCutoff, repeatCutoff, w.max)
	if err != nil {
		return fmt.Errorf("doorbell scan: %w", err)
	}
	if len(msgs) == 0 {
		return nil
	}

	// Group messages by recipient role ID.
	byRole := make(map[int64][]*db.Message) // roleID → messages
	for _, m := range msgs {
		byRole[m.ToRoleID] = append(byRole[m.ToRoleID], m)
	}

	for roleID, roleMsgs := range byRole {
		bnd, err := w.bindings.GetLiveByRole(ctx, roleID)
		if err != nil {
			// Recipient unbound — skip; they'll see messages via hera_inbox
			// when they re-bind.
			w.log.Debug("doorbell: recipient unbound, skipping", "role_id", roleID)
			continue
		}

		doorbell := []byte(FormatDoorbell(roleMsgs))
		if _, err := w.pty.PostTaskInput(ctx, bnd.ArgusTaskID, doorbell); err != nil {
			w.log.Warn("doorbell: PTY write failed", "role_id", roleID, "task_id", bnd.ArgusTaskID, "err", err)
			continue
		}

		msgIDs := make([]int64, len(roleMsgs))
		for i, m := range roleMsgs {
			msgIDs[i] = m.ID
		}
		if err := w.msgs.RecordNudge(ctx, msgIDs); err != nil {
			w.log.Warn("doorbell: record nudge failed", "role_id", roleID, "err", err)
		}

		w.log.Debug("doorbell: nudged recipient",
			"role_id", roleID,
			"task_id", bnd.ArgusTaskID,
			"msg_count", len(roleMsgs),
		)
	}
	return nil
}
