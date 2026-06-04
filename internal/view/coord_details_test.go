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
// (carrying mission/constraints/project) and two workers across two repos, then
// asserts buildCoordDetails derives every available field.
func TestBuildCoordDetails_Fields(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	before := time.Now().Add(-time.Second)

	orch, _ := d.Orchestrators.Create(ctx, "alpha")
	coord, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "alpha-coord", Kind: db.KindCoordinator,
		ArgusProject: "repo-a", Mission: "ship the thing", Constraints: "no force-push",
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
	if cd.Mission != "ship the thing" {
		t.Errorf("Mission = %q, want %q", cd.Mission, "ship the thing")
	}
	if cd.Constraints != "no force-push" {
		t.Errorf("Constraints = %q, want %q", cd.Constraints, "no force-push")
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
		ArgusProject: "repo-zed", Mission: "shipthething", Constraints: "noforce",
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
	for _, want := range []string{"Details", "alpha", "Mission", "shipthething", "repo-zed", "builderx"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered screen missing %q\n---\n%s", want, got)
		}
	}
}
