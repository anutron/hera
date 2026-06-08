package inject

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/anutron/hera/internal/db"
)

// PTYWriter writes raw bytes to an argus task's PTY. *argus.Client
// satisfies this with PostTaskInput.
type PTYWriter interface {
	PostTaskInput(ctx context.Context, taskID string, bytes []byte) (int, error)
}

// IdleChecker reports whether an argus task is in the eligible-for-auto-
// submit state. *idle.Tracker satisfies this.
type IdleChecker interface {
	IsIdle(taskID string) bool
}

// Injector delivers messages into argus PTYs with idle gating.
type Injector struct {
	pty               PTYWriter
	idle              IdleChecker
	autoInjectEnabled atomic.Bool
}

// New constructs an Injector wired to the given dependencies.
// autoInjectEnabled defaults to true (v1 behavior).
func New(pty PTYWriter, idle IdleChecker) *Injector {
	i := &Injector{pty: pty, idle: idle}
	i.autoInjectEnabled.Store(true)
	return i
}

// SetAutoInjectEnabled flips the master switch over the auto-submit
// branch of Inject. When false, every message lands in busy_buffer mode
// regardless of recipient idle state. Lock-free at the read site.
func (i *Injector) SetAutoInjectEnabled(b bool) {
	i.autoInjectEnabled.Store(b)
}

// FormatPointer returns the PTY representation of an initial message delivery.
// The recipient sees a subject-line pointer and calls hera_inbox to read the
// full body (which marks the message as read).
func FormatPointer(senderRoleName string, msgID int64, tldr string) string {
	return fmt.Sprintf("[hera from %s] msg #%d — %s", senderRoleName, msgID, tldr)
}

// Inject delivers a pointer notification into the recipient task's PTY.
// Returns the chosen delivery mode so the caller can persist it on the
// message row. The recipient reads the full body by calling hera_inbox.
//
//   - DeliveryIdleSubmit: PTY was idle, pointer+"\r" injected, auto-submits.
//     CR (not LF) is the byte the keyboard's Return key emits, and the
//     recipient's TUI runs the PTY in raw mode so termios does not
//     translate CR<->LF — only CR triggers submit.
//   - DeliveryBusyBuffer: PTY was not idle, pointer injected with no
//     trailing terminator, the user submits when ready.
//
// Errors are returned without choosing a fallback mode – the caller
// decides how to retry or mark the message as failed.
func (i *Injector) Inject(ctx context.Context, taskID, senderRoleName string, msgID int64, tldr string) (db.DeliveryMode, error) {
	formatted := FormatPointer(senderRoleName, msgID, tldr)
	isIdle := i.idle.IsIdle(taskID)
	if isIdle && i.autoInjectEnabled.Load() {
		if _, err := i.pty.PostTaskInput(ctx, taskID, []byte(formatted+"\r")); err != nil {
			return db.DeliveryPending, fmt.Errorf("inject (idle path): %w", err)
		}
		return db.DeliveryIdleSubmit, nil
	}
	if _, err := i.pty.PostTaskInput(ctx, taskID, []byte(formatted)); err != nil {
		return db.DeliveryPending, fmt.Errorf("inject (busy path): %w", err)
	}
	return db.DeliveryBusyBuffer, nil
}
