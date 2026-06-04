package view

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/anutron/argus-sdk/theme"

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

	// At 80x24 the rail is width RailWidth and the two panes split the
	// remaining columns. Top bar = row 0; the body fills rows 1..23 — hera
	// renders NO bottom-bar row of its own (D12: argus draws the plugin-mode
	// status bar from hera's pushed hotkeys).
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

	// No internal bottom bar: hera advertises focus-aware hotkeys to argus
	// instead of rendering its own status row, so no focus-state bracket
	// label must appear anywhere on the surface.
	for _, label := range []string{"[RAIL]", "[COORD]", "[AGENT]"} {
		if strings.Contains(got, label) {
			t.Errorf("surface must not render hera's own bottom-bar label %q; got:\n%s", label, got)
		}
	}

	// The body's bottom pane border should reach the last row (row 23),
	// proving the body — not a retired bottom bar — owns that row.
	if !strings.ContainsAny(rows[23], "└┘─") {
		t.Errorf("expected the body's bottom border on the last row; got %q", rows[23])
	}
}

// OnFocusChanged must push a focus-appropriate hotkeys frame to argus (D12):
// the items reflect the new focus state's bindings, and the frame is a
// TEXT control envelope of shape {"type":"hotkeys","items":[...]}.
func TestApp_OnFocusChanged_PushesFocusAwareHotkeys(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	conn := &fakeControlConn{}
	a.SetControl(newViewControl(context.Background(), conn))

	decodeLast := func() (string, []HotkeyItem) {
		w := conn.Writes()
		if len(w) == 0 {
			t.Fatalf("no hotkeys frame pushed")
		}
		var env struct {
			Type  string       `json:"type"`
			Items []HotkeyItem `json:"items"`
		}
		if err := json.Unmarshal(w[len(w)-1].Data, &env); err != nil {
			t.Fatalf("hotkeys frame not valid JSON: %v", err)
		}
		return env.Type, env.Items
	}

	labelsFor := func(items []HotkeyItem) string {
		var sb strings.Builder
		for _, it := range items {
			sb.WriteString(it.Key)
			sb.WriteString(":")
			sb.WriteString(it.Label)
			sb.WriteString(" ")
		}
		return sb.String()
	}

	a.OnFocusChanged(FocusRAIL)
	typ, items := decodeLast()
	if typ != "hotkeys" {
		t.Fatalf("frame type: want hotkeys, got %q", typ)
	}
	railLabels := labelsFor(items)
	if !strings.Contains(railLabels, "j/k:move") || !strings.Contains(railLabels, "Esc:argus") {
		t.Fatalf("RAIL hotkeys missing rail-specific bindings: %s", railLabels)
	}

	a.OnFocusChanged(FocusCOORD)
	_, items = decodeLast()
	coordLabels := labelsFor(items)
	if !strings.Contains(coordLabels, "coord PTY") || strings.Contains(coordLabels, "j/k:move") {
		t.Fatalf("COORD hotkeys should reflect coord focus, not RAIL: %s", coordLabels)
	}
}

// TestApp_OnFocusChanged_PaintsArgusCyanBorders proves the focus-feedback
// color contract (D12): the focused element's border is painted in argus's
// title/focus cyan (theme.ColorTitle) and the two unfocused borders in argus's
// dim border gray (theme.ColorBorder), so Ctrl-H into hera feels like the same
// app. A regression to tcell.ColorYellow / ColorWhite (the pre-stage colors)
// must fail here. The rail and both panes embed tview.Box, so GetBorderColor
// reflects exactly what OnFocusChanged's SetBorderColor calls set.
func TestApp_OnFocusChanged_PaintsArgusCyanBorders(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Each case: the focused element shows ColorTitle, the other two ColorBorder.
	cases := []struct {
		name  string
		state FocusState
	}{
		{name: "RAIL", state: FocusRAIL},
		{name: "COORD", state: FocusCOORD},
		{name: "AGENT", state: FocusAGENT},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a.OnFocusChanged(tc.state)

			want := func(state FocusState, this FocusState) tcell.Color {
				if state == this {
					return theme.ColorTitle
				}
				return theme.ColorBorder
			}

			if got := a.pieces.rail.GetBorderColor(); got != want(tc.state, FocusRAIL) {
				t.Errorf("rail border = %v, want %v", got, want(tc.state, FocusRAIL))
			}
			if got := a.pieces.coord.GetBorderColor(); got != want(tc.state, FocusCOORD) {
				t.Errorf("coord border = %v, want %v", got, want(tc.state, FocusCOORD))
			}
			if got := a.pieces.agent.GetBorderColor(); got != want(tc.state, FocusAGENT) {
				t.Errorf("agent border = %v, want %v", got, want(tc.state, FocusAGENT))
			}

			// Guard against a silent regression to the retired focus colors.
			focusedColor := a.pieces.rail.GetBorderColor()
			switch tc.state {
			case FocusCOORD:
				focusedColor = a.pieces.coord.GetBorderColor()
			case FocusAGENT:
				focusedColor = a.pieces.agent.GetBorderColor()
			}
			if focusedColor == tcell.ColorYellow || focusedColor == tcell.ColorWhite {
				t.Errorf("focused border regressed to the pre-stage yellow/white: %v", focusedColor)
			}
		})
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

// TestApp_InPaneNavigate_MovesSelectionAndKeepsPaneFocus proves the ⌘↑/↓
// (in-pane navigation) behavior: from inside a pane, navigating to the
// next/prev agent moves the rail selection AND re-enters the new selection's
// primary pane — focus stays in a pane (AGENT here), never returns to RAIL,
// and the bound agent task changes to the new selection's task.
func TestApp_InPaneNavigate_MovesSelectionAndKeepsPaneFocus(t *testing.T) {
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
	focus := NewFocusMachine()
	a.SetFocusMachine(focus)
	a.selectDebounce = 0

	// Land the cursor on w1 and enter its AGENT pane (focus in a pane).
	if !a.pieces.rail.SelectByRoleID(w1.ID) {
		t.Fatalf("could not select w1")
	}
	a.OnRailSelectEnter()
	focus.JumpToAGENT()
	if a.AgentTaskID() != "t1" {
		t.Fatalf("baseline agent: want t1, got %q", a.AgentTaskID())
	}

	// In-pane nav forward: selection moves to w2, focus stays in a pane bound
	// to w2's task.
	got := a.InPaneNavigate(+1)
	if got != FocusAGENT {
		t.Fatalf("InPaneNavigate(+1) on worker: want FocusAGENT (stay in pane), got %s", got)
	}
	if got == FocusRAIL {
		t.Fatalf("InPaneNavigate must NOT return focus to RAIL")
	}
	if a.AgentTaskID() != "t2" {
		t.Fatalf("agent task after in-pane nav: want t2, got %q", a.AgentTaskID())
	}

	// Back again lands on w1.
	if got := a.InPaneNavigate(-1); got != FocusAGENT {
		t.Fatalf("InPaneNavigate(-1): want FocusAGENT, got %s", got)
	}
	if a.AgentTaskID() != "t1" {
		t.Fatalf("agent task after reverse nav: want t1, got %q", a.AgentTaskID())
	}
}

// TestApp_InPaneNavigate_SkipsCoordlessOrchHeader proves that in-pane nav
// (⌘↑/↓) never strands the operator on a coord-less orchestrator header
// (CoordTaskID == "") — a row that is pane-bindable for j/k navigation but has
// no pane to enter (OnRailSelectEnter → FocusRAIL). When such a header sits
// between two real pane-bindable agent rows, in-pane nav must skip it and land
// focus on the next row that actually enters a pane, with the bound task
// changed. This satisfies "focus remains in a pane bound to the NEW selection."
func TestApp_InPaneNavigate_SkipsCoordlessOrchHeader(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	w1, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w1.ID, ArgusTaskID: "t1", WorktreePath: "/1"})

	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	focus := NewFocusMachine()
	a.SetFocusMachine(focus)
	a.selectDebounce = 0

	// Hand-build a rail layout that puts a coord-less orchestrator header
	// between two pane-bindable agent rows:
	//   w1 (AGENT) → coordless-orch header (bindable but FocusRAIL) → w2 (AGENT)
	w1Role := &roleEntry{OrchestratorID: orch.ID, RoleID: w1.ID, Name: "w1", RoleKind: string(db.KindWorker), ArgusTaskID: "t1", Live: true}
	w2Role := &roleEntry{OrchestratorID: 99, RoleID: 200, Name: "w2", RoleKind: string(db.KindWorker), ArgusTaskID: "t2", Live: true}
	coordlessHeader := &orchEntry{ID: 99, Name: "coordless", CoordTaskID: ""}
	a.pieces.rail.orchestrators = []*orchEntry{
		{ID: orch.ID, Name: "proj", CoordTaskID: "", Roles: []*roleEntry{w1Role}},
		coordlessHeader,
	}
	a.pieces.rail.rows = []railRow{
		{kind: railRowRole, role: w1Role},          // 0 bindable → AGENT (t1)
		{kind: railRowOrch, orch: coordlessHeader}, // 1 bindable but coord-less → FocusRAIL
		{kind: railRowRole, role: w2Role},          // 2 bindable → AGENT (t2)
	}

	// Land on w1 and enter its AGENT pane.
	a.pieces.rail.cursor = 0
	a.OnRailSelectEnter()
	focus.JumpToAGENT()
	if a.AgentTaskID() != "t1" {
		t.Fatalf("baseline agent: want t1, got %q", a.AgentTaskID())
	}

	// In-pane nav forward must SKIP the coord-less header and land on w2's
	// AGENT pane — never RAIL — with the bound task changed to t2.
	got := a.InPaneNavigate(+1)
	if got == FocusRAIL {
		t.Fatalf("InPaneNavigate must not strand focus in RAIL on a coord-less header")
	}
	if got != FocusAGENT {
		t.Fatalf("InPaneNavigate(+1): want FocusAGENT on w2, got %s", got)
	}
	if a.AgentTaskID() != "t2" {
		t.Fatalf("bound agent task should change to t2 (coord-less header skipped); got %q", a.AgentTaskID())
	}
	// Selection must have moved past the coord-less header onto w2.
	if ref, ok := a.pieces.rail.CurrentRef().(*roleEntry); !ok || ref.RoleID != 200 {
		t.Fatalf("rail selection should be on w2; got %T %+v", a.pieces.rail.CurrentRef(), a.pieces.rail.CurrentRef())
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

// TestPopulateRail_SubCoordinatorNestsAndBindsOwnTask is the end-to-end
// multi-binding fixture: orchestrator "parent" has a worker role "sub" whose
// live binding's argus task is ALSO orchestrator "child"'s coord task. populate
// must (a) nest "child"'s leaf under the "sub" sub-coordinator row, (b) NOT
// double-render "child" at top level, and (c) bind HERA to the SUB-COORD's OWN
// task (not the parent's coord) when the sub-coordinator is selected →
// full-width HERA (coordPresent=true, agentPresent=false).
func TestPopulateRail_SubCoordinatorNestsAndBindsOwnTask(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	parent, _ := d.Orchestrators.Create(ctx, "parent")
	parentCoord, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: parent.ID, Name: "parent-coord", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	subWorker, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: parent.ID, Name: "sub", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: parentCoord.ID, ArgusTaskID: "t-parent-coord", WorktreePath: "/pc"})
	// The sub worker's task IS the child orchestrator's coord task (multi-binding).
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: subWorker.ID, ArgusTaskID: "t-sub", WorktreePath: "/sub"})

	child, _ := d.Orchestrators.Create(ctx, "child")
	childCoord, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: child.ID, Name: "child-coord", Kind: db.KindCoordinator, ArgusProject: "p",
	})
	childLeaf, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: child.ID, Name: "child-leaf", Kind: db.KindWorker, ArgusProject: "p",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: childCoord.ID, ArgusTaskID: "t-sub", WorktreePath: "/sub"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: childLeaf.ID, ArgusTaskID: "t-child-leaf", WorktreePath: "/cl"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	a.SetFocusMachine(NewFocusMachine())
	a.selectDebounce = 0

	// The child orchestrator must NOT render as a top-level row (it's consumed
	// as a nested sub-coordinator). Top-level rows are only "parent".
	topLevelOrchs := 0
	var subCoordRow *roleEntry
	for _, row := range a.pieces.rail.rows {
		if row.kind == railRowOrch && row.orch != nil && !row.orch.Archived {
			topLevelOrchs++
			if row.orch.ID == child.ID {
				t.Fatalf("child orchestrator must NOT render at top level; it nests under the sub-coordinator")
			}
		}
		if row.kind == railRowRole && row.role != nil && row.role.RoleID == subWorker.ID {
			subCoordRow = row.role
		}
	}
	if topLevelOrchs != 1 {
		t.Fatalf("expected exactly one top-level orchestrator (parent); got %d", topLevelOrchs)
	}
	if subCoordRow == nil {
		t.Fatalf("sub-coordinator role row missing from the rail rows")
	}
	if subCoordRow.RoleKind != string(db.KindCoordinator) {
		t.Fatalf("the sub worker must be promoted to coordinator kind; got %q", subCoordRow.RoleKind)
	}
	if subCoordRow.childOrch == nil || subCoordRow.childOrch.ID != child.ID {
		t.Fatalf("sub-coordinator must carry its childOrch pointer; got %+v", subCoordRow.childOrch)
	}

	// The child's leaf must be present as a nested row.
	foundChildLeaf := false
	for _, row := range a.pieces.rail.rows {
		if row.kind == railRowRole && row.role != nil && row.role.RoleID == childLeaf.ID {
			foundChildLeaf = true
		}
	}
	if !foundChildLeaf {
		t.Fatalf("child orchestrator's leaf must render nested under the sub-coordinator")
	}

	// Selecting the sub-coordinator composes full-width HERA bound to ITS OWN
	// task (t-sub), not the parent's coord (t-parent-coord).
	if !a.pieces.rail.SelectByRoleID(subWorker.ID) {
		t.Fatalf("could not select the sub-coordinator row")
	}
	a.applyRailSelection(a.pieces.rail.CurrentRef())
	if a.CoordTaskID() != "t-sub" {
		t.Fatalf("HERA must bind to the sub-coordinator's OWN task t-sub; got %q", a.CoordTaskID())
	}
	if !a.coordPresent || a.agentPresent {
		t.Fatalf("sub-coordinator selection must be full-width HERA (coordPresent=true, agentPresent=false); got coord=%v agent=%v", a.coordPresent, a.agentPresent)
	}

	// Enter on the sub-coordinator enters the HERA pane.
	if got := a.OnRailSelectEnter(); got != FocusCOORD {
		t.Fatalf("Enter on a sub-coordinator must enter HERA (FocusCOORD); got %s", got)
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

// TestPopulateRail_WorkerLessOrchestratorRendersHeaderOnly confirms that a
// top-level orchestrator with a live coord but NO worker agents renders as
// JUST its foldable coordinator header row — no synthetic child coord/agent
// row — and that selecting the header composes the full-width HERA pane
// (coordinator mode) bound to the coord's task.
func TestPopulateRail_WorkerLessOrchestratorRendersHeaderOnly(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "solo")
	coordRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "solo",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coordRole.ID, ArgusTaskID: "tc", WorktreePath: "/c"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	a.SetFocusMachine(NewFocusMachine())
	a.selectDebounce = 0

	// The orchEntry carries the coord task but has zero child rows.
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
	if len(got.Roles) != 0 {
		t.Fatalf("worker-less orchestrator must have zero child roles (header IS the coord); got %+v", got.Roles)
	}

	// The rail renders exactly one row: the foldable coordinator header.
	if len(a.pieces.rail.rows) != 1 {
		t.Fatalf("worker-less orchestrator must render exactly one rail row (the header); got %d rows", len(a.pieces.rail.rows))
	}
	if a.pieces.rail.rows[0].kind != railRowOrch {
		t.Fatalf("the sole row must be the coordinator header (railRowOrch); got kind %d", a.pieces.rail.rows[0].kind)
	}

	// No child coord/agent role row should be selectable.
	if a.pieces.rail.SelectByRoleID(coordRole.ID) {
		t.Fatalf("coord role row must not be selectable (no synthetic child row)")
	}

	// Selecting the header yields coordinator mode bound to the coord's task.
	if !a.pieces.rail.SelectByOrchID(orch.ID) {
		t.Fatalf("could not locate orchestrator header row")
	}
	if got := a.OnRailSelectEnter(); got != FocusCOORD {
		t.Fatalf("Enter on worker-less coordinator header: want FocusCOORD, got %s", got)
	}
	if a.CoordTaskID() != "tc" {
		t.Fatalf("coord task should be tc after header Enter; got %q", a.CoordTaskID())
	}
	if !a.coordPresent || a.agentPresent {
		t.Fatalf("worker-less coordinator must compose full-width HERA (coord-only); coordPresent=%v agentPresent=%v", a.coordPresent, a.agentPresent)
	}
}

// TestPopulateRail_DeadBindingsHiddenByDefault confirms that when the argus
// state cache (warm) has NO RECORD of a worker binding's task — the task was
// pruned / 404s — the role row is filtered out of the rail. With
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

	// t-dead is absent from the (warm) state cache: the record is gone.
	// statePaneSource has no StatesReady method, so the cache counts as warm.
	src := &statePaneSource{states: map[string]ArgusTaskState{
		"t-alive": {Status: "in_progress"},
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
// coord binding whose argus task RECORD is gone (absent from the warm
// state cache — pruned / 404) does NOT populate the orchestrator's
// CoordTaskID — otherwise the COORD pane would bind to a tombstone and
// render placeholder forever.
func TestPopulateRail_DeadCoordSkippedFromCoordTaskID(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	coordRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "proj",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coordRole.ID, ArgusTaskID: "t-dead-coord", WorktreePath: "/c"})

	// Warm cache, no record of t-dead-coord: the task is gone.
	src := &statePaneSource{states: map[string]ArgusTaskState{}}
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

// aliveStatePaneSource mirrors the daemon's managerPaneSource capability set
// for bucketing tests: BOTH a status-based TaskAliveChecker (argus maps
// complete/failed/stopped to not-alive) AND a TaskStateProvider whose cache
// still holds the task record. The pair is what the completed-not-archived
// regression needs — the checker says "not alive" while the record exists.
type aliveStatePaneSource struct {
	fakePaneSource
	alive  map[string]bool
	states map[string]ArgusTaskState
}

func (s *aliveStatePaneSource) IsTaskAlive(taskID string) bool { return s.alive[taskID] }
func (s *aliveStatePaneSource) TaskState(taskID string) (ArgusTaskState, bool) {
	st, ok := s.states[taskID]
	return st, ok
}

// Spec (complete-not-archived): stepping a task to complete must NOT bucket it
// as archived. A completed task whose argus record still exists (cache hit,
// archived=false, hera archived_at NULL) renders among its coordinator's
// ACTIVE children with Dead=false — status never buckets; only archive flags
// and record-nonexistence do.
func TestPopulateRail_CompletedTaskStaysActive(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	w, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "stepped-done", Kind: db.KindWorker, ArgusProject: "proj",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w.ID, ArgusTaskID: "t-done", WorktreePath: "/w"})

	src := &aliveStatePaneSource{
		alive:  map[string]bool{"t-done": false}, // status-based checker: complete => "not alive"
		states: map[string]ArgusTaskState{"t-done": {Status: "complete"}},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	var entry *roleEntry
	for _, o := range a.pieces.rail.orchestrators {
		for _, r := range o.Roles {
			if r.RoleID == w.ID {
				entry = r
			}
		}
	}
	if entry == nil {
		t.Fatalf("completed worker role missing from rail data")
	}
	if entry.Dead {
		t.Fatalf("completed task with an existing argus record must not be Dead; got %+v", entry)
	}
	if roleArchived(entry) {
		t.Fatalf("completed task must not bucket as archived (no archive flag set); got %+v", entry)
	}
	if !entry.HasState || entry.Status != "complete" {
		t.Fatalf("row must carry the complete status for the ✓ glyph; got HasState=%v Status=%q", entry.HasState, entry.Status)
	}

	// Render: the row stays visible in the default (no `l`) view.
	got := renderApp(t, a, 80, 24)
	if !strings.Contains(got, "stepped-done") {
		t.Fatalf("completed row must render among active children; got:\n%s", got)
	}
}

// Spec (complete-not-archived): a coordinator whose task is completed but
// still exists keeps feeding the orchestrator header — CoordTaskID binds the
// pane and CoordStatus drives the ✓ glyph. Only archived or record-gone coord
// bindings are skipped as tombstones.
func TestPopulateRail_CompletedCoordStillFeedsCoordTaskID(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	coordRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "proj",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coordRole.ID, ArgusTaskID: "t-coord-done", WorktreePath: "/c"})

	src := &aliveStatePaneSource{
		alive:  map[string]bool{"t-coord-done": false},
		states: map[string]ArgusTaskState{"t-coord-done": {Status: "complete"}},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	var entry *orchEntry
	for _, o := range a.pieces.rail.orchestrators {
		if o.ID == orch.ID {
			entry = o
		}
	}
	if entry == nil {
		t.Fatalf("orchestrator missing from rail data")
	}
	if entry.CoordTaskID != "t-coord-done" {
		t.Fatalf("completed-but-existing coord must feed CoordTaskID; got %q", entry.CoordTaskID)
	}
	if !entry.CoordHasState || entry.CoordStatus != "complete" {
		t.Fatalf("header must carry the coord's complete state; got CoordHasState=%v CoordStatus=%q", entry.CoordHasState, entry.CoordStatus)
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
// selecting a coordinator (orchestrator header) composes rail + the HERA pane
// bound to the coord task + the right-side Details pane, with NO agent pane
// present (coord-details-pane change; HERA was previously full-width).
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
	// Coordinator mode composes the HERA pane plus the Details pane (2 panes),
	// with NO agent pane (coord-details-pane change).
	if got := bodyPaneCount(a); got != 2 {
		t.Fatalf("coordinator mode body must have HERA + Details (2 panes); got %d", got)
	}
	if bodyHasItem(a, a.pieces.agent) {
		t.Fatalf("coordinator mode must NOT compose the AGENT pane into the body")
	}
	if !bodyHasItem(a, a.pieces.coord) {
		t.Fatalf("coordinator mode must compose the HERA (coord) pane into the body")
	}
	if !bodyHasItem(a, a.pieces.details) {
		t.Fatalf("coordinator mode must compose the Details pane into the body")
	}
}

// TestBuildApp_InitialFrameMatchesInitialSelection proves the opening frame
// honors the three-mode contract for the initially-selected row. With only a
// coord bound (no live worker), findInitialSelection picks the coordinator, so
// the first frame must be coordinator mode (HERA + Details, NO agent pane) —
// not the hardcoded agent split — even before any keypress.
func TestBuildApp_InitialFrameMatchesInitialSelection(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, _ := d.Orchestrators.Create(ctx, "proj")
	c, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: c.ID, ArgusTaskID: "tc", WorktreePath: "/c"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Initial selection landed on the coordinator (the orchestrator header is
	// the only selectable row), so the opening frame must be coordinator mode.
	if !a.coordPresent || a.agentPresent {
		t.Fatalf("initial frame: want coordPresent=true agentPresent=false (coordinator-initial), got %v/%v", a.coordPresent, a.agentPresent)
	}
	// The coordinator-initial frame composes HERA + Details (2 panes), no agent.
	if got := bodyPaneCount(a); got != 2 {
		t.Fatalf("coordinator-initial frame must compose HERA + Details (2 panes); got %d", got)
	}
	if bodyHasItem(a, a.pieces.agent) {
		t.Fatalf("coordinator-initial frame must NOT compose the AGENT pane")
	}
	if !bodyHasItem(a, a.pieces.details) {
		t.Fatalf("coordinator-initial frame must compose the Details pane")
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

// TestBuildApp_WorkerInCoordlessProjectClearsCoord proves that selecting a
// worker in a project with no coord task clears the HERA pane rather than
// leaving it bound to the PREVIOUS project's coordinator. Without the fix the
// `if coordTask != ""` guard skipped rebindCoord, so HERA kept showing a
// foreign coordinator while the split stayed.
func TestBuildApp_WorkerInCoordlessProjectClearsCoord(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	// Project 1: coord + worker (coord-ful).
	o1, _ := d.Orchestrators.Create(ctx, "coordful")
	c1, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: o1.ID, Name: "c", Kind: db.KindCoordinator, ArgusProject: "p1"})
	w1, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: o1.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "p1"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: c1.ID, ArgusTaskID: "tc1", WorktreePath: "/c1"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w1.ID, ArgusTaskID: "tw1", WorktreePath: "/w1"})

	// Project 2: worker only, no coord (coord-less).
	o2, _ := d.Orchestrators.Create(ctx, "coordless")
	w2, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: o2.ID, Name: "w2", Kind: db.KindWorker, ArgusProject: "p2"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w2.ID, ArgusTaskID: "tw2", WorktreePath: "/w2"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	a.SetFocusMachine(NewFocusMachine())
	a.selectDebounce = 0

	// Select the coord-ful project's worker first: HERA binds to tc1.
	if !a.pieces.rail.SelectByRoleID(w1.ID) {
		t.Fatalf("could not select coord-ful worker")
	}
	if a.CoordTaskID() != "tc1" {
		t.Fatalf("after coord-ful worker, HERA should bind to tc1; got %q", a.CoordTaskID())
	}

	// Now select the coord-less project's worker. HERA must clear to "",
	// NOT keep showing the prior project's coordinator (tc1).
	if !a.pieces.rail.SelectByRoleID(w2.ID) {
		t.Fatalf("could not select coord-less worker")
	}
	if a.CoordTaskID() != "" {
		t.Fatalf("worker in coord-less project must clear HERA; got %q (foreign coord leaked)", a.CoordTaskID())
	}
	if !a.agentPresent {
		t.Fatalf("worker selection must keep the AGENT pane present")
	}
	if a.AgentTaskID() != "tw2" {
		t.Fatalf("AGENT should bind to the coord-less worker tw2; got %q", a.AgentTaskID())
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

	// Coordinator → HERA + Details (2 panes), no agent.
	a.applyRailSelection(&orchEntry{ID: orch.ID, Name: "proj", CoordTaskID: "tc"})
	if bodyPaneCount(a) != 2 || !a.coordPresent || a.agentPresent {
		t.Fatalf("after coordinator select: want HERA+Details (2 panes) coord-only; got panes=%d coord=%v agent=%v", bodyPaneCount(a), a.coordPresent, a.agentPresent)
	}
	if !bodyHasItem(a, a.pieces.details) {
		t.Fatalf("coordinator select must compose the Details pane")
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

// TestPopulateRail_HeraArchivedRoleInArchiveExpandoByDefault pins the
// archive-visibility partition for HERA-archived roles (archived_at set, the
// `a` key): in the DEFAULT view (showArchived off) the role must move into
// its coordinator's Archive (N) expando — collapsed, but present and
// reachable — instead of vanishing from the rail entirely. Dead and
// argus-archived children already get this treatment; a hera-archived child
// must not be the odd one out just because the rail's role query dropped it.
// Also guards the consumers: the header's live-child (N) count must exclude
// the archived role, and folding the expando open must reveal it.
func TestPopulateRail_HeraArchivedRoleInArchiveExpandoByDefault(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	liveWorker, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "activerow", Kind: db.KindWorker, ArgusProject: "proj",
	})
	archWorker, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "stashedrow", Kind: db.KindWorker, ArgusProject: "proj",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: liveWorker.ID, ArgusTaskID: "t-active", WorktreePath: "/a"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: archWorker.ID, ArgusTaskID: "t-stashed", WorktreePath: "/s"})
	if err := d.Roles.Archive(ctx, archWorker.ID); err != nil {
		t.Fatalf("archive role: %v", err)
	}

	src := &alivePaneSource{alive: map[string]bool{
		"t-active":  true,
		"t-stashed": true,
	}}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// The archived role must be present in the rail DATA (Archived=true) so
	// buildRows can bucket it into the per-coordinator Archive expando.
	var archEntry *roleEntry
	for _, o := range a.pieces.rail.orchestrators {
		for _, r := range o.Roles {
			if r.RoleID == archWorker.ID {
				archEntry = r
			}
		}
	}
	if archEntry == nil {
		t.Fatalf("hera-archived role missing from rail data in default view: it vanished instead of moving into the Archive expando")
	}
	if !archEntry.Archived {
		t.Fatalf("expected Archived=true on hera-archived role; got %+v", archEntry)
	}

	got := renderApp(t, a, 80, 24)
	// Collapsed by default: the expando header renders, the row name doesn't.
	if !strings.Contains(got, "Archive (1)") {
		t.Fatalf("coordinator must render an Archive (1) expando for its hera-archived child in the default view; got:\n%s", got)
	}
	if strings.Contains(got, "stashedrow") {
		t.Fatalf("archived row must stay behind the collapsed expando by default; got:\n%s", got)
	}
	// Header live-child count excludes the archived role.
	if !strings.Contains(got, "proj (1)") {
		t.Fatalf("coordinator header (N) must count only active children; got:\n%s", got)
	}

	// Folding the expando open reveals the archived row — never unreachable.
	a.pieces.rail.archiveExpanded[orch.ID] = true
	a.pieces.rail.buildRows()
	got = renderApp(t, a, 80, 24)
	if !strings.Contains(got, "stashedrow") {
		t.Fatalf("opening the Archive expando must reveal the hera-archived row; got:\n%s", got)
	}
}

// TestFindInitialSelection_SkipsHeraArchivedRole guards startup
// auto-selection against the inclusive rail load: an archived role — even
// with a live binding whose argus task is alive — must never be picked for
// the AGENT or COORD pane.
func TestFindInitialSelection_SkipsHeraArchivedRole(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	archWorker, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "stashed", Kind: db.KindWorker, ArgusProject: "proj",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: archWorker.ID, ArgusTaskID: "t-stashed", WorktreePath: "/s"})
	if err := d.Roles.Archive(ctx, archWorker.ID); err != nil {
		t.Fatalf("archive role: %v", err)
	}

	src := &alivePaneSource{alive: map[string]bool{"t-stashed": true}}
	coord, agent := findInitialSelection(d, src)
	if coord != "" || agent != "" {
		t.Fatalf("startup selection must never bind an archived role's task; got coord=%q agent=%q", coord, agent)
	}
}

// TestPopulateRail_ArchivedSubCoordStaysInExpando pins the multi-binding
// lift for an ARCHIVED worker that is also a child orchestrator's coord:
// the default view must mirror today's `l` listall behavior (the only mode
// that previously ran the inclusive query through resolveSubCoordinators) —
// the worker is promoted to a coordinator row inside its parent's Archive
// expando, and the child orchestrator is consumed from the top level so it
// is never double-rendered as an active root.
func TestPopulateRail_ArchivedSubCoordStaysInExpando(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	parent, _ := d.Orchestrators.Create(ctx, "parent")
	child, _ := d.Orchestrators.Create(ctx, "childproj")
	// Worker under parent, bound to the SAME argus task as child's coord.
	subWorker, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: parent.ID, Name: "subcoord", Kind: db.KindWorker, ArgusProject: "parent",
	})
	childCoord, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: child.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "childproj",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: subWorker.ID, ArgusTaskID: "t-multi", WorktreePath: "/m"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: childCoord.ID, ArgusTaskID: "t-multi", WorktreePath: "/m"})
	if err := d.Roles.Archive(ctx, subWorker.ID); err != nil {
		t.Fatalf("archive role: %v", err)
	}

	src := &alivePaneSource{alive: map[string]bool{"t-multi": true}}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// The child orchestrator must be consumed by the lift (not a top-level
	// row), exactly as in `l` mode.
	for _, o := range a.pieces.rail.orchestrators {
		if o.ID == child.ID {
			t.Fatalf("child orchestrator must be consumed by the sub-coordinator lift, not rendered top-level")
		}
	}
	// The parent has zero non-archived children, so it defaults collapsed;
	// expand it explicitly (the operator's toggle) to reach its Archive expando.
	a.pieces.rail.collapsed[parent.ID] = false
	a.pieces.rail.buildRows()

	// The archived sub-coord renders only behind the parent's Archive expando.
	got := renderApp(t, a, 80, 24)
	if !strings.Contains(got, "Archive (1)") {
		t.Fatalf("parent must render an Archive (1) expando holding the archived sub-coordinator; got:\n%s", got)
	}
	if strings.Contains(got, "subcoord") {
		t.Fatalf("archived sub-coordinator must stay behind the collapsed expando by default; got:\n%s", got)
	}
	a.pieces.rail.archiveExpanded[parent.ID] = true
	a.pieces.rail.buildRows()
	got = renderApp(t, a, 80, 24)
	if !strings.Contains(got, "subcoord") {
		t.Fatalf("opening the parent's Archive expando must reveal the archived sub-coordinator; got:\n%s", got)
	}
}

// Spec (mixed-coord-repair): a task that is the coord-pane binding
// (CoordTaskID) of a RENDERED orchestrator header must NOT additionally fall
// back into the Freelance section — the header preserves findability
// (selecting it binds the coord pane to the task), so the freelance row was a
// duplicate (live observation: archive-this-coord rendered both as a
// collapsed orchestrator header and as a freelance row).
func TestBuildApp_HeaderReachableCoordTaskDoesNotDuplicateIntoFreelance(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, err := d.Orchestrators.Create(ctx, "hera-view-finish")
	if err != nil {
		t.Fatalf("orch: %v", err)
	}
	coord, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "Hera",
	})
	if err != nil {
		t.Fatalf("role: %v", err)
	}
	bnd, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: coord.ID, ArgusTaskID: "ux-qa", WorktreePath: "/w",
	})
	if err != nil {
		t.Fatalf("binding: %v", err)
	}
	if err := d.Bindings.End(ctx, bnd.ID, "resync_missing"); err != nil {
		t.Fatalf("end binding: %v", err)
	}

	src := &freelancePaneSource{
		states: map[string]ArgusTaskState{"ux-qa": {Status: "in_review"}},
		tasks: []ArgusTaskInfo{
			{ID: "ux-qa", Name: "hera-1.0-ux-qa", Project: "Hera", Elapsed: "2h", State: ArgusTaskState{Status: "in_review"}},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// The header carries the task as its coord-pane binding (the latest-
	// binding fallback), so the task IS reachable through the rendered tree...
	var entry *orchEntry
	for _, o := range a.pieces.rail.orchestrators {
		if o.ID == orch.ID {
			entry = o
		}
	}
	if entry == nil || entry.CoordTaskID != "ux-qa" {
		t.Fatalf("header must carry the lapsed coord task as CoordTaskID; got %+v", entry)
	}
	// ...so Freelance must not duplicate it.
	if fl := a.pieces.rail.freelance; len(fl) != 0 {
		t.Fatalf("header-reachable coord task must not duplicate into Freelance; got %+v", fl)
	}
}

// Spec (mixed-coord-repair): a TRULY orphaned task still falls back to
// Freelance (rail-truthfulness preserved). Here the coord role is
// hera-archived, so the rendered header binds no coord task (CoordTaskID
// empty) — without the fallback the live argus task would be reachable
// nowhere.
func TestBuildApp_OrphanedCoordTaskStillFallsBackToFreelance(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, err := d.Orchestrators.Create(ctx, "hera-view-finish")
	if err != nil {
		t.Fatalf("orch: %v", err)
	}
	coord, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "Hera",
	})
	if err != nil {
		t.Fatalf("role: %v", err)
	}
	bnd, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: coord.ID, ArgusTaskID: "ux-qa", WorktreePath: "/w",
	})
	if err != nil {
		t.Fatalf("binding: %v", err)
	}
	if err := d.Bindings.End(ctx, bnd.ID, "resync_missing"); err != nil {
		t.Fatalf("end binding: %v", err)
	}
	// Hera-archive the coord role: the header then binds NO coord task, so
	// the task is not reachable through the rendered tree.
	if err := d.Roles.Archive(ctx, coord.ID); err != nil {
		t.Fatalf("archive role: %v", err)
	}

	src := &freelancePaneSource{
		states: map[string]ArgusTaskState{"ux-qa": {Status: "in_review"}},
		tasks: []ArgusTaskInfo{
			{ID: "ux-qa", Name: "hera-1.0-ux-qa", Project: "Hera", Elapsed: "2h", State: ArgusTaskState{Status: "in_review"}},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	fl := a.pieces.rail.freelance
	if len(fl) != 1 || len(fl[0].Tasks) != 1 || fl[0].Tasks[0].ArgusTaskID != "ux-qa" {
		t.Fatalf("orphaned coord task (header binds nothing) must fall back to Freelance; got %+v", fl)
	}

	// The named row renders in the rail, so the operator can FIND the session.
	out := renderApp(t, a, 100, 40)
	if !strings.Contains(out, "hera-1.0-ux-qa") {
		t.Fatalf("rail render must contain the orphaned coord task by name\n%s", out)
	}
}

// Spec (mixed-coord-repair): populateRail captures the coord task's argus
// archived bit onto the orchestrator entry, so the header can render the ⊘
// repair cue and `a` can pick the repair-first direction.
func TestBuildApp_MixedCoordArchivedLandsOnEntry(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, err := d.Orchestrators.Create(ctx, "mixed")
	if err != nil {
		t.Fatalf("orch: %v", err)
	}
	coord, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "Hera",
	})
	if err != nil {
		t.Fatalf("role: %v", err)
	}
	if _, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: coord.ID, ArgusTaskID: "T1", WorktreePath: "/w",
	}); err != nil {
		t.Fatalf("binding: %v", err)
	}

	src := &freelancePaneSource{
		states: map[string]ArgusTaskState{"T1": {Status: "in_progress", Idle: true, Archived: true}},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	var entry *orchEntry
	for _, o := range a.pieces.rail.orchestrators {
		if o.ID == orch.ID {
			entry = o
		}
	}
	if entry == nil {
		t.Fatal("orchestrator entry missing from rail")
	}
	if entry.CoordTaskID != "T1" {
		t.Fatalf("mixed-coord header keeps its coord-pane binding; got %q", entry.CoordTaskID)
	}
	if !entry.CoordArgusArchived {
		t.Fatal("coord task's argus archived bit must land on entry.CoordArgusArchived")
	}

	// The selection bridge carries the repair signal to the `a` handler.
	if !a.pieces.rail.SelectByOrchID(orch.ID) {
		t.Fatal("select orchestrator header")
	}
	sel := a.CurrentRailSelection()
	if sel.Kind != selOrchestrator || sel.CoordTaskID != "T1" || !sel.CoordArgusArchived {
		t.Fatalf("railSelection must carry CoordTaskID+CoordArgusArchived; got %+v", sel)
	}
}

// Spec (rail-truthfulness): a worker role whose only binding ended still
// renders as a role ROW via the latest-binding fallback — it must NOT
// additionally appear in the Freelance section.
func TestBuildApp_EndedBindingWorkerRowDoesNotDuplicateIntoFreelance(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, err := d.Orchestrators.Create(ctx, "kbtest")
	if err != nil {
		t.Fatalf("orch: %v", err)
	}
	w, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "hit-s-on-this-agent", Kind: db.KindWorker, ArgusProject: "Hera",
	})
	if err != nil {
		t.Fatalf("role: %v", err)
	}
	bnd, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: w.ID, ArgusTaskID: "hit-s", WorktreePath: "/w",
	})
	if err != nil {
		t.Fatalf("binding: %v", err)
	}
	if err := d.Bindings.End(ctx, bnd.ID, "argus_archived"); err != nil {
		t.Fatalf("end binding: %v", err)
	}

	src := &freelancePaneSource{
		states: map[string]ArgusTaskState{"hit-s": {Status: "in_review"}},
		tasks: []ArgusTaskInfo{
			{ID: "hit-s", Name: "hit-s-on-this-agent", Project: "Hera", State: ArgusTaskState{Status: "in_review"}},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// The worker renders as a tree row carrying the task id...
	var rowTask string
	for _, o := range a.pieces.rail.orchestrators {
		for _, r := range o.Roles {
			if r.RoleID == w.ID {
				rowTask = r.ArgusTaskID
			}
		}
	}
	if rowTask != "hit-s" {
		t.Fatalf("worker row must carry the ended binding's task id via the latest-binding fallback; got %q", rowTask)
	}
	// ...so Freelance must not duplicate it.
	if fl := a.pieces.rail.freelance; len(fl) != 0 {
		t.Fatalf("task rendered as a role row must not duplicate into Freelance; got %+v", fl)
	}
}

// TestApp_QueueSelectRole_AppliedOnNextRepopulate proves FIX 2: the worker
// auto-select is DEFERRED to the next rail repopulate. At QueueSelectRole time
// the new row does not exist (the broadcaster-driven refresh has not run), so
// an immediate select would no-op. After the role+binding are inserted and the
// rail repopulates, the queued row becomes the rail selection — and focus is
// untouched (stays RAIL).
func TestApp_QueueSelectRole_AppliedOnNextRepopulate(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	coord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "proj-coord", Kind: db.KindCoordinator, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coord.ID, ArgusTaskID: "c1", WorktreePath: "/c"})

	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	focus := NewFocusMachine()
	a.SetFocusMachine(focus)

	// Simulate SpawnWorker inserting the worker AFTER the operator pressed `w`:
	// first the bridge queues the (not-yet-existent) role id, then the row lands
	// in the DB, then the broadcaster-driven repopulate runs.
	worker, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "new-worker", Kind: db.KindWorker, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: worker.ID, ArgusTaskID: "w9", WorktreePath: "/w9"})

	// Queue the select. (Order vs. insert does not matter — the apply happens at
	// repopulate; queue it now to mirror the bridge's post-spawn call.)
	a.QueueSelectRole(worker.ID)

	// Repopulate the rail (what the DAO broadcaster triggers ~100ms after the
	// inserts). Call populateRail directly to avoid QueueUpdateDraw blocking on
	// a non-running event loop in the unit test.
	if err := a.populateRail(d); err != nil {
		t.Fatalf("populateRail: %v", err)
	}

	// The queued row must now be the rail selection.
	ref, ok := a.pieces.rail.CurrentRef().(*roleEntry)
	if !ok || ref.RoleID != worker.ID {
		t.Fatalf("auto-select after repopulate: want rail on worker role %d; got %T %+v",
			worker.ID, a.pieces.rail.CurrentRef(), a.pieces.rail.CurrentRef())
	}

	// Focus must be untouched (RAIL).
	if focus.State() != FocusRAIL {
		t.Fatalf("auto-select must keep focus in RAIL; got %s", focus.State())
	}

	// The pending id must be consumed — a second repopulate must NOT re-steer
	// selection (move the cursor elsewhere first, then repopulate, and confirm
	// it stays where the operator left it).
	a.pieces.rail.SelectByOrchID(orch.ID) // move cursor off the worker
	if err := a.populateRail(d); err != nil {
		t.Fatalf("populateRail (second): %v", err)
	}
	if _, stillWorker := a.pieces.rail.CurrentRef().(*roleEntry); stillWorker {
		if ref2, _ := a.pieces.rail.CurrentRef().(*roleEntry); ref2 != nil && ref2.RoleID == worker.ID {
			t.Fatal("pending select must be consumed; a later repopulate re-selected the worker")
		}
	}
}

// TestApp_QueueSelectRole_AbandonsUnresolvableAfterBound proves the pending
// id does not hijack future repopulates forever: a role id that never appears
// is dropped after maxPendingSelectMisses repopulates.
func TestApp_QueueSelectRole_AbandonsUnresolvableAfterBound(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	coord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "proj-coord", Kind: db.KindCoordinator, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coord.ID, ArgusTaskID: "c1", WorktreePath: "/c"})

	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Queue a role id that will never exist.
	a.QueueSelectRole(999999)

	for i := 0; i < maxPendingSelectMisses; i++ {
		if err := a.populateRail(d); err != nil {
			t.Fatalf("populateRail attempt %d: %v", i, err)
		}
	}

	a.selectMu.Lock()
	pending := a.pendingSelectRoleID
	misses := a.pendingSelectMisses
	a.selectMu.Unlock()
	if pending != 0 {
		t.Fatalf("unresolvable pending select must be abandoned after %d repopulates; still %d",
			maxPendingSelectMisses, pending)
	}
	// No unbounded retry: the miss counter is reset (not climbing) and a
	// further repopulate touches nothing — the pending id is fully dropped.
	if misses != 0 {
		t.Fatalf("after abandonment the miss counter must reset; got %d", misses)
	}
	if err := a.populateRail(d); err != nil {
		t.Fatalf("populateRail (post-abandon): %v", err)
	}
	a.selectMu.Lock()
	pendingAfter := a.pendingSelectRoleID
	a.selectMu.Unlock()
	if pendingAfter != 0 {
		t.Fatalf("post-abandon repopulate must not resurrect the pending select; got %d", pendingAfter)
	}
}

// TestApp_QueueSelectRole_FreshBudgetPerQueue proves each newly-queued select
// gets the full miss budget: queue A (never appears), burn most of its budget,
// resolve nothing, then queue B — B must survive maxPendingSelectMisses
// repopulates of its own (the prior queue's burned misses must not carry over).
func TestApp_QueueSelectRole_FreshBudgetPerQueue(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	coord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "proj-coord", Kind: db.KindCoordinator, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coord.ID, ArgusTaskID: "c1", WorktreePath: "/c"})

	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Queue A and burn maxPendingSelectMisses-1 misses (one short of abandon).
	a.QueueSelectRole(999999)
	for i := 0; i < maxPendingSelectMisses-1; i++ {
		if err := a.populateRail(d); err != nil {
			t.Fatalf("populateRail (A) attempt %d: %v", i, err)
		}
	}

	// Now queue B — a different never-arriving id. The reset must give B the
	// FULL budget. If the burned misses carried over, B would be abandoned after
	// a single repopulate.
	a.QueueSelectRole(888888)

	a.selectMu.Lock()
	missesAfterRequeue := a.pendingSelectMisses
	a.selectMu.Unlock()
	if missesAfterRequeue != 0 {
		t.Fatalf("QueueSelectRole must reset the miss counter; got %d", missesAfterRequeue)
	}

	// B survives one more than A had survived (proving fresh budget).
	for i := 0; i < maxPendingSelectMisses-1; i++ {
		if err := a.populateRail(d); err != nil {
			t.Fatalf("populateRail (B) attempt %d: %v", i, err)
		}
	}
	a.selectMu.Lock()
	pendingB := a.pendingSelectRoleID
	a.selectMu.Unlock()
	if pendingB != 888888 {
		t.Fatalf("queue B must retain its full budget across %d repopulates; pending=%d",
			maxPendingSelectMisses-1, pendingB)
	}
}

// TestApp_CurrentRailSelection_AgentRow_CarriesCoordRoleID drives the REAL
// CurrentRailSelection (not the fake selector) for a worker/agent row and
// asserts it carries the owning orchestrator's coord role id. This is the
// signal OnNewWorker's selRole branch needs; without it `w` on an agent row
// resolves CoordRoleID==0 and the spawn dies as "not applicable" (the
// production face of delta scenario "w resolves an agent selection to its
// coordinator").
func TestApp_CurrentRailSelection_AgentRow_CarriesCoordRoleID(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	coord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "proj-coord", Kind: db.KindCoordinator, ArgusProject: "proj"})
	worker, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coord.ID, ArgusTaskID: "tc", WorktreePath: "/c"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: worker.ID, ArgusTaskID: "tw", WorktreePath: "/w"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	if !a.pieces.rail.SelectByRoleID(worker.ID) {
		t.Fatalf("could not select worker row")
	}
	sel := a.CurrentRailSelection()
	if sel.Kind != selRole || sel.RoleID != worker.ID {
		t.Fatalf("selection should be the worker role row; got %+v", sel)
	}
	if sel.OrchestratorID != orch.ID {
		t.Fatalf("OrchestratorID: want %d, got %d", orch.ID, sel.OrchestratorID)
	}
	if sel.CoordRoleID != coord.ID {
		t.Fatalf("CoordRoleID: want the owning coord role %d, got %d (w on an agent row would die as not-applicable)", coord.ID, sel.CoordRoleID)
	}
}

// TestApp_CurrentRailSelection_SubCoordRow_TargetsItself drives the REAL
// CurrentRailSelection for a promoted sub-coordinator row and asserts it
// carries (a) RoleKind == coordinator, (b) its OWN role id as the coord-role
// target, and (c) the CHILD orchestrator's id — so OnNewWorker spawns the
// worker under the sub-coordinator (the child orchestrator), not the parent.
// Delta D2: "a coordinator row (root OR a sub-coordinator role row) targets
// that coordinator."
func TestApp_CurrentRailSelection_SubCoordRow_TargetsItself(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	parent, _ := d.Orchestrators.Create(ctx, "parent")
	parentCoord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: parent.ID, Name: "parent-coord", Kind: db.KindCoordinator, ArgusProject: "p"})
	subWorker, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: parent.ID, Name: "sub", Kind: db.KindWorker, ArgusProject: "p"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: parentCoord.ID, ArgusTaskID: "t-parent-coord", WorktreePath: "/pc"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: subWorker.ID, ArgusTaskID: "t-sub", WorktreePath: "/sub"})

	child, _ := d.Orchestrators.Create(ctx, "child")
	childCoord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: child.ID, Name: "child-coord", Kind: db.KindCoordinator, ArgusProject: "p"})
	// The sub worker's task IS the child orchestrator's coord task (multi-binding).
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: childCoord.ID, ArgusTaskID: "t-sub", WorktreePath: "/sub"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	if !a.pieces.rail.SelectByRoleID(subWorker.ID) {
		t.Fatalf("could not select the sub-coordinator row")
	}
	sel := a.CurrentRailSelection()
	if sel.Kind != selRole {
		t.Fatalf("sub-coord selection must be a role row; got kind %v", sel.Kind)
	}
	if sel.RoleKind != string(db.KindCoordinator) {
		t.Fatalf("promoted sub-coord row must report coordinator kind; got %q", sel.RoleKind)
	}
	if sel.RoleID != subWorker.ID {
		t.Fatalf("RoleID: want the sub worker role %d, got %d", subWorker.ID, sel.RoleID)
	}
	// The child orchestrator id must be carried so OnNewWorker can target it.
	if sel.ChildOrchestratorID != child.ID {
		t.Fatalf("ChildOrchestratorID: want the child orchestrator %d, got %d (sub-coord row would spawn under the PARENT)", child.ID, sel.ChildOrchestratorID)
	}
}

// TestApp_OnNewWorker_AgentRow_SpawnsUnderCoord_RealSelection wires the REAL
// App selection into the mutation bridge (over a fake mutationService that only
// records the resolved SpawnWorkerInput) and proves `w` on a worker row spawns
// under that worker's coordinator — the integration the fake-selector unit test
// could not catch.
func TestApp_OnNewWorker_AgentRow_SpawnsUnderCoord_RealSelection(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	coord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "proj-coord", Kind: db.KindCoordinator, ArgusProject: "proj"})
	worker, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coord.ID, ArgusTaskID: "tc", WorktreePath: "/c"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: worker.ID, ArgusTaskID: "tw", WorktreePath: "/w"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	if !a.pieces.rail.SelectByRoleID(worker.ID) {
		t.Fatalf("could not select worker row")
	}

	m := &fakeModals{stubInputAnswer: "do work"}
	svc := &fakeMutationService{}
	// Wire the REAL App as the selector; bridge resolves the target from it.
	b := newMutationBridge(context.Background(), m, a, svc, &fakeListAll{}, &fakeRepopulator{}, nil, nil)

	b.OnNewWorker()
	b.waitIdle()

	svc.mu.Lock()
	calls := svc.spawnWorkerCalls
	svc.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("w on a worker row must spawn (got %d SpawnWorker calls); the CoordRoleID==0 guard would no-op", len(calls))
	}
	if calls[0].TargetOrchestratorID != orch.ID {
		t.Fatalf("TargetOrchestratorID: want %d, got %d", orch.ID, calls[0].TargetOrchestratorID)
	}
	if calls[0].CoordRoleID != coord.ID {
		t.Fatalf("CoordRoleID: want %d, got %d", coord.ID, calls[0].CoordRoleID)
	}
}

// TestApp_OnNewWorker_SubCoordRow_SpawnsUnderChild_RealSelection proves `w` on
// a sub-coordinator row spawns under the CHILD orchestrator (the sub-coord
// itself), using the real App selection.
func TestApp_OnNewWorker_SubCoordRow_SpawnsUnderChild_RealSelection(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	parent, _ := d.Orchestrators.Create(ctx, "parent")
	parentCoord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: parent.ID, Name: "parent-coord", Kind: db.KindCoordinator, ArgusProject: "p"})
	subWorker, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: parent.ID, Name: "sub", Kind: db.KindWorker, ArgusProject: "p"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: parentCoord.ID, ArgusTaskID: "t-parent-coord", WorktreePath: "/pc"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: subWorker.ID, ArgusTaskID: "t-sub", WorktreePath: "/sub"})

	child, _ := d.Orchestrators.Create(ctx, "child")
	childCoord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: child.ID, Name: "child-coord", Kind: db.KindCoordinator, ArgusProject: "p"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: childCoord.ID, ArgusTaskID: "t-sub", WorktreePath: "/sub"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	if !a.pieces.rail.SelectByRoleID(subWorker.ID) {
		t.Fatalf("could not select sub-coordinator row")
	}

	m := &fakeModals{stubInputAnswer: "do work"}
	svc := &fakeMutationService{}
	b := newMutationBridge(context.Background(), m, a, svc, &fakeListAll{}, &fakeRepopulator{}, nil, nil)

	b.OnNewWorker()
	b.waitIdle()

	svc.mu.Lock()
	calls := svc.spawnWorkerCalls
	svc.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("w on a sub-coordinator row must spawn; got %d SpawnWorker calls", len(calls))
	}
	if calls[0].TargetOrchestratorID != child.ID {
		t.Fatalf("sub-coord spawn must target the CHILD orchestrator %d; got %d (would nest under the parent)", child.ID, calls[0].TargetOrchestratorID)
	}
	if calls[0].CoordRoleID != subWorker.ID {
		t.Fatalf("sub-coord spawn must use the sub-coord's OWN role %d as coord; got %d", subWorker.ID, calls[0].CoordRoleID)
	}
}

// TestApp_OnNewWorker_ArchivedAgentRow_SpawnsUnderCoord_RealSelection proves a
// `w` press on an ARCHIVED agent row still resolves to its (valid) coordinator
// and spawns — the selected row's archived state must not block resolution
// (spec: "an archived or dead agent row still resolves to its coordinator").
// Drives the REAL App selection: archive the worker, reveal it via the Archive
// expando, select it, run the bridge over a recording fake svc.
func TestApp_OnNewWorker_ArchivedAgentRow_SpawnsUnderCoord_RealSelection(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	coord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "proj-coord", Kind: db.KindCoordinator, ArgusProject: "proj"})
	worker, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coord.ID, ArgusTaskID: "tc", WorktreePath: "/c"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: worker.ID, ArgusTaskID: "tw", WorktreePath: "/w"})
	if err := d.Roles.Archive(ctx, worker.ID); err != nil {
		t.Fatalf("archive worker role: %v", err)
	}

	a, err := BuildApp(d, &alivePaneSource{alive: map[string]bool{"tc": true, "tw": true}})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Reveal the archived row (it lives behind the per-coordinator Archive
	// expando) so the cursor can land on it.
	a.SetShowArchived(true)
	if err := a.populateRail(d); err != nil {
		t.Fatalf("populateRail: %v", err)
	}
	if !a.pieces.rail.SelectByRoleID(worker.ID) {
		t.Fatalf("could not select the archived worker row")
	}
	// Sanity: the row really is archived, and still carries its coordinator.
	sel := a.CurrentRailSelection()
	if !sel.Archived {
		t.Fatalf("expected the selected worker row to be archived; got %+v", sel)
	}
	if sel.CoordRoleID != coord.ID {
		t.Fatalf("archived worker row must still carry CoordRoleID %d; got %d", coord.ID, sel.CoordRoleID)
	}

	m := &fakeModals{stubInputAnswer: "do work"}
	svc := &fakeMutationService{}
	b := newMutationBridge(context.Background(), m, a, svc, &fakeListAll{}, &fakeRepopulator{}, nil, nil)
	b.OnNewWorker()
	b.waitIdle()

	svc.mu.Lock()
	calls := svc.spawnWorkerCalls
	svc.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("w on an archived agent row must still spawn; got %d SpawnWorker calls", len(calls))
	}
	if calls[0].TargetOrchestratorID != orch.ID || calls[0].CoordRoleID != coord.ID {
		t.Fatalf("archived agent row spawn must target its coordinator (orch %d, coord %d); got orch %d coord %d",
			orch.ID, coord.ID, calls[0].TargetOrchestratorID, calls[0].CoordRoleID)
	}
}

// TestApp_OnNewWorker_DeadAgentRow_SpawnsUnderCoord_RealSelection proves a `w`
// press on a DEAD agent row (its argus task record gone — absent from a warm
// state cache) still resolves to its coordinator and spawns. Drives the REAL
// App selection.
func TestApp_OnNewWorker_DeadAgentRow_SpawnsUnderCoord_RealSelection(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	coord, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "proj-coord", Kind: db.KindCoordinator, ArgusProject: "proj"})
	worker, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coord.ID, ArgusTaskID: "tc", WorktreePath: "/c"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: worker.ID, ArgusTaskID: "tw", WorktreePath: "/w"})

	// Warm state cache that knows the coord task but NOT the worker task →
	// the worker row is DEAD (record gone). statePaneSource has no StatesReady
	// method, so taskGone treats the cache as warm.
	src := &statePaneSource{states: map[string]ArgusTaskState{"tc": {Status: "in_progress"}}}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Dead rows hide by default; reveal via the Archive expando.
	a.SetShowArchived(true)
	if err := a.populateRail(d); err != nil {
		t.Fatalf("populateRail: %v", err)
	}
	if !a.pieces.rail.SelectByRoleID(worker.ID) {
		t.Fatalf("could not select the dead worker row")
	}
	sel := a.CurrentRailSelection()
	if !sel.Dead {
		t.Fatalf("expected the selected worker row to be dead; got %+v", sel)
	}
	if sel.CoordRoleID != coord.ID {
		t.Fatalf("dead worker row must still carry CoordRoleID %d; got %d", coord.ID, sel.CoordRoleID)
	}

	m := &fakeModals{stubInputAnswer: "do work"}
	svc := &fakeMutationService{}
	b := newMutationBridge(context.Background(), m, a, svc, &fakeListAll{}, &fakeRepopulator{}, nil, nil)
	b.OnNewWorker()
	b.waitIdle()

	svc.mu.Lock()
	calls := svc.spawnWorkerCalls
	svc.mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("w on a dead agent row must still spawn; got %d SpawnWorker calls", len(calls))
	}
	if calls[0].TargetOrchestratorID != orch.ID || calls[0].CoordRoleID != coord.ID {
		t.Fatalf("dead agent row spawn must target its coordinator (orch %d, coord %d); got orch %d coord %d",
			orch.ID, coord.ID, calls[0].TargetOrchestratorID, calls[0].CoordRoleID)
	}
}

// IsFiltering delegates to the rail's input-mode flag so the KeyRouter's
// RailFilter gate (wired Filter: app in session.go) yields keys to the rail
// while the operator types a `/` query. (change rail-search)
func TestApp_IsFilteringDelegatesToRail(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	if a.IsFiltering() {
		t.Fatalf("rail must not be filtering initially")
	}
	a.pieces.rail.BeginFilter()
	if !a.IsFiltering() {
		t.Fatalf("IsFiltering must report true once the rail enters input mode")
	}
	a.pieces.rail.AcceptFilter()
	if a.IsFiltering() {
		t.Fatalf("IsFiltering must report false after the filter is accepted (input mode left)")
	}
}
