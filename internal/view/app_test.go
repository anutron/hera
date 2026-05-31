package view

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/anutron/hera/internal/db"
)

// openTestDB mirrors internal/db's test helper: a fresh on-disk SQLite
// in t.TempDir with all migrations applied.
func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.sqlite")
	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// fakePaneSource is a no-op PaneSource used in layout tests. It records
// the taskIDs it was asked about so we can assert wiring.
type fakePaneSource struct {
	calls []string
	// snapshots, if set, supplies the bytes returned per taskID.
	snapshots map[string][]byte
	// channels, if set, supplies the byte channel returned per taskID.
	channels map[string]chan []byte
	// sizes, if set, supplies the (cols, rows) returned per taskID.
	sizes map[string][2]int
	// resizes records every ResizeTask call in order.
	resizes []paneResizeCall
}

type paneResizeCall struct {
	TaskID string
	Cols   int
	Rows   int
}

func (f *fakePaneSource) SubscribeTask(taskID string) ([]byte, <-chan []byte, func()) {
	f.calls = append(f.calls, taskID)
	snap := []byte(nil)
	if f.snapshots != nil {
		snap = f.snapshots[taskID]
	}
	var ch <-chan []byte
	if f.channels != nil {
		if c, ok := f.channels[taskID]; ok {
			ch = c
		}
	}
	return snap, ch, func() {}
}

func (f *fakePaneSource) TaskSize(taskID string) (int, int) {
	if f.sizes == nil {
		return 0, 0
	}
	s, ok := f.sizes[taskID]
	if !ok {
		return 0, 0
	}
	return s[0], s[1]
}

func (f *fakePaneSource) ResizeTask(taskID string, cols, rows int) {
	f.resizes = append(f.resizes, paneResizeCall{TaskID: taskID, Cols: cols, Rows: rows})
}

// renderApp drives one Draw cycle on the App's root primitive against a
// SimulationScreen of the requested size and returns the full screen
// contents.
func renderApp(t *testing.T, a *App, w, h int) string {
	t.Helper()
	sim := tcell.NewSimulationScreen("")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	sim.SetSize(w, h)
	root := a.pieces.root
	root.SetRect(0, 0, w, h)
	root.Draw(sim)
	sim.Show()
	return readScreen(sim)
}

func TestBuildApp_RejectsNilDB(t *testing.T) {
	if _, err := BuildApp(nil, nil); err == nil {
		t.Fatal("expected error for nil db")
	}
}

func TestBuildApp_TopBarShowsHERA(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	got := renderApp(t, a, 80, 24)
	firstRow := strings.SplitN(got, "\n", 2)[0]
	if !strings.Contains(firstRow, "HERA") {
		t.Fatalf("expected 'HERA' on row 0; got %q", firstRow)
	}
}

func TestBuildApp_ThreeColumnsAndChrome(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// At 80x24 the rail is width 22 and the two panes split the
	// remaining 58 columns. Top bar = row 0; bottom bar = row 23.
	got := renderApp(t, a, 80, 24)
	rows := strings.Split(got, "\n")
	if len(rows) < 24 {
		t.Fatalf("expected at least 24 rows; got %d", len(rows))
	}

	// Top bar should NOT contain rail/pane border characters; it's the
	// plain text "HERA".
	if strings.ContainsAny(rows[0][:RailWidth], "│┃") {
		t.Errorf("top bar row should not include vertical border within rail column; row 0 = %q", rows[0])
	}

	// The rail should render its own bordered box; row 1 (just below
	// the top bar) should show the rail border or title.
	railSlice := rows[1][:RailWidth]
	if !strings.ContainsAny(railSlice, "─┌┐└┘─Rail") {
		t.Errorf("expected rail border characters on row 1 within rail column; got %q", railSlice)
	}

	// The bottom bar should show the RAIL hint placeholder.
	if !strings.Contains(rows[23], "[RAIL]") {
		t.Errorf("expected RAIL hint on bottom bar; got %q", rows[23])
	}
}

func TestBuildApp_RailHeaderRendered(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	got := renderApp(t, a, 80, 24)
	if !strings.Contains(got, "Rail") {
		t.Fatalf("expected rail title 'Rail' visible in border; got:\n%s", got)
	}
}

func TestBuildApp_EmptyDBShowsNoProjectsAndPlaceholders(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	got := renderApp(t, a, 80, 24)
	if !strings.Contains(got, "(no projects)") {
		t.Fatalf("expected '(no projects)' placeholder in rail; got:\n%s", got)
	}
	if !strings.Contains(got, "(no coord selected)") {
		t.Fatalf("expected coord pane placeholder; got:\n%s", got)
	}
	if !strings.Contains(got, "(no agent selected)") {
		t.Fatalf("expected agent pane placeholder; got:\n%s", got)
	}
}

func TestBuildApp_PopulatesRailFromDAOs(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, err := d.Orchestrators.Create(ctx, "foo")
	if err != nil {
		t.Fatalf("Create orchestrator: %v", err)
	}
	if _, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID,
		Name:           "coord",
		Kind:           db.KindCoordinator,
		ArgusProject:   "foo",
	}); err != nil {
		t.Fatalf("Create coord role: %v", err)
	}
	if _, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID,
		Name:           "worker-1",
		Kind:           db.KindWorker,
		ArgusProject:   "foo",
	}); err != nil {
		t.Fatalf("Create worker role: %v", err)
	}

	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	got := renderApp(t, a, 80, 24)
	if !strings.Contains(got, "foo") {
		t.Fatalf("expected orchestrator name 'foo' in rail; got:\n%s", got)
	}
	if !strings.Contains(got, "worker-1") {
		t.Fatalf("expected role 'worker-1' in rail; got:\n%s", got)
	}
	// Coord rows are no longer rendered in the rail — the orchestrator
	// header owns the coord task implicitly. Inspect the widget rather
	// than the rendered text (the COORD pane title also contains
	// "Coord" so a full-screen substring match would be ambiguous).
	for _, o := range a.pieces.rail.orchestrators {
		for _, r := range o.Roles {
			if r.RoleKind == string(db.KindCoordinator) {
				t.Errorf("rail Roles must not include coord rows; found %+v", r)
			}
		}
	}
}

// statePaneSource is a fakePaneSource extended with TaskStateProvider so
// rail-rendering tests can drive argus task state (status/idle/needs-input/
// archived).
type statePaneSource struct {
	fakePaneSource
	states map[string]ArgusTaskState
}

func (s *statePaneSource) TaskState(taskID string) (ArgusTaskState, bool) {
	st, ok := s.states[taskID]
	return st, ok
}

// TestBuildApp_RailRowReflectsArgusState proves a row picks up the argus-
// reported state for its bound task (status drives the icon, not hera's
// binding presence). A live worker bound to a task argus reports as
// "complete" must render as done, not idle.
func TestBuildApp_RailRowReflectsArgusState(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, err := d.Orchestrators.Create(ctx, "foo")
	if err != nil {
		t.Fatalf("orch: %v", err)
	}
	w, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "foo",
	})
	if err != nil {
		t.Fatalf("role: %v", err)
	}
	if _, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: w.ID, ArgusTaskID: "wtask", WorktreePath: "/w",
	}); err != nil {
		t.Fatalf("binding: %v", err)
	}

	src := &statePaneSource{states: map[string]ArgusTaskState{
		"wtask": {Status: "complete"},
	}}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	var got *roleEntry
	for _, o := range a.pieces.rail.orchestrators {
		for _, r := range o.Roles {
			if r.RoleID == w.ID {
				got = r
			}
		}
	}
	if got == nil {
		t.Fatalf("worker role missing from rail")
	}
	if !got.HasState || got.Status != "complete" {
		t.Errorf("row did not reflect argus state: HasState=%v Status=%q, want true/\"complete\"", got.HasState, got.Status)
	}
}

// TestBuildApp_ArgusArchivedRoleHiddenFromActiveRail proves a role whose
// argus task is archived is kept out of the active rail (matching argus's
// non-archived set), even though hera itself hasn't archived the role.
func TestBuildApp_ArgusArchivedRoleHiddenFromActiveRail(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, err := d.Orchestrators.Create(ctx, "foo")
	if err != nil {
		t.Fatalf("orch: %v", err)
	}
	w, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "foo",
	})
	if err != nil {
		t.Fatalf("role: %v", err)
	}
	if _, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: w.ID, ArgusTaskID: "wtask", WorktreePath: "/w",
	}); err != nil {
		t.Fatalf("binding: %v", err)
	}

	src := &statePaneSource{states: map[string]ArgusTaskState{
		"wtask": {Status: "complete", Archived: true},
	}}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	for _, row := range a.pieces.rail.rows {
		if row.kind == railRowRole && row.role != nil && row.role.RoleID == w.ID {
			t.Errorf("argus-archived role must be hidden from the active rail by default, but it rendered as a visible row")
		}
	}
}

// TestBuildApp_GoneTaskRoleHiddenFromActiveRail proves a role whose argus
// task no longer exists (cache miss, once the cache is warm) is dropped from
// the active rail — argus doesn't show deleted tasks, so neither should hera.
func TestBuildApp_GoneTaskRoleHiddenFromActiveRail(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, err := d.Orchestrators.Create(ctx, "foo")
	if err != nil {
		t.Fatalf("orch: %v", err)
	}
	w, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "foo",
	})
	if err != nil {
		t.Fatalf("role: %v", err)
	}
	if _, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: w.ID, ArgusTaskID: "gonetask", WorktreePath: "/w",
	}); err != nil {
		t.Fatalf("binding: %v", err)
	}

	// Provider with no entry for "gonetask" → argus doesn't know it (gone).
	// statePaneSource doesn't implement StatesReady, so applyArgusState
	// treats it as ready → marks the role dead → hidden.
	src := &statePaneSource{states: map[string]ArgusTaskState{}}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	for _, row := range a.pieces.rail.rows {
		if row.kind == railRowRole && row.role != nil && row.role.RoleID == w.ID {
			t.Errorf("role bound to a gone argus task must be hidden from the active rail, but it rendered as a visible row")
		}
	}
}

// TestBuildApp_DoneRoleSurfacesLastBindingTaskID proves that a role whose
// binding has ended (the agent finished / its argus task completed) still
// carries its most-recent binding's argus task id on the rail row. Without
// this the row's ArgusTaskID is empty and applyRailSelection never rebinds
// the AGENT pane off it — the "right pane never changes / panes stuck on the
// last live agent" bug. With it, selecting a done agent shows its last
// output read-only (argus serves /api/tasks/{id}/output for completed tasks).
func TestBuildApp_DoneRoleSurfacesLastBindingTaskID(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, err := d.Orchestrators.Create(ctx, "foo")
	if err != nil {
		t.Fatalf("Create orchestrator: %v", err)
	}
	w, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "worker-done", Kind: db.KindWorker, ArgusProject: "foo",
	})
	if err != nil {
		t.Fatalf("Create worker role: %v", err)
	}
	bnd, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: w.ID, ArgusTaskID: "done-task-id", WorktreePath: "/x",
	})
	if err != nil {
		t.Fatalf("Create binding: %v", err)
	}
	// The agent finished: end the binding so no live binding remains.
	if err := d.Bindings.End(ctx, bnd.ID, "completed"); err != nil {
		t.Fatalf("End binding: %v", err)
	}

	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	var found *roleEntry
	for _, o := range a.pieces.rail.orchestrators {
		for _, r := range o.Roles {
			if r.RoleID == w.ID {
				found = r
			}
		}
	}
	if found == nil {
		t.Fatalf("worker-done role missing from rail; orchestrators=%+v", a.pieces.rail.orchestrators)
	}
	if found.Live {
		t.Errorf("expected role non-live (binding ended), got Live=true")
	}
	if found.ArgusTaskID != "done-task-id" {
		t.Errorf("done role ArgusTaskID = %q, want \"done-task-id\" (most-recent binding) — pane can't follow selection without it", found.ArgusTaskID)
	}
}

func TestBuildApp_LiveBindingMarksRoleAndSubscribesPanes(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, err := d.Orchestrators.Create(ctx, "proj1")
	if err != nil {
		t.Fatalf("Create orchestrator: %v", err)
	}
	coordRole, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID,
		Name:           "coord",
		Kind:           db.KindCoordinator,
		ArgusProject:   "proj1",
	})
	if err != nil {
		t.Fatalf("Create coord role: %v", err)
	}
	workerRole, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID,
		Name:           "w1",
		Kind:           db.KindWorker,
		ArgusProject:   "proj1",
	})
	if err != nil {
		t.Fatalf("Create worker role: %v", err)
	}
	if _, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID:       coordRole.ID,
		ArgusTaskID:  "coord-task-id",
		WorktreePath: "/tmp/proj1-coord",
	}); err != nil {
		t.Fatalf("Create coord binding: %v", err)
	}
	if _, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID:       workerRole.ID,
		ArgusTaskID:  "worker-task-id",
		WorktreePath: "/tmp/proj1-w1",
	}); err != nil {
		t.Fatalf("Create worker binding: %v", err)
	}

	src := &fakePaneSource{
		snapshots: map[string][]byte{
			"coord-task-id":  []byte("coord-snapshot\n"),
			"worker-task-id": []byte("worker-snapshot\n"),
		},
	}

	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Both bindings should have been subscribed to via the proxy fake.
	gotCalls := map[string]int{}
	for _, c := range src.calls {
		gotCalls[c]++
	}
	if gotCalls["coord-task-id"] == 0 {
		t.Errorf("expected SubscribeTask call for coord task; calls=%v", src.calls)
	}
	if gotCalls["worker-task-id"] == 0 {
		t.Errorf("expected SubscribeTask call for worker task; calls=%v", src.calls)
	}

	got := renderApp(t, a, 80, 24)
	if !strings.Contains(got, "coord-snapshot") {
		t.Fatalf("expected coord snapshot rendered in coord pane; got:\n%s", got)
	}
	if !strings.Contains(got, "worker-snapshot") {
		t.Fatalf("expected worker snapshot rendered in agent pane; got:\n%s", got)
	}

	// Coord rows are now implicit per orchestrator — they don't appear in
	// the rail's role list. The orchestrator's CoordTaskID should carry
	// the coord binding's argus task ID, and the worker role should be
	// the only role in the slice and marked Live.
	gotLive := map[string]bool{}
	for _, o := range a.pieces.rail.orchestrators {
		if o.ID == orch.ID && o.CoordTaskID != "coord-task-id" {
			t.Errorf("expected orchestrator CoordTaskID=coord-task-id; got %q", o.CoordTaskID)
		}
		for _, r := range o.Roles {
			if r.RoleKind == string(db.KindCoordinator) {
				t.Errorf("rail Roles slice must not contain coord rows; found %+v", r)
			}
			if r.Live {
				gotLive[r.Name] = true
			}
		}
	}
	if !gotLive["w1"] {
		t.Errorf("expected live worker role; rail orchestrators=%+v", a.pieces.rail.orchestrators)
	}
}

func TestBuildApp_NoLiveAgentLeavesPlaceholders(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, err := d.Orchestrators.Create(ctx, "proj")
	if err != nil {
		t.Fatalf("Create orchestrator: %v", err)
	}
	if _, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID,
		Name:           "coord",
		Kind:           db.KindCoordinator,
		ArgusProject:   "proj",
	}); err != nil {
		t.Fatalf("Create coord role: %v", err)
	}
	// No bindings — should leave panes with placeholders.

	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	if len(src.calls) != 0 {
		t.Errorf("expected zero SubscribeTask calls (no live bindings); got %v", src.calls)
	}
	got := renderApp(t, a, 80, 24)
	if !strings.Contains(got, "(no coord selected)") {
		t.Fatalf("expected coord pane placeholder; got:\n%s", got)
	}
}

// alivePaneSource is a fakePaneSource extended with TaskAliveChecker so
// findInitialSelection can filter recently-completed tasks.
type alivePaneSource struct {
	fakePaneSource
	alive map[string]bool // taskID → alive?
}

func (a *alivePaneSource) IsTaskAlive(taskID string) bool {
	if a.alive == nil {
		return true
	}
	return a.alive[taskID]
}

func TestFindInitialSelection_PrefersLiveWorker(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	deadRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w-dead", Kind: db.KindWorker, ArgusProject: "proj",
	})
	liveRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w-live", Kind: db.KindWorker, ArgusProject: "proj",
	})
	coordRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "proj",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: deadRole.ID, ArgusTaskID: "task-dead", WorktreePath: "/x"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: liveRole.ID, ArgusTaskID: "task-live", WorktreePath: "/y"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coordRole.ID, ArgusTaskID: "task-coord", WorktreePath: "/c"})

	src := &alivePaneSource{alive: map[string]bool{
		"task-dead":  false,
		"task-live":  true,
		"task-coord": true,
	}}

	coord, agent := findInitialSelection(d, src)
	if agent != "task-live" {
		t.Fatalf("agent: want task-live (the only alive worker), got %q", agent)
	}
	if coord != "task-coord" {
		t.Fatalf("coord: want task-coord, got %q", coord)
	}
}

func TestFindInitialSelection_FallsBackToCoordWhenNoLiveWorker(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	workerRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "proj",
	})
	coordRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "proj",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: workerRole.ID, ArgusTaskID: "task-w", WorktreePath: "/w"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coordRole.ID, ArgusTaskID: "task-c", WorktreePath: "/c"})

	src := &alivePaneSource{alive: map[string]bool{
		"task-w": false,
		"task-c": true,
	}}

	coord, agent := findInitialSelection(d, src)
	if coord != "task-c" {
		t.Fatalf("coord: want task-c, got %q", coord)
	}
	if agent != "task-c" {
		t.Fatalf("agent fallback: want task-c (coord task used for agent pane when no live worker), got %q", agent)
	}
}

func TestFindInitialSelection_NothingLiveReturnsEmpty(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	workerRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "proj",
	})
	coordRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "proj",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: workerRole.ID, ArgusTaskID: "task-w", WorktreePath: "/w"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coordRole.ID, ArgusTaskID: "task-c", WorktreePath: "/c"})

	src := &alivePaneSource{alive: map[string]bool{
		"task-w": false,
		"task-c": false,
	}}

	coord, agent := findInitialSelection(d, src)
	if coord != "" || agent != "" {
		t.Fatalf("everything dead must return empty pair; got (%q, %q)", coord, agent)
	}
}

func TestFindInitialSelection_NoCheckerKeepsLegacyBehavior(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	w, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "proj"})
	c, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w.ID, ArgusTaskID: "task-w", WorktreePath: "/w"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: c.ID, ArgusTaskID: "task-c", WorktreePath: "/c"})

	// fakePaneSource does NOT implement TaskAliveChecker; selection should
	// treat the worker binding as live and pick it.
	src := &fakePaneSource{}
	coord, agent := findInitialSelection(d, src)
	if coord != "task-c" || agent != "task-w" {
		t.Fatalf("no checker should mirror DB-only behavior; got (%q, %q), want (task-c, task-w)", coord, agent)
	}
}

func TestFindInitialSelection_PrefersSecondOrchestratorWithLiveWorker(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	o1, _ := d.Orchestrators.Create(ctx, "first")
	o2, _ := d.Orchestrators.Create(ctx, "second")
	w1, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: o1.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "first"})
	w2, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: o2.ID, Name: "w2", Kind: db.KindWorker, ArgusProject: "second"})
	c2, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: o2.ID, Name: "coord2", Kind: db.KindCoordinator, ArgusProject: "second"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w1.ID, ArgusTaskID: "t1", WorktreePath: "/1"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w2.ID, ArgusTaskID: "t2", WorktreePath: "/2"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: c2.ID, ArgusTaskID: "t2c", WorktreePath: "/2c"})

	// Only second orchestrator's worker is alive.
	src := &alivePaneSource{alive: map[string]bool{
		"t1": false,
		"t2": true,
	}}

	coord, agent := findInitialSelection(d, src)
	if agent != "t2" {
		t.Fatalf("must prefer second orchestrator's live worker; got agent=%q", agent)
	}
	if coord != "t2c" {
		t.Fatalf("coord should come from second orchestrator; got %q", coord)
	}
}

func TestApp_OnRailSelectEnter_WorkerRebindsAgentAndJumps(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	w1, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "proj"})
	w2, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "w2", Kind: db.KindWorker, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w1.ID, ArgusTaskID: "t1", WorktreePath: "/1"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w2.ID, ArgusTaskID: "t2", WorktreePath: "/2"})

	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// findInitialSelection should have picked w1 (first worker). Move the
	// rail's cursor to w2 and fire Enter.
	startAgent := a.AgentTaskID()
	if startAgent != "t1" {
		t.Fatalf("baseline AgentTaskID: want t1, got %q", startAgent)
	}

	if !a.pieces.rail.SelectByRoleID(w2.ID) {
		t.Fatalf("could not locate w2 role row in rail")
	}

	// Enter binds the agent to w2's task AND jumps into the AGENT pane so the
	// operator can type — the only reliable way in when argus eats the
	// Cmd/Ctrl-arrow focus ladder. (Browsing is j/k, which rebinds live.)
	got := a.OnRailSelectEnter()
	if got != FocusAGENT {
		t.Fatalf("Enter on worker row: want FocusAGENT (jump into PTY), got %s", got)
	}
	if a.AgentTaskID() != "t2" {
		t.Fatalf("agent task after rebind: want t2, got %q", a.AgentTaskID())
	}
}

func TestApp_OnRailSelectEnter_OrchHeaderEntersHERA(t *testing.T) {
	// An orchestrator header IS a coordinator selection (D13). Per the
	// "Enter enters the selection's primary pane" requirement, Enter on a
	// coordinator row enters its HERA (COORD) pane — it does NOT fold (space
	// folds; only Freelance / Archive expando headers fold on Enter). The
	// HERA pane binds to the orchestrator's coord task.
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	w, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "proj"})
	c, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w.ID, ArgusTaskID: "tw", WorktreePath: "/w"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: c.ID, ArgusTaskID: "tc", WorktreePath: "/c"})

	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	a.SetFocusMachine(NewFocusMachine())
	a.selectDebounce = 0

	// Coord rows should not be selectable in the rail at all.
	if a.pieces.rail.SelectByRoleID(c.ID) {
		t.Fatalf("coord role row must not be selectable in rail")
	}

	if !a.pieces.rail.SelectByOrchID(orch.ID) {
		t.Fatalf("could not locate orchestrator header row")
	}
	if got := a.OnRailSelectEnter(); got != FocusCOORD {
		t.Fatalf("Enter on coordinator header: want FocusCOORD (enter HERA pane), got %s", got)
	}
	if a.CoordTaskID() != "tc" {
		t.Fatalf("coord task should be tc after header Enter; got %q", a.CoordTaskID())
	}
}

// TestPopulateRail_FiltersCoordRows confirms that coord roles never
// appear as their own rail rows; instead the orchestrator entry's
// CoordTaskID holds the coord binding so the COORD pane can rebind
// implicitly when an agent / header is selected.
func TestPopulateRail_FiltersCoordRows(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	coordRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "proj",
	})
	workerRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "proj",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coordRole.ID, ArgusTaskID: "tc", WorktreePath: "/c"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: workerRole.ID, ArgusTaskID: "tw", WorktreePath: "/w"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	var got *orchEntry
	for _, o := range a.pieces.rail.orchestrators {
		if o.ID == orch.ID {
			got = o
			break
		}
	}
	if got == nil {
		t.Fatalf("orchestrator %d missing from rail data", orch.ID)
	}
	if got.CoordTaskID != "tc" {
		t.Fatalf("CoordTaskID: want tc, got %q", got.CoordTaskID)
	}
	if len(got.Roles) != 1 || got.Roles[0].RoleID != workerRole.ID {
		t.Fatalf("Roles must contain only the worker; got %+v", got.Roles)
	}
}

// TestPopulateRail_DeadBindingsHiddenByDefault confirms that when the
// PaneSource exposes TaskAliveChecker and reports a worker binding's
// task as gone, the role row is filtered out of the rail. With
// showArchived=true the row reappears (marked Dead) so the operator
// can still see the tombstone.
func TestPopulateRail_DeadBindingsHiddenByDefault(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	liveWorker, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "alive", Kind: db.KindWorker, ArgusProject: "proj",
	})
	deadWorker, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "tombstone", Kind: db.KindWorker, ArgusProject: "proj",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: liveWorker.ID, ArgusTaskID: "t-alive", WorktreePath: "/a"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: deadWorker.ID, ArgusTaskID: "t-dead", WorktreePath: "/d"})

	src := &alivePaneSource{alive: map[string]bool{
		"t-alive": true,
		"t-dead":  false,
	}}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Dead role should still be in the orchestrator's Roles slice (so
	// the operator can resurface it via `l`), but with Dead=true.
	var deadEntry *roleEntry
	for _, o := range a.pieces.rail.orchestrators {
		for _, r := range o.Roles {
			if r.RoleID == deadWorker.ID {
				deadEntry = r
			}
		}
	}
	if deadEntry == nil {
		t.Fatalf("dead worker role missing from rail data")
	}
	if !deadEntry.Dead {
		t.Fatalf("expected Dead=true on dead worker; got %+v", deadEntry)
	}

	// Render: dead row should NOT appear with showArchived=false.
	got := renderApp(t, a, 80, 24)
	if !strings.Contains(got, "alive") {
		t.Fatalf("alive worker should appear in rail; got:\n%s", got)
	}
	if strings.Contains(got, "tombstone") {
		t.Fatalf("dead worker must be hidden when showArchived=false; got:\n%s", got)
	}

	// Flip showArchived; the dead row should now render.
	a.showArchived = true
	if err := a.populateRail(d); err != nil {
		t.Fatalf("populateRail: %v", err)
	}
	got = renderApp(t, a, 80, 24)
	if !strings.Contains(got, "tombstone") {
		t.Fatalf("dead worker should appear when showArchived=true; got:\n%s", got)
	}
}

// TestPopulateRail_DeadCoordSkippedFromCoordTaskID confirms that a
// coord binding whose argus task is gone does NOT populate the
// orchestrator's CoordTaskID — otherwise the COORD pane would bind to
// a tombstone and render placeholder forever.
func TestPopulateRail_DeadCoordSkippedFromCoordTaskID(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	coordRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "proj",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coordRole.ID, ArgusTaskID: "t-dead-coord", WorktreePath: "/c"})

	src := &alivePaneSource{alive: map[string]bool{"t-dead-coord": false}}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	for _, o := range a.pieces.rail.orchestrators {
		if o.ID == orch.ID && o.CoordTaskID != "" {
			t.Fatalf("dead coord must not populate CoordTaskID; got %q", o.CoordTaskID)
		}
	}
}

// TestApp_OnRailSelectionChanged_RebindsBothPanes confirms that moving
// the rail cursor onto an agent row drives a rebind of both COORD (to
// the orchestrator's coord task) and AGENT (to the agent's task)
// without requiring Enter.
func TestApp_OnRailSelectionChanged_RebindsBothPanes(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	coordRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "proj",
	})
	w1, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "proj",
	})
	w2, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w2", Kind: db.KindWorker, ArgusProject: "proj",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coordRole.ID, ArgusTaskID: "tc", WorktreePath: "/c"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w1.ID, ArgusTaskID: "t1", WorktreePath: "/1"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w2.ID, ArgusTaskID: "t2", WorktreePath: "/2"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	// Drive rebinds synchronously so the test doesn't depend on a wall
	// clock; the production debounce window is 120ms.
	a.selectDebounce = 0

	// Sanity: findInitialSelection picked w1.
	if a.AgentTaskID() != "t1" {
		t.Fatalf("baseline AgentTaskID: want t1, got %q", a.AgentTaskID())
	}

	// SelectByRoleID fires the selection-changed callback synchronously
	// (since selectDebounce is 0), which should rebind both panes.
	if !a.pieces.rail.SelectByRoleID(w2.ID) {
		t.Fatalf("could not locate w2 in rail")
	}
	if a.AgentTaskID() != "t2" {
		t.Fatalf("agent rebind on selection change: want t2, got %q", a.AgentTaskID())
	}
	if a.CoordTaskID() != "tc" {
		t.Fatalf("coord rebind on selection change: want tc, got %q", a.CoordTaskID())
	}
}

// TestApp_OnRailSelectionChanged_DebounceCoalescesRapidMoves confirms
// that a burst of selection changes inside the debounce window
// coalesces into a single rebind on the final cursor position.
func TestApp_OnRailSelectionChanged_DebounceCoalescesRapidMoves(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	w1, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "proj",
	})
	w2, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w2", Kind: db.KindWorker, ArgusProject: "proj",
	})
	w3, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w3", Kind: db.KindWorker, ArgusProject: "proj",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w1.ID, ArgusTaskID: "t1", WorktreePath: "/1"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w2.ID, ArgusTaskID: "t2", WorktreePath: "/2"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w3.ID, ArgusTaskID: "t3", WorktreePath: "/3"})

	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	// Don't drive tApp.Run — the QueueUpdateDraw bounce won't fire. Skip
	// it and let the timer callback short-circuit to applyRailSelection
	// directly by clearing the app handle. (The handler checks a.app
	// != nil; nil means "fire inline".)
	a.app = nil
	a.selectDebounce = 80 * time.Millisecond

	// Burst of three selection changes inside one debounce window.
	a.pieces.rail.SelectByRoleID(w1.ID)
	a.pieces.rail.SelectByRoleID(w2.ID)
	a.pieces.rail.SelectByRoleID(w3.ID)

	// Wait past the debounce window plus slop. We expect exactly one
	// rebind, landing on w3.
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		if a.AgentTaskID() == "t3" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if a.AgentTaskID() != "t3" {
		t.Fatalf("debounce should land on final position w3 (t3); got %q", a.AgentTaskID())
	}

	// SubscribeTask call count is the cheapest proxy for "how many
	// rebinds happened." The initial BuildApp subscribed to t1 (the
	// pick from findInitialSelection); the debounce fires one more
	// rebind onto t3. (CoordTaskID stays "" since this orchestrator has
	// no coord role.)
	gotT3 := 0
	for _, c := range src.calls {
		if c == "t3" {
			gotT3++
		}
	}
	if gotT3 != 1 {
		t.Fatalf("expected exactly 1 SubscribeTask call for t3 after debounce; got %d (all calls=%v)", gotT3, src.calls)
	}
}

// TestApp_OnRailSelectionChanged_OrchHeaderKeepsAgent confirms that
// selecting an orchestrator header row rebinds only the COORD pane
// (per the documented selection semantics — the operator's last agent
// stays visible while they re-anchor the coord pane).
func TestApp_OnRailSelectionChanged_OrchHeaderKeepsAgent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	o1, _ := d.Orchestrators.Create(ctx, "first")
	c1, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: o1.ID, Name: "c", Kind: db.KindCoordinator, ArgusProject: "first"})
	w1, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: o1.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "first"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: c1.ID, ArgusTaskID: "tc1", WorktreePath: "/c1"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w1.ID, ArgusTaskID: "tw1", WorktreePath: "/w1"})

	o2, _ := d.Orchestrators.Create(ctx, "second")
	c2, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: o2.ID, Name: "c", Kind: db.KindCoordinator, ArgusProject: "second"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: c2.ID, ArgusTaskID: "tc2", WorktreePath: "/c2"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	a.selectDebounce = 0

	// Baseline: first orchestrator's coord + worker bindings drove
	// initial selection.
	if a.CoordTaskID() != "tc1" || a.AgentTaskID() != "tw1" {
		t.Fatalf("baseline coord/agent: want (tc1, tw1), got (%q, %q)", a.CoordTaskID(), a.AgentTaskID())
	}

	// Move cursor to the second orchestrator's header. Only COORD
	// should rebind; AGENT should remain on tw1.
	if !a.pieces.rail.SelectByOrchID(o2.ID) {
		t.Fatalf("could not locate o2 header")
	}
	if a.CoordTaskID() != "tc2" {
		t.Fatalf("header selection should rebind COORD to tc2; got %q", a.CoordTaskID())
	}
	if a.AgentTaskID() != "tw1" {
		t.Fatalf("header selection must NOT rebind AGENT; got %q (want tw1)", a.AgentTaskID())
	}
}

func TestBuildApp_NilSourceDoesNotPanic(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, _ := d.Orchestrators.Create(ctx, "p")
	role, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator,
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, ArgusTaskID: "t", WorktreePath: "",
	})

	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	_ = renderApp(t, a, 80, 24) // must not panic
}

// freelancePaneSource is a fakePaneSource that also implements
// TaskStateProvider and FreelanceProvider so rail tests can drive the
// Freelance section from a synthetic argus task list.
type freelancePaneSource struct {
	fakePaneSource
	states map[string]ArgusTaskState
	tasks  []ArgusTaskInfo
}

func (s *freelancePaneSource) TaskState(taskID string) (ArgusTaskState, bool) {
	st, ok := s.states[taskID]
	return st, ok
}
func (s *freelancePaneSource) LiveTasks() []ArgusTaskInfo { return s.tasks }

// TestBuildApp_FreelanceGroupsByProjectExcludesManagedAndArchived proves the
// Freelance section contains exactly the unmanaged, non-archived argus tasks
// grouped by project — a hera-bound task and an archived task are excluded.
func TestBuildApp_FreelanceGroupsByProjectExcludesManagedAndArchived(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, err := d.Orchestrators.Create(ctx, "managed")
	if err != nil {
		t.Fatalf("orch: %v", err)
	}
	w, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "Hera",
	})
	if err != nil {
		t.Fatalf("role: %v", err)
	}
	if _, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: w.ID, ArgusTaskID: "managed-1", WorktreePath: "/w",
	}); err != nil {
		t.Fatalf("binding: %v", err)
	}

	src := &freelancePaneSource{
		states: map[string]ArgusTaskState{},
		tasks: []ArgusTaskInfo{
			{ID: "managed-1", Name: "managed", Project: "Hera", State: ArgusTaskState{Status: "in_progress"}},
			{ID: "free-b1", Name: "beta-1", Project: "Beta", Elapsed: "5m", State: ArgusTaskState{Status: "in_progress"}},
			{ID: "free-b2", Name: "beta-2", Project: "Beta", State: ArgusTaskState{Status: "complete"}},
			{ID: "free-a1", Name: "alpha-1", Project: "Alpha", State: ArgusTaskState{Status: "in_progress", Idle: true}},
			{ID: "free-arch", Name: "archived-one", Project: "Alpha", State: ArgusTaskState{Status: "complete", Archived: true}},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	fl := a.pieces.rail.freelance
	if len(fl) != 2 {
		t.Fatalf("want 2 freelance projects (Alpha, Beta), got %d: %+v", len(fl), fl)
	}
	// Sorted by project name.
	if fl[0].Project != "Alpha" || fl[1].Project != "Beta" {
		t.Fatalf("freelance projects not sorted: got %q, %q", fl[0].Project, fl[1].Project)
	}
	// Alpha: only free-a1 (free-arch excluded; managed-1 excluded).
	if len(fl[0].Tasks) != 1 || fl[0].Tasks[0].ArgusTaskID != "free-a1" {
		t.Fatalf("Alpha tasks wrong: %+v", fl[0].Tasks)
	}
	// Beta: free-b1, free-b2.
	if len(fl[1].Tasks) != 2 {
		t.Fatalf("Beta should have 2 tasks, got %d", len(fl[1].Tasks))
	}
	if fl[1].Tasks[0].ElapsedOverride != "5m" {
		t.Errorf("freelance elapsed override not carried: got %q", fl[1].Tasks[0].ElapsedOverride)
	}
	if fl[1].Tasks[0].RoleKind != string(db.KindFreelance) {
		t.Errorf("freelance row kind = %q, want freelance", fl[1].Tasks[0].RoleKind)
	}

	// Rendered rail shows the Freelance separator and repo headers.
	out := renderApp(t, a, 100, 40)
	for _, want := range []string{"Freelance", "Alpha", "Beta"} {
		if !strings.Contains(out, want) {
			t.Errorf("rail render missing %q\n%s", want, out)
		}
	}
}

// TestBuildApp_SelectingFreelancerEntersFullWidthMode proves that selecting a
// freelance row removes the coord pane (CoordTaskID blank) and binds the
// agent to the freelancer; selecting a normal orchestrator restores coord.
func TestBuildApp_SelectingFreelancerEntersFullWidthMode(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, _ := d.Orchestrators.Create(ctx, "managed")
	c, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "Hera",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: c.ID, ArgusTaskID: "coord-1", WorktreePath: "/c",
	})

	src := &freelancePaneSource{
		states: map[string]ArgusTaskState{},
		tasks: []ArgusTaskInfo{
			{ID: "free-1", Name: "freelancer", Project: "Beta", State: ArgusTaskState{Status: "in_progress"}},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	a.selectDebounce = 0

	if len(a.pieces.rail.freelance) != 1 || len(a.pieces.rail.freelance[0].Tasks) != 1 {
		t.Fatalf("expected one freelancer; got %+v", a.pieces.rail.freelance)
	}
	freelancer := a.pieces.rail.freelance[0].Tasks[0]

	a.applyRailSelection(freelancer)
	if a.coordPresent {
		t.Fatalf("selecting freelancer should enter freelance mode (no coord pane)")
	}
	if !a.agentPresent {
		t.Fatalf("freelance mode must keep the agent pane present")
	}
	if a.AgentTaskID() != "free-1" {
		t.Fatalf("agent should bind to freelancer; got %q", a.AgentTaskID())
	}
	if a.CoordTaskID() != "" {
		t.Fatalf("freelance mode must blank the coord task; got %q", a.CoordTaskID())
	}

	// Selecting the managed orchestrator header switches to coordinator mode
	// (full-width HERA, no agent) and restores the coord binding.
	a.applyRailSelection(&orchEntry{ID: orch.ID, Name: "managed", CoordTaskID: "coord-1"})
	if !a.coordPresent {
		t.Fatalf("selecting a managed orchestrator should restore the coord pane")
	}
	if a.CoordTaskID() != "coord-1" {
		t.Fatalf("coord should rebind to coord-1 after exit; got %q", a.CoordTaskID())
	}
}

// bodyPaneCount reports how many panes (coord/agent terminal panes) are in
// the body Flex after the rail. The rail is always item 0, so the count of
// non-rail items is the number of present panes.
func bodyPaneCount(a *App) int {
	if a.pieces.body == nil {
		return 0
	}
	// body item 0 is the rail; the remaining items are panes.
	return a.pieces.body.GetItemCount() - 1
}

func bodyHasItem(a *App, p interface{ GetRect() (int, int, int, int) }) bool {
	body := a.pieces.body
	for i := 0; i < body.GetItemCount(); i++ {
		if body.GetItem(i) == p {
			return true
		}
	}
	return false
}

// TestBuildApp_CoordinatorSelectionIsFullWidthHERA proves the three-mode body:
// selecting a coordinator (orchestrator header) composes rail + a single
// full-width HERA pane bound to the coord task, with NO agent pane present.
func TestBuildApp_CoordinatorSelectionIsFullWidthHERA(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, _ := d.Orchestrators.Create(ctx, "proj")
	w, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "proj"})
	c, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w.ID, ArgusTaskID: "tw", WorktreePath: "/w"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: c.ID, ArgusTaskID: "tc", WorktreePath: "/c"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	a.SetFocusMachine(NewFocusMachine())
	a.selectDebounce = 0

	a.applyRailSelection(&orchEntry{ID: orch.ID, Name: "proj", CoordTaskID: "tc"})

	if !a.coordPresent || a.agentPresent {
		t.Fatalf("coordinator mode: want coordPresent=true agentPresent=false, got %v/%v", a.coordPresent, a.agentPresent)
	}
	if a.CoordTaskID() != "tc" {
		t.Fatalf("HERA pane should bind to coord task tc; got %q", a.CoordTaskID())
	}
	if got := bodyPaneCount(a); got != 1 {
		t.Fatalf("coordinator mode body must have exactly one pane (full-width HERA); got %d", got)
	}
	if bodyHasItem(a, a.pieces.agent) {
		t.Fatalf("coordinator mode must NOT compose the AGENT pane into the body")
	}
	if !bodyHasItem(a, a.pieces.coord) {
		t.Fatalf("coordinator mode must compose the HERA (coord) pane into the body")
	}
}

// TestBuildApp_AgentSelectionSplitsHERAAndAgent proves selecting a worker
// composes rail + HERA + AGENT (both panes present).
func TestBuildApp_AgentSelectionSplitsHERAAndAgent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, _ := d.Orchestrators.Create(ctx, "proj")
	w, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "proj"})
	c, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w.ID, ArgusTaskID: "tw", WorktreePath: "/w"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: c.ID, ArgusTaskID: "tc", WorktreePath: "/c"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	a.SetFocusMachine(NewFocusMachine())
	a.selectDebounce = 0

	if !a.pieces.rail.SelectByRoleID(w.ID) {
		t.Fatalf("could not select worker row")
	}
	a.applyRailSelection(a.pieces.rail.CurrentRef())

	if !a.coordPresent || !a.agentPresent {
		t.Fatalf("agent mode: want both panes present, got coord=%v agent=%v", a.coordPresent, a.agentPresent)
	}
	if got := bodyPaneCount(a); got != 2 {
		t.Fatalf("agent mode body must have two panes (HERA + AGENT); got %d", got)
	}
	if a.CoordTaskID() != "tc" || a.AgentTaskID() != "tw" {
		t.Fatalf("agent mode bindings: want coord=tc agent=tw, got %q/%q", a.CoordTaskID(), a.AgentTaskID())
	}
}

// TestBuildApp_FreelancerSelectionIsFullWidthAgent proves selecting a
// freelancer composes rail + a single full-width AGENT pane, no HERA pane.
func TestBuildApp_FreelancerSelectionIsFullWidthAgent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, _ := d.Orchestrators.Create(ctx, "managed")
	c, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "Hera"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: c.ID, ArgusTaskID: "coord-1", WorktreePath: "/c"})

	src := &freelancePaneSource{
		states: map[string]ArgusTaskState{},
		tasks: []ArgusTaskInfo{
			{ID: "free-1", Name: "freelancer", Project: "Beta", State: ArgusTaskState{Status: "in_progress"}},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	a.SetFocusMachine(NewFocusMachine())
	a.selectDebounce = 0

	freelancer := a.pieces.rail.freelance[0].Tasks[0]
	a.applyRailSelection(freelancer)

	if a.coordPresent || !a.agentPresent {
		t.Fatalf("freelance mode: want coordPresent=false agentPresent=true, got %v/%v", a.coordPresent, a.agentPresent)
	}
	if got := bodyPaneCount(a); got != 1 {
		t.Fatalf("freelance mode body must have one pane (full-width AGENT); got %d", got)
	}
	if bodyHasItem(a, a.pieces.coord) {
		t.Fatalf("freelance mode must NOT compose the HERA (coord) pane")
	}
	if a.AgentTaskID() != "free-1" {
		t.Fatalf("AGENT pane should bind to freelancer; got %q", a.AgentTaskID())
	}
}

// TestBuildApp_SwitchingSelectionRecomposesAndTearsDown proves moving across
// coordinator → agent → freelancer re-composes the body each time and tears
// down the now-absent pane's binding (coord blanks in freelance mode).
func TestBuildApp_SwitchingSelectionRecomposesAndTearsDown(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, _ := d.Orchestrators.Create(ctx, "proj")
	w, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "proj"})
	c, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w.ID, ArgusTaskID: "tw", WorktreePath: "/w"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: c.ID, ArgusTaskID: "tc", WorktreePath: "/c"})

	src := &freelancePaneSource{
		states: map[string]ArgusTaskState{
			// Keep the managed worker/coord rows live (not dead-hidden) by
			// giving argus state for their tasks.
			"tw": {Status: "in_progress"},
			"tc": {Status: "in_progress"},
		},
		tasks: []ArgusTaskInfo{
			{ID: "free-1", Name: "freelancer", Project: "Beta", State: ArgusTaskState{Status: "in_progress"}},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	a.SetFocusMachine(NewFocusMachine())
	a.selectDebounce = 0

	// Coordinator → full-width HERA.
	a.applyRailSelection(&orchEntry{ID: orch.ID, Name: "proj", CoordTaskID: "tc"})
	if bodyPaneCount(a) != 1 || !a.coordPresent || a.agentPresent {
		t.Fatalf("after coordinator select: want 1 pane coord-only; got panes=%d coord=%v agent=%v", bodyPaneCount(a), a.coordPresent, a.agentPresent)
	}

	// Agent → split.
	if !a.pieces.rail.SelectByRoleID(w.ID) {
		t.Fatalf("could not select worker row")
	}
	a.applyRailSelection(a.pieces.rail.CurrentRef())
	if bodyPaneCount(a) != 2 || !a.coordPresent || !a.agentPresent {
		t.Fatalf("after agent select: want 2 panes; got panes=%d coord=%v agent=%v", bodyPaneCount(a), a.coordPresent, a.agentPresent)
	}

	// Freelancer → full-width AGENT, coord torn down.
	freelancer := a.pieces.rail.freelance[0].Tasks[0]
	a.applyRailSelection(freelancer)
	if bodyPaneCount(a) != 1 || a.coordPresent || !a.agentPresent {
		t.Fatalf("after freelancer select: want 1 pane agent-only; got panes=%d coord=%v agent=%v", bodyPaneCount(a), a.coordPresent, a.agentPresent)
	}
	if a.CoordTaskID() != "" {
		t.Fatalf("freelance mode must release the coord binding; got %q", a.CoordTaskID())
	}
}

// TestApp_OnRailSelectEnter_CoordinatorEntersHERA proves Enter on a
// coordinator (orchestrator header) row enters the HERA (COORD) pane.
func TestApp_OnRailSelectEnter_CoordinatorEntersHERA(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, _ := d.Orchestrators.Create(ctx, "proj")
	w, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "proj"})
	c, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w.ID, ArgusTaskID: "tw", WorktreePath: "/w"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: c.ID, ArgusTaskID: "tc", WorktreePath: "/c"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	a.SetFocusMachine(NewFocusMachine())
	a.selectDebounce = 0

	if !a.pieces.rail.SelectByOrchID(orch.ID) {
		t.Fatalf("could not select orchestrator header")
	}
	got := a.OnRailSelectEnter()
	if got != FocusCOORD {
		t.Fatalf("Enter on coordinator header: want FocusCOORD (enter HERA pane), got %s", got)
	}
	if a.CoordTaskID() != "tc" {
		t.Fatalf("HERA pane should bind to coord task tc; got %q", a.CoordTaskID())
	}
}

// TestApp_OnRailSelectEnter_FreelancerEntersAgent proves Enter on a freelance
// row enters the AGENT pane.
func TestApp_OnRailSelectEnter_FreelancerEntersAgent(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, _ := d.Orchestrators.Create(ctx, "managed")
	c, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "Hera"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: c.ID, ArgusTaskID: "coord-1", WorktreePath: "/c"})

	src := &freelancePaneSource{
		states: map[string]ArgusTaskState{},
		tasks: []ArgusTaskInfo{
			{ID: "free-1", Name: "freelancer", Project: "Beta", State: ArgusTaskState{Status: "in_progress"}},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	a.SetFocusMachine(NewFocusMachine())
	a.selectDebounce = 0

	if !a.pieces.rail.SelectByRoleID(a.pieces.rail.freelance[0].Tasks[0].RoleID) {
		// freelance rows have RoleID 0; select by project header then move down.
		if !a.pieces.rail.SelectByProject("Beta") {
			t.Fatalf("could not select Beta freelance group")
		}
		a.pieces.rail.CursorDown()
	}
	got := a.OnRailSelectEnter()
	if got != FocusAGENT {
		t.Fatalf("Enter on freelancer: want FocusAGENT, got %s", got)
	}
	if a.AgentTaskID() != "free-1" {
		t.Fatalf("AGENT pane should bind to freelancer; got %q", a.AgentTaskID())
	}
}

// TestApp_OnRailSelectEnter_FreelanceHeaderFolds proves Enter on a Freelance
// repo-group header toggles the fold instead of entering a pane.
func TestApp_OnRailSelectEnter_FreelanceHeaderFolds(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, _ := d.Orchestrators.Create(ctx, "managed")
	c, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "Hera"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: c.ID, ArgusTaskID: "coord-1", WorktreePath: "/c"})

	src := &freelancePaneSource{
		states: map[string]ArgusTaskState{},
		tasks: []ArgusTaskInfo{
			{ID: "free-1", Name: "freelancer", Project: "Beta", State: ArgusTaskState{Status: "in_progress"}},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	a.SetFocusMachine(NewFocusMachine())
	a.selectDebounce = 0

	if !a.pieces.rail.SelectByProject("Beta") {
		t.Fatalf("could not select Beta freelance group header")
	}
	wasCollapsed := a.pieces.rail.freelanceCollapsed["Beta"]
	got := a.OnRailSelectEnter()
	if got != FocusRAIL {
		t.Fatalf("Enter on Freelance header: want FocusRAIL (fold, stay in rail), got %s", got)
	}
	if a.pieces.rail.freelanceCollapsed["Beta"] == wasCollapsed {
		t.Fatalf("Enter on Freelance header must toggle its fold state")
	}
}

// TestApp_OnRailSelectEnter_ArchiveExpandoFolds proves Enter on an Archive
// expando toggles its fold rather than entering a pane. Uses a per-coordinator
// Archive expando (an active orchestrator with an archived worker child), which
// renders without needing `l` listall.
func TestApp_OnRailSelectEnter_ArchiveExpandoFolds(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, _ := d.Orchestrators.Create(ctx, "live")
	c, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "live"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: c.ID, ArgusTaskID: "lc", WorktreePath: "/lc"})
	// An active worker so the orchestrator has a live row, plus an
	// argus-archived worker so the per-coordinator Archive expando renders
	// without needing the `l` listall toggle.
	w, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "live"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w.ID, ArgusTaskID: "tw", WorktreePath: "/w"})
	aw, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "old", Kind: db.KindWorker, ArgusProject: "live"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: aw.ID, ArgusTaskID: "told", WorktreePath: "/old"})

	src := &statePaneSource{states: map[string]ArgusTaskState{
		"lc":   {Status: "in_progress"},
		"tw":   {Status: "in_progress"},
		"told": {Status: "complete", Archived: true},
	}}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	a.SetFocusMachine(NewFocusMachine())
	a.selectDebounce = 0

	if !a.pieces.rail.SelectByArchiveOwner(orch.ID) {
		t.Fatalf("could not select per-coordinator Archive expando")
	}
	wasOpen := a.pieces.rail.archiveExpanded[orch.ID]
	got := a.OnRailSelectEnter()
	if got != FocusRAIL {
		t.Fatalf("Enter on Archive expando: want FocusRAIL (fold), got %s", got)
	}
	if a.pieces.rail.archiveExpanded[orch.ID] == wasOpen {
		t.Fatalf("Enter on Archive expando must toggle its fold")
	}
}
