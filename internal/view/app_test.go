package view

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

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
	if !strings.Contains(got, "coord") {
		t.Fatalf("expected role 'coord' in rail; got:\n%s", got)
	}
	if !strings.Contains(got, "worker-1") {
		t.Fatalf("expected role 'worker-1' in rail; got:\n%s", got)
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

	// Both role rows should be marked live (moon-stars icon, driven by
	// roleEntry.Live). Inspect the widget rather than the rendered text
	// so the assertion is robust against icon-font / theme changes.
	gotLive := map[string]bool{}
	for _, o := range a.pieces.rail.orchestrators {
		for _, r := range o.Roles {
			if r.Live {
				gotLive[r.Name] = true
			}
		}
	}
	if !gotLive["coord"] {
		t.Errorf("expected live coord role; rail orchestrators=%+v", a.pieces.rail.orchestrators)
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

func TestApp_OnRailSelectEnter_WorkerRebindsAgent(t *testing.T) {
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

	got := a.OnRailSelectEnter()
	if got != FocusRAIL {
		t.Fatalf("Enter on worker row: want FocusRAIL (keep focus to browse), got %s", got)
	}
	if a.AgentTaskID() != "t2" {
		t.Fatalf("agent task after rebind: want t2, got %q", a.AgentTaskID())
	}
}

func TestApp_OnRailSelectEnter_CoordRebindsCoord(t *testing.T) {
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

	// Initial coord pane is bound to tc (the only coord). Move selection
	// to the coord row anyway and confirm Enter returns FocusCOORD.
	if !a.pieces.rail.SelectByRoleID(c.ID) {
		t.Fatalf("could not locate coord role row in rail")
	}

	if got := a.OnRailSelectEnter(); got != FocusRAIL {
		t.Fatalf("Enter on coord row: want FocusRAIL (keep focus to browse), got %s", got)
	}
	if a.CoordTaskID() != "tc" {
		t.Fatalf("coord task after rebind: want tc, got %q", a.CoordTaskID())
	}
}

func TestApp_OnRailSelectEnter_OrchHeaderPropagates(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, _ := d.Orchestrators.Create(ctx, "proj")
	w, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "w", Kind: db.KindWorker, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w.ID, ArgusTaskID: "tw", WorktreePath: "/w"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Position cursor on the orchestrator header row.
	if !a.pieces.rail.SelectByOrchID(orch.ID) {
		t.Fatalf("could not locate orchestrator header row")
	}

	if got := a.OnRailSelectEnter(); got != FocusRAIL {
		t.Fatalf("Enter on orchestrator header: want FocusRAIL (let tree fold/unfold), got %s", got)
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
