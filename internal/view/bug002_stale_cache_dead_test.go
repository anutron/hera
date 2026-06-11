package view

import (
	"context"
	"testing"

	"github.com/anutron/hera/internal/db"
)

// staleStatePaneSource models the daemon's argus state cache in the precise
// failure condition behind BUG-002: the cache has completed at least one poll
// (StatesReady == true, the readiness latch never resets) but its polls have
// since stopped succeeding (argus bounced / hung / slow — the 30s HTTP timeout
// path retains the frozen snapshot), so the snapshot is STALE (StatesFresh ==
// false) and a given live task created/changed after the freeze is simply
// ABSENT from it.
type staleStatePaneSource struct {
	fakePaneSource
	states map[string]ArgusTaskState
	ready  bool
	fresh  bool
}

func (s *staleStatePaneSource) TaskState(taskID string) (ArgusTaskState, bool) {
	st, ok := s.states[taskID]
	return st, ok
}
func (s *staleStatePaneSource) StatesReady() bool { return s.ready }
func (s *staleStatePaneSource) StatesFresh() bool { return s.fresh }

// TestBUG002_StaleCacheDoesNotClassifyLiveTaskDead is the core claim: a role
// whose bound argus task is ABSENT from the state cache MUST NOT be classified
// Dead when the cache is STALE (ready but no recent successful poll). Argus's
// own state (MCP task_list) still reports the task live + non-archived; hera's
// cache simply froze and never learned about it. Per spec, deadness is
// record-NONEXISTENCE, read from a TRUSTWORTHY snapshot — a stale cache reports
// "unknown", not "gone", so the row stays in the active tree.
func TestBUG002_StaleCacheDoesNotClassifyLiveTaskDead(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, err := d.Orchestrators.Create(ctx, "sherlock-3b")
	if err != nil {
		t.Fatalf("orch: %v", err)
	}
	w, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "1c-team-link", Kind: db.KindWorker, ArgusProject: "sherlock",
	})
	if err != nil {
		t.Fatalf("role: %v", err)
	}
	if _, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: w.ID, ArgusTaskID: "1c-add-memory-svc", WorktreePath: "/w",
	}); err != nil {
		t.Fatalf("binding: %v", err)
	}

	// Cache is READY (it polled once) but STALE (polls stopped succeeding) and
	// has NO entry for the sub-coord task — exactly the BUG-002 condition.
	src := &staleStatePaneSource{
		states: map[string]ArgusTaskState{},
		ready:  true,
		fresh:  false,
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	got := findRoleEntry(a, w.ID)
	if got == nil {
		t.Fatalf("worker role missing from rail data")
	}
	if got.Dead {
		t.Fatalf("a stale cache MUST NOT classify a live (argus-reported) task Dead; got Dead=true")
	}
}

// TestBUG002_FreshCacheStillBucketsGoneTask is the guard rail in the other
// direction: the staleness tolerance MUST NOT defeat the genuine prune case.
// While polls keep succeeding (cache FRESH), a task that is truly absent from
// the snapshot (pruned / 404) is still record-gone and MUST bucket Dead.
func TestBUG002_FreshCacheStillBucketsGoneTask(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, err := d.Orchestrators.Create(ctx, "sherlock-3b")
	if err != nil {
		t.Fatalf("orch: %v", err)
	}
	w, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "gone-worker", Kind: db.KindWorker, ArgusProject: "sherlock",
	})
	if err != nil {
		t.Fatalf("role: %v", err)
	}
	if _, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: w.ID, ArgusTaskID: "pruned-task", WorktreePath: "/w",
	}); err != nil {
		t.Fatalf("binding: %v", err)
	}

	// Cache is READY and FRESH (polls succeeding) yet has no record of the task:
	// argus genuinely pruned it. This MUST still bucket Dead.
	src := &staleStatePaneSource{
		states: map[string]ArgusTaskState{},
		ready:  true,
		fresh:  true,
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	got := findRoleEntry(a, w.ID)
	if got == nil {
		t.Fatalf("worker role missing from rail data")
	}
	if !got.Dead {
		t.Fatalf("a fresh cache with no record for the task MUST classify it Dead (genuine prune); got Dead=false")
	}
}

// findRoleEntry locates a roleEntry by role id across the rail's orchestrators
// (active tree + nested sub-coords).
func findRoleEntry(a *App, roleID int64) *roleEntry {
	for _, o := range a.pieces.rail.orchestrators {
		if r := findRoleInOrch(o, roleID); r != nil {
			return r
		}
	}
	return nil
}

func findRoleInOrch(o *orchEntry, roleID int64) *roleEntry {
	for _, r := range o.Roles {
		if r.RoleID == roleID {
			return r
		}
		// Sub-coordinators nest beneath a worker row via childOrch.
		if r.childOrch != nil {
			if nested := findRoleInOrch(r.childOrch, roleID); nested != nil {
				return nested
			}
		}
	}
	return nil
}
