package view

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/anutron/hera/internal/db"
)

// findOrchEntry returns the populated orchEntry for the named orchestrator
// from the rail's source-of-truth slice (post resolveSubCoordinators).
func findOrchEntry(rail *railList, name string) *orchEntry {
	for _, o := range rail.orchestrators {
		if o.Name == name {
			return o
		}
	}
	return nil
}

// TestBuildCoordDetails_Fields seeds one orchestrator with a coordinator role
// (carrying prompt/project) and two workers across two repos, then asserts
// buildCoordDetails derives every available field.
func TestBuildCoordDetails_Fields(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	before := time.Now().Add(-time.Second)

	orch, _ := d.Orchestrators.Create(ctx, "alpha")
	coord, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "alpha-coord", Kind: db.KindCoordinator,
		ArgusProject: "repo-a", Prompt: "ship the thing",
	})
	w1, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "builder", Kind: db.KindWorker, ArgusProject: "repo-a",
	})
	w2, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "tester", Kind: db.KindWorker, ArgusProject: "repo-b",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coord.ID, ArgusTaskID: "t-coord", WorktreePath: "/c"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w1.ID, ArgusTaskID: "t-w1", WorktreePath: "/w1"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w2.ID, ArgusTaskID: "t-w2", WorktreePath: "/w2"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	oe := findOrchEntry(a.pieces.rail, "alpha")
	if oe == nil {
		t.Fatalf("orchEntry for alpha not found in rail")
	}

	cd, err := buildCoordDetails(ctx, d, oe)
	if err != nil {
		t.Fatalf("buildCoordDetails: %v", err)
	}

	if cd.Name != "alpha" {
		t.Errorf("Name = %q, want alpha", cd.Name)
	}
	if cd.Prompt != "ship the thing" {
		t.Errorf("Prompt = %q, want %q", cd.Prompt, "ship the thing")
	}
	if cd.Created.IsZero() {
		t.Errorf("Created is zero; want the orchestrator created_at")
	}
	if cd.LastActivity.Before(before) {
		t.Errorf("LastActivity = %v, want >= %v (a recent binding/role time)", cd.LastActivity, before)
	}
	wantRepos := []string{"repo-a", "repo-b"}
	if strings.Join(cd.Repos, ",") != strings.Join(wantRepos, ",") {
		t.Errorf("Repos = %v, want %v (distinct, sorted)", cd.Repos, wantRepos)
	}
	if len(cd.Roster) != 2 {
		t.Fatalf("Roster len = %d, want 2 (the two workers, not the coord)", len(cd.Roster))
	}
	names := map[string]string{}
	for _, r := range cd.Roster {
		names[r.Name] = r.Kind
	}
	if names["builder"] != string(db.KindWorker) {
		t.Errorf("roster builder kind = %q, want worker", names["builder"])
	}
	if names["tester"] != string(db.KindWorker) {
		t.Errorf("roster tester kind = %q, want worker", names["tester"])
	}
	if _, ok := names["alpha-coord"]; ok {
		t.Errorf("roster must NOT include the coordinator role itself")
	}

	// DB-fallback: CoordWorktreePath must be populated from the coord binding.
	if cd.CoordWorktreePath != "/c" {
		t.Errorf("CoordWorktreePath = %q, want /c (from coord binding)", cd.CoordWorktreePath)
	}
}

// TestBuildCoordDetails_RosterIncludesSubCoordinator uses the multi-binding
// fixture: the parent's roster must include the promoted sub-coordinator with
// coordinator kind.
func TestBuildCoordDetails_RosterIncludesSubCoordinator(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	parent, _ := d.Orchestrators.Create(ctx, "parent")
	parentCoord, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: parent.ID, Name: "parent-coord", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	sub, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: parent.ID, Name: "sub", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: parentCoord.ID, ArgusTaskID: "t-parent-coord", WorktreePath: "/pc"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: sub.ID, ArgusTaskID: "t-sub", WorktreePath: "/sub"})

	child, _ := d.Orchestrators.Create(ctx, "child")
	childCoord, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: child.ID, Name: "child-coord", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: childCoord.ID, ArgusTaskID: "t-sub", WorktreePath: "/sub"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	oe := findOrchEntry(a.pieces.rail, "parent")
	if oe == nil {
		t.Fatalf("orchEntry for parent not found")
	}
	cd, err := buildCoordDetails(ctx, d, oe)
	if err != nil {
		t.Fatalf("buildCoordDetails: %v", err)
	}
	foundSub := false
	for _, r := range cd.Roster {
		if r.Name == "sub" {
			foundSub = true
			if r.Kind != string(db.KindCoordinator) {
				t.Errorf("sub roster kind = %q, want coordinator (promoted sub-coordinator)", r.Kind)
			}
		}
	}
	if !foundSub {
		t.Errorf("roster must include the sub-coordinator 'sub'")
	}
}

// TestBuildCoordDetails_AgentNameAndWorktreePath verifies that CoordArgusName
// and CoordWorktreePath are threaded from orchEntry through to coordDetails.
// The state-cache path sets them on orchEntry (tested via populateRail); here
// we verify buildCoordDetails passes them through correctly when they are
// already present on the entry (state-cache populated) and falls back to the
// DB binding when they are absent.
func TestBuildCoordDetails_AgentNameAndWorktreePath(t *testing.T) {
	ctx := context.Background()

	t.Run("passthrough from orchEntry", func(t *testing.T) {
		oe := &orchEntry{
			ID:                1,
			Name:              "rel-1.0",
			CoordTaskID:       "t-coord",
			CoordArgusName:    "the-rel-1.0-task",
			CoordWorktreePath: "/worktrees/Hera/the-rel-1.0-task",
		}
		cd, err := buildCoordDetails(ctx, nil, oe)
		if err != nil {
			t.Fatalf("buildCoordDetails: %v", err)
		}
		if cd.CoordArgusName != "the-rel-1.0-task" {
			t.Errorf("CoordArgusName = %q, want the-rel-1.0-task", cd.CoordArgusName)
		}
		if cd.CoordWorktreePath != "/worktrees/Hera/the-rel-1.0-task" {
			t.Errorf("CoordWorktreePath = %q, want /worktrees/Hera/the-rel-1.0-task", cd.CoordWorktreePath)
		}
	})

	t.Run("DB fallback for worktree when cache empty", func(t *testing.T) {
		d := openTestDB(t)
		orch, _ := d.Orchestrators.Create(ctx, "beta")
		coord, _ := d.Roles.Create(ctx, db.CreateRoleInput{
			OrchestratorID: orch.ID, Name: "beta-coord", Kind: db.KindCoordinator,
		})
		_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{
			RoleID: coord.ID, ArgusTaskID: "t-beta", WorktreePath: "/wt/beta",
		})
		oe := &orchEntry{
			ID:          orch.ID,
			Name:        "beta",
			CoordRoleID: coord.ID,
			CoordTaskID: "t-beta",
			// CoordWorktreePath intentionally empty (cache miss)
		}
		cd, err := buildCoordDetails(ctx, d, oe)
		if err != nil {
			t.Fatalf("buildCoordDetails: %v", err)
		}
		if cd.CoordWorktreePath != "/wt/beta" {
			t.Errorf("CoordWorktreePath = %q, want /wt/beta (DB fallback)", cd.CoordWorktreePath)
		}
	})
}

// TestDetailsPane_OnlyInCoordinatorMode verifies the Details pane is composed
// into the body for a coordinator selection (root header and sub-coordinator)
// and absent for agent / freelancer selections.
func TestDetailsPane_OnlyInCoordinatorMode(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "alpha")
	coord, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "alpha-coord", Kind: db.KindCoordinator, ArgusProject: "repo-a",
	})
	worker, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "builder", Kind: db.KindWorker, ArgusProject: "repo-a",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coord.ID, ArgusTaskID: "t-coord", WorktreePath: "/c"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: worker.ID, ArgusTaskID: "t-w", WorktreePath: "/w"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	a.SetFocusMachine(NewFocusMachine())
	a.selectDebounce = 0

	if a.pieces.details == nil {
		t.Fatalf("layout must construct a details pane")
	}

	// Worker selection → agent mode → Details absent.
	if !a.pieces.rail.SelectByRoleID(worker.ID) {
		t.Fatalf("could not select worker row")
	}
	a.applyRailSelection(a.pieces.rail.CurrentRef())
	if !a.coordPresent || !a.agentPresent {
		t.Fatalf("worker selection should be agent mode (coord+agent); got coord=%v agent=%v", a.coordPresent, a.agentPresent)
	}
	if bodyHasItem(a, a.pieces.details) {
		t.Errorf("Details pane must NOT be composed in agent mode")
	}

	// Coordinator header selection → coord mode → Details present.
	a.pieces.rail.SelectByOrchID(orch.ID)
	a.applyRailSelection(a.pieces.rail.CurrentRef())
	if !a.coordPresent || a.agentPresent {
		t.Fatalf("header selection should be coordinator mode; got coord=%v agent=%v", a.coordPresent, a.agentPresent)
	}
	if !bodyHasItem(a, a.pieces.details) {
		t.Errorf("Details pane MUST be composed in coordinator mode")
	}
}

// TestDetailsPane_RendersCoordFields drives a Draw cycle with a coordinator
// selected and asserts the rendered screen surfaces the coordinator's metadata.
func TestDetailsPane_RendersCoordFields(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "alpha")
	coord, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "alpha-coord", Kind: db.KindCoordinator,
		ArgusProject: "repo-zed", Prompt: "shipthething",
	})
	worker, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "builderx", Kind: db.KindWorker, ArgusProject: "repo-zed",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coord.ID, ArgusTaskID: "t-coord", WorktreePath: "/c"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: worker.ID, ArgusTaskID: "t-w", WorktreePath: "/w"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	a.SetFocusMachine(NewFocusMachine())
	a.selectDebounce = 0

	a.pieces.rail.SelectByOrchID(orch.ID)
	a.applyRailSelection(a.pieces.rail.CurrentRef())

	got := renderApp(t, a, 140, 30)
	// "Worktree" label must appear: DB fallback populates /c from the coord binding.
	for _, want := range []string{"Details", "alpha", "Prompt", "shipthething", "repo-zed", "builderx", "Worktree"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered screen missing %q\n---\n%s", want, got)
		}
	}
}

// --- BUG-028: live coordinator must never render ✓/complete ---

// Spec (live-coord-never-complete): buildCoordDetails for a live coordinator
// whose argus task is "complete" must produce Status="in_progress" + CoordIdle=true
// so the Details pane renders "idle" and the idle moon glyph, not "complete"/✓.
func TestBuildCoordDetails_LiveCoordCompleteBecomesIdle(t *testing.T) {
	oe := &orchEntry{
		ID:            1,
		Name:          "hera-1.0-release",
		Archived:      false,
		CoordTaskID:   "t-coord",
		CoordHasState: true,
		CoordStatus:   "complete",
		CoordIdle:     false,
	}
	cd, err := buildCoordDetails(context.Background(), nil, oe)
	if err != nil {
		t.Fatalf("buildCoordDetails: %v", err)
	}
	if cd.Status == "complete" {
		t.Fatalf("live coord: Details Status = %q, want non-complete (idle masking)", cd.Status)
	}
	if cd.Status != "in_progress" || !cd.CoordIdle {
		t.Fatalf("live coord: Details Status=%q CoordIdle=%v, want Status=in_progress CoordIdle=true", cd.Status, cd.CoordIdle)
	}
	// statusLabel must report "idle", not "complete".
	lbl := statusLabel(cd.HasState, cd.NeedsInput, cd.Status, cd.CoordIdle)
	if lbl != "idle" {
		t.Fatalf("live coord statusLabel = %q, want %q", lbl, "idle")
	}
}

// TestDetailsPane_LiveRefreshOnRepopulate verifies BUG-037: after a DB change
// triggers a rail repopulate (without the operator moving the cursor), the
// Details pane's roster reflects the new agents — no leave-and-return required.
func TestDetailsPane_LiveRefreshOnRepopulate(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "alpha")
	coord, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "alpha-coord", Kind: db.KindCoordinator, ArgusProject: "repo-a",
	})
	w1, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "first-worker", Kind: db.KindWorker, ArgusProject: "repo-a",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coord.ID, ArgusTaskID: "t-coord", WorktreePath: "/c"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w1.ID, ArgusTaskID: "t-w1", WorktreePath: "/w1"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	a.SetFocusMachine(NewFocusMachine())
	a.selectDebounce = 0

	// Select the coordinator header — puts Details pane into coordinator mode.
	a.pieces.rail.SelectByOrchID(orch.ID)
	a.applyRailSelection(a.pieces.rail.CurrentRef())

	if a.pieces.details == nil {
		t.Fatalf("layout must construct a details pane")
	}
	if a.pieces.details.data.Name != "alpha" {
		t.Fatalf("Details Name = %q, want alpha", a.pieces.details.data.Name)
	}
	if len(a.pieces.details.data.Roster) != 1 {
		t.Fatalf("Details Roster len = %d before repopulate, want 1", len(a.pieces.details.data.Roster))
	}

	// Simulate a worker joining while the Details pane is open: insert a
	// second worker into the DB (as the hera_join path would), then call
	// populateRail directly (as the RailRefresher would via RepopulateRail).
	// The selection does NOT change — the cursor stays on the coordinator header.
	w2, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "second-worker", Kind: db.KindWorker, ArgusProject: "repo-a",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w2.ID, ArgusTaskID: "t-w2", WorktreePath: "/w2"})

	if err := a.populateRail(d); err != nil {
		t.Fatalf("populateRail: %v", err)
	}

	// BUG-037: Details pane must now show both workers without the operator
	// leaving and re-entering the coordinator view.
	if len(a.pieces.details.data.Roster) != 2 {
		names := make([]string, len(a.pieces.details.data.Roster))
		for i, r := range a.pieces.details.data.Roster {
			names[i] = r.Name
		}
		t.Errorf("Details Roster len = %d after repopulate, want 2; roster: %v", len(a.pieces.details.data.Roster), names)
	}
}

// infoStatePaneSource extends statePaneSource with TaskInfoProvider so tests
// can drive both state and full task info (name + worktree) via the state cache.
type infoStatePaneSource struct {
	statePaneSource
	infos map[string]ArgusTaskInfo
}

func (s *infoStatePaneSource) TaskInfo(taskID string) (ArgusTaskInfo, bool) {
	info, ok := s.infos[taskID]
	return info, ok
}

// TestDetailsPane_RendersAgentNameAndWorktreeFromCache exercises the full path
// from the state cache (TaskInfoProvider) through populateRail → orchEntry →
// buildCoordDetails → Details pane rendering. When the cache has name and
// worktree for the coord task, they must appear in the rendered output.
func TestDetailsPane_RendersAgentNameAndWorktreeFromCache(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "gamma")
	coord, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "gamma-coord", Kind: db.KindCoordinator, ArgusProject: "repo-g",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: coord.ID, ArgusTaskID: "t-gamma", WorktreePath: "/old/path",
	})

	src := &infoStatePaneSource{
		statePaneSource: statePaneSource{
			states: map[string]ArgusTaskState{
				"t-gamma": {Status: "in_progress"},
			},
		},
		infos: map[string]ArgusTaskInfo{
			"t-gamma": {
				ID:           "t-gamma",
				Name:         "gamma-task-name",
				Project:      "repo-g",
				WorktreePath: "/cache/worktrees/gamma-task-name",
				State:        ArgusTaskState{Status: "in_progress"},
			},
		},
	}

	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	a.SetFocusMachine(NewFocusMachine())
	a.selectDebounce = 0

	a.pieces.rail.SelectByOrchID(orch.ID)
	a.applyRailSelection(a.pieces.rail.CurrentRef())

	// Verify the orchEntry was populated from the cache.
	oe := findOrchEntry(a.pieces.rail, "gamma")
	if oe == nil {
		t.Fatalf("orchEntry for gamma not found")
	}
	if oe.CoordArgusName != "gamma-task-name" {
		t.Errorf("orchEntry.CoordArgusName = %q, want gamma-task-name", oe.CoordArgusName)
	}
	if oe.CoordWorktreePath != "/cache/worktrees/gamma-task-name" {
		t.Errorf("orchEntry.CoordWorktreePath = %q, want /cache/worktrees/gamma-task-name", oe.CoordWorktreePath)
	}

	// Verify they render in the Details pane screen output.
	got := renderApp(t, a, 140, 30)
	for _, want := range []string{"Agent", "gamma-task-name", "Worktree"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered screen missing %q\n---\n%s", want, got)
		}
	}
}

// Spec (live-coord-never-complete): archived coordinator with complete argus
// task is NOT masked — buildCoordDetails must keep Status="complete" so the
// Details pane for an archived coord still shows ✓.
func TestBuildCoordDetails_ArchivedCoordCompletionNotMasked(t *testing.T) {
	oe := &orchEntry{
		ID:            2,
		Name:          "old-release",
		Archived:      true,
		CoordTaskID:   "t-old",
		CoordHasState: true,
		CoordStatus:   "complete",
	}
	cd, err := buildCoordDetails(context.Background(), nil, oe)
	if err != nil {
		t.Fatalf("buildCoordDetails: %v", err)
	}
	if cd.Status != "complete" {
		t.Fatalf("archived coord: Details Status = %q, want complete (no masking)", cd.Status)
	}
}
