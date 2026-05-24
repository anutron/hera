package inject

import (
	"context"
	"fmt"

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
	pty  PTYWriter
	idle IdleChecker
}

// New constructs an Injector wired to the given dependencies.
func New(pty PTYWriter, idle IdleChecker) *Injector {
	return &Injector{pty: pty, idle: idle}
}

// FormatBody returns the on-PTY representation of a message body. Exposed
// for tests so they can assert the exact byte sequence.
func FormatBody(senderRoleName, body string) string {
	return fmt.Sprintf("[hera from %s] %s", senderRoleName, body)
}

// Inject delivers a message into the recipient task's PTY. Returns the
// chosen delivery mode so the caller can persist it on the message row.
//
//   - DeliveryIdleSubmit: PTY was idle, body+"\n" injected, auto-submits.
//   - DeliveryBusyBuffer: PTY was not idle, body injected without "\n",
//     the user submits when ready.
//
// Errors are returned without choosing a fallback mode – the caller
// decides how to retry or mark the message as failed.
func (i *Injector) Inject(ctx context.Context, taskID, senderRoleName, body string) (db.DeliveryMode, error) {
	formatted := FormatBody(senderRoleName, body)
	if i.idle.IsIdle(taskID) {
		if _, err := i.pty.PostTaskInput(ctx, taskID, []byte(formatted+"\n")); err != nil {
			return db.DeliveryPending, fmt.Errorf("inject (idle path): %w", err)
		}
		return db.DeliveryIdleSubmit, nil
	}
	if _, err := i.pty.PostTaskInput(ctx, taskID, []byte(formatted)); err != nil {
		return db.DeliveryPending, fmt.Errorf("inject (busy path): %w", err)
	}
	return db.DeliveryBusyBuffer, nil
}
