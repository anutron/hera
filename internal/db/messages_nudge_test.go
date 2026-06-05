package db

import (
	"context"
	"testing"
	"time"
)

// helpers shared with existing message tests.
func seedOrchAndRoles(t *testing.T, d *DB) (coord *Role, worker *Role) {
	t.Helper()
	ctx := context.Background()
	orch, _ := d.Orchestrators.Create(ctx, "test-orch")
	coord, _ = d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: KindCoordinator, ArgusProject: "p",
	})
	worker, _ = d.Roles.Create(ctx, CreateRoleInput{
		OrchestratorID: orch.ID, Name: "worker", Kind: KindWorker, ArgusProject: "p",
	})
	return coord, worker
}

func mustCreateMsg(t *testing.T, d *DB, from, to *Role) *Message {
	t.Helper()
	ctx := context.Background()
	msg, err := d.Messages.Create(ctx, CreateMessageInput{
		FromRoleID: from.ID, ToRoleID: to.ID, Body: "ping",
	})
	if err != nil {
		t.Fatalf("Messages.Create: %v", err)
	}
	return msg
}

func mustSetDelivered(t *testing.T, d *DB, msgID int64, mode DeliveryMode) {
	t.Helper()
	if err := d.Messages.SetDelivered(context.Background(), msgID, mode); err != nil {
		t.Fatalf("Messages.SetDelivered: %v", err)
	}
}

// TestMessages_NudgeColumnsZeroOnCreate asserts that new messages have
// nudge_count=0 and nudged_at=NULL.
func TestMessages_NudgeColumnsZeroOnCreate(t *testing.T) {
	d := openTestDB(t)
	coord, worker := seedOrchAndRoles(t, d)
	msg := mustCreateMsg(t, d, coord, worker)

	got, err := d.Messages.GetByID(context.Background(), msg.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.NudgeCount != 0 {
		t.Fatalf("nudge_count = %d, want 0", got.NudgeCount)
	}
	if got.NudgedAt != nil {
		t.Fatalf("nudged_at should be nil on new message, got %v", got.NudgedAt)
	}
}

// TestMessages_UnreadIdleSubmitStale_ReturnsStaleMsgs asserts the query
// returns idle_submit messages past the first-nudge cutoff.
func TestMessages_UnreadIdleSubmitStale_ReturnsStaleMsgs(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	coord, worker := seedOrchAndRoles(t, d)

	msg := mustCreateMsg(t, d, coord, worker)
	mustSetDelivered(t, d, msg.ID, DeliveryIdleSubmit)

	// firstCutoff is in the future relative to delivered_at so the message qualifies.
	firstCutoff := time.Now().Add(time.Minute) // older than this
	repeatCutoff := time.Now().Add(time.Minute)

	stale, err := d.Messages.UnreadIdleSubmitStale(ctx, firstCutoff, repeatCutoff, 5)
	if err != nil {
		t.Fatalf("UnreadIdleSubmitStale: %v", err)
	}
	if len(stale) != 1 || stale[0].ID != msg.ID {
		t.Fatalf("expected 1 stale message (id=%d), got %+v", msg.ID, stale)
	}
}

// TestMessages_UnreadIdleSubmitStale_ExcludesReadMsgs asserts that messages
// with read_at set are not returned.
func TestMessages_UnreadIdleSubmitStale_ExcludesReadMsgs(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	coord, worker := seedOrchAndRoles(t, d)

	msg := mustCreateMsg(t, d, coord, worker)
	mustSetDelivered(t, d, msg.ID, DeliveryIdleSubmit)
	if _, err := d.Messages.MarkRead(ctx, worker.ID, []int64{msg.ID}); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}

	firstCutoff := time.Now().Add(time.Minute)
	stale, err := d.Messages.UnreadIdleSubmitStale(ctx, firstCutoff, firstCutoff, 5)
	if err != nil {
		t.Fatalf("UnreadIdleSubmitStale: %v", err)
	}
	if len(stale) != 0 {
		t.Fatalf("expected 0 stale (already read), got %d", len(stale))
	}
}

// TestMessages_UnreadIdleSubmitStale_ExcludesBusyBuffer asserts that
// busy_buffer messages are never eligible for nudge.
func TestMessages_UnreadIdleSubmitStale_ExcludesBusyBuffer(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	coord, worker := seedOrchAndRoles(t, d)

	msg := mustCreateMsg(t, d, coord, worker)
	mustSetDelivered(t, d, msg.ID, DeliveryBusyBuffer)

	firstCutoff := time.Now().Add(time.Minute)
	stale, err := d.Messages.UnreadIdleSubmitStale(ctx, firstCutoff, firstCutoff, 5)
	if err != nil {
		t.Fatalf("UnreadIdleSubmitStale: %v", err)
	}
	if len(stale) != 0 {
		t.Fatalf("busy_buffer message should not appear as stale, got %d", len(stale))
	}
}

// TestMessages_UnreadIdleSubmitStale_ExcledesAtCap asserts that messages at
// the nudge cap are excluded.
func TestMessages_UnreadIdleSubmitStale_ExcludesAtCap(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	coord, worker := seedOrchAndRoles(t, d)

	msg := mustCreateMsg(t, d, coord, worker)
	mustSetDelivered(t, d, msg.ID, DeliveryIdleSubmit)

	// Nudge it exactly maxNudges times.
	maxNudges := 3
	for i := 0; i < maxNudges; i++ {
		if err := d.Messages.RecordNudge(ctx, []int64{msg.ID}); err != nil {
			t.Fatalf("RecordNudge %d: %v", i, err)
		}
	}

	firstCutoff := time.Now().Add(time.Minute)
	repeatCutoff := time.Now().Add(time.Minute)
	stale, err := d.Messages.UnreadIdleSubmitStale(ctx, firstCutoff, repeatCutoff, maxNudges)
	if err != nil {
		t.Fatalf("UnreadIdleSubmitStale: %v", err)
	}
	if len(stale) != 0 {
		t.Fatalf("message at cap should be excluded, got %d", len(stale))
	}
}

// TestMessages_RecordNudge_IncrementsCountAndSetsTimestamp asserts that
// RecordNudge increments nudge_count and sets nudged_at.
func TestMessages_RecordNudge_IncrementsCountAndSetsTimestamp(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	coord, worker := seedOrchAndRoles(t, d)

	msg := mustCreateMsg(t, d, coord, worker)
	mustSetDelivered(t, d, msg.ID, DeliveryIdleSubmit)

	before := time.Now()
	if err := d.Messages.RecordNudge(ctx, []int64{msg.ID}); err != nil {
		t.Fatalf("RecordNudge: %v", err)
	}
	after := time.Now()

	got, err := d.Messages.GetByID(ctx, msg.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.NudgeCount != 1 {
		t.Fatalf("nudge_count = %d, want 1", got.NudgeCount)
	}
	if got.NudgedAt == nil {
		t.Fatalf("nudged_at is nil after RecordNudge")
	}
	if got.NudgedAt.Before(before) || got.NudgedAt.After(after.Add(time.Second)) {
		t.Fatalf("nudged_at %v not in expected range [%v, %v]", got.NudgedAt, before, after)
	}
}

// TestMessages_RecordNudge_MultipleTimes asserts nudge_count accumulates.
func TestMessages_RecordNudge_MultipleTimes(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	coord, worker := seedOrchAndRoles(t, d)

	msg := mustCreateMsg(t, d, coord, worker)
	mustSetDelivered(t, d, msg.ID, DeliveryIdleSubmit)

	for i := 1; i <= 3; i++ {
		if err := d.Messages.RecordNudge(ctx, []int64{msg.ID}); err != nil {
			t.Fatalf("RecordNudge %d: %v", i, err)
		}
	}
	got, _ := d.Messages.GetByID(ctx, msg.ID)
	if got.NudgeCount != 3 {
		t.Fatalf("nudge_count = %d, want 3", got.NudgeCount)
	}
}

// TestMessages_RecordNudge_SkipsAlreadyRead asserts that RecordNudge does not
// increment nudge_count on messages that are already read.
func TestMessages_RecordNudge_SkipsAlreadyRead(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	coord, worker := seedOrchAndRoles(t, d)

	msg := mustCreateMsg(t, d, coord, worker)
	mustSetDelivered(t, d, msg.ID, DeliveryIdleSubmit)
	if _, err := d.Messages.MarkRead(ctx, worker.ID, []int64{msg.ID}); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}

	if err := d.Messages.RecordNudge(ctx, []int64{msg.ID}); err != nil {
		t.Fatalf("RecordNudge: %v", err)
	}
	got, _ := d.Messages.GetByID(ctx, msg.ID)
	if got.NudgeCount != 0 {
		t.Fatalf("RecordNudge should skip read messages; nudge_count = %d", got.NudgeCount)
	}
}

// TestMessages_UnreadIdleSubmitStale_RepeatCutoffEnforced asserts that after
// the first nudge, only messages nudged before repeatCutoff qualify.
//
// repeatCutoff is an absolute time: messages where nudged_at <= repeatCutoff
// are eligible. Setting repeatCutoff in the past (before nudged_at) means the
// message is too recent; setting it at or after nudged_at means it's due.
func TestMessages_UnreadIdleSubmitStale_RepeatCutoffEnforced(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)
	coord, worker := seedOrchAndRoles(t, d)

	msg := mustCreateMsg(t, d, coord, worker)
	mustSetDelivered(t, d, msg.ID, DeliveryIdleSubmit)
	// Record one nudge so nudge_count=1 and nudged_at≈now.
	if err := d.Messages.RecordNudge(ctx, []int64{msg.ID}); err != nil {
		t.Fatalf("RecordNudge: %v", err)
	}

	// repeatCutoff 1 minute in the past: message was nudged "just now", which
	// is newer than the cutoff, so nudged_at > repeatCutoff → not selected.
	firstCutoff := time.Now().Add(time.Minute)
	repeatCutoffPast := time.Now().Add(-time.Minute)
	stale, err := d.Messages.UnreadIdleSubmitStale(ctx, firstCutoff, repeatCutoffPast, 5)
	if err != nil {
		t.Fatalf("UnreadIdleSubmitStale: %v", err)
	}
	if len(stale) != 0 {
		t.Fatalf("recently nudged message should not appear stale yet (cutoff in past), got %d", len(stale))
	}

	// repeatCutoff 1 minute in the future: message was nudged "just now", which
	// is older than the cutoff, so nudged_at <= repeatCutoff → selected.
	repeatCutoffFuture := time.Now().Add(time.Minute)
	stale, err = d.Messages.UnreadIdleSubmitStale(ctx, firstCutoff, repeatCutoffFuture, 5)
	if err != nil {
		t.Fatalf("UnreadIdleSubmitStale (future cutoff): %v", err)
	}
	if len(stale) != 1 {
		t.Fatalf("message should appear stale when cutoff is after nudged_at, got %d", len(stale))
	}
}
