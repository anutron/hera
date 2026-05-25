package idle

import (
	"context"
	"testing"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/events"
)

func TestTracker_TaskArchivedDropsEntry(t *testing.T) {
	// session.idle populates the tracker; task.archived drops the entry
	// so the map doesn't grow unboundedly.
	tr := New()
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeSessionIdle, TaskID: "t-vanish"})
	if _, _, ok := tr.Lookup("t-vanish"); !ok {
		t.Fatalf("expected tracker to have an entry for t-vanish before archive")
	}
	tr.HandleEvent(context.Background(), argus.Event{Type: events.TypeTaskArchived, TaskID: "t-vanish"})
	if _, _, ok := tr.Lookup("t-vanish"); ok {
		t.Fatalf("expected tracker entry for t-vanish to be dropped after task.archived")
	}
}
