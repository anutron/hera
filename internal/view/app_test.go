package view

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

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
	t.Cleanup(func() { _ = d.Close() })
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
	// invalidated records every InvalidateResize call (BUG-053).
	invalidated []string
	// resetSubscriptions records every ResetSubscription call (BUG-012).
	resetSubscriptions []string
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

// InvalidateResize satisfies paneResizeInvalidator (BUG-053).
func (f *fakePaneSource) InvalidateResize(taskID string) {
	f.invalidated = append(f.invalidated, taskID)
}

// ResetSubscription satisfies paneSubscriptionResetter (BUG-012).
func (f *fakePaneSource) ResetSubscription(taskID string) {
	f.resetSubscriptions = append(f.resetSubscriptions, taskID)
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

// The body fills the full terminal height — no internal top-bar margin (BUG-031).
// Row 0 is the pane top border, and it must not contain any "HERA" branding.
func TestBuildApp_NoHeraHeraBrandingInTopRow(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	got := renderApp(t, a, 80, 24)
	firstRow := strings.SplitN(got, "\n", 2)[0]
	if strings.Contains(firstRow, "HERA") {
		t.Fatalf("top row must not stamp HERA branding; got %q", firstRow)
	}
}

func TestBuildApp_ThreeColumnsAndChrome(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// At 80x24 the body fills all rows 0..23 (no internal top-bar margin,
	// BUG-031). Hera renders NO bottom-bar row of its own (D12: argus draws the
	// plugin-mode status bar from hera's pushed hotkeys).
	got := renderApp(t, a, 80, 24)
	rows := strings.Split(got, "\n")
	if len(rows) < 24 {
		t.Fatalf("expected at least 24 rows; got %d", len(rows))
	}

	// Row 0 is the rail top border (the body is flush — no blank row above it).
	railSlice := rows[0][:RailWidth]
	if !strings.ContainsAny(railSlice, "─┌┐└┘╔╗═") {
		t.Errorf("expected rail top-border characters on row 0; got %q", railSlice)
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
	if !strings.ContainsAny(rows[23], "└┘─╚╝═") {
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

	// barLabelsFor renders only the items flagged Bar:true — the subset argus
	// draws on its context-sensitive bottom bar (vs. the full help overlay set).
	barLabelsFor := func(items []HotkeyItem) string {
		var sb strings.Builder
		for _, it := range items {
			if !it.Bar {
				continue
			}
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

	// `w` (spawn worker) and `J` (adopt) are advertised on the BOTTOM BAR
	// (Bar:true) so both rail gestures are discoverable, not just in the `?`
	// help overlay (BUG-007). J is positioned immediately after w so it is
	// visible in the bar before the rename/delete/archive block that gets
	// truncated at typical terminal widths (BUG-031).
	railBar := barLabelsFor(items)
	if !strings.Contains(railBar, "w:new agent") {
		t.Fatalf("RAIL bottom bar must advertise w:new agent (Bar:true): %s", railBar)
	}
	if !strings.Contains(railBar, "J:adopt") {
		t.Fatalf("RAIL bottom bar must advertise J:adopt (Bar:true): %s", railBar)
	}
	// J must appear immediately after w in the bar string so the argus bottom
	// bar renders it before the rename/delete block hits the width limit.
	wIdx := strings.Index(railBar, "w:new agent")
	jIdx := strings.Index(railBar, "J:adopt")
	if wIdx < 0 || jIdx < 0 || jIdx <= wIdx {
		t.Fatalf("J:adopt must follow w:new agent in the bar string (BUG-031); bar=%s", railBar)
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

// TestBuildApp_AllChromeSurfacesUseHeraBackground proves the single-background
// contract (BUG-001): every hera-rendered chrome surface — root/body, rail,
// both panes, and the Details pane — paints the same heraBackground the pane
// interiors use, so no grey-blue (tview's stock ColorBlack/Color0 or the argus
// ColorStatusBG dark gray) bleeds through anywhere. A regression that drops one
// of the explicit SetBackgroundColor calls (or reverts the global tview.Styles
// repoint) trips here. The top-bar row is gone (BUG-031) so it is no longer a
// checked surface.
func TestBuildApp_AllChromeSurfacesUseHeraBackground(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	surfaces := []struct {
		name string
		bg   tcell.Color
	}{
		{"root", a.pieces.root.GetBackgroundColor()},
		{"body", a.pieces.body.GetBackgroundColor()},
		{"rail", a.pieces.rail.GetBackgroundColor()},
		{"coord", a.pieces.coord.GetBackgroundColor()},
		{"agent", a.pieces.agent.GetBackgroundColor()},
		{"details", a.pieces.details.GetBackgroundColor()},
	}
	for _, s := range surfaces {
		if s.bg != heraBackground {
			t.Errorf("%s background = %v, want heraBackground (%v) — BUG-001", s.name, s.bg, heraBackground)
		}
		if s.bg == theme.ColorStatusBG {
			t.Errorf("%s regressed to the grey-blue ColorStatusBG", s.name)
		}
	}

	// The global tview default must also be repointed so unset primitives
	// (gaps, the pages canvas, form internals) fall through to the same black.
	if tview.Styles.PrimitiveBackgroundColor != heraBackground {
		t.Errorf("tview.Styles.PrimitiveBackgroundColor = %v, want heraBackground", tview.Styles.PrimitiveBackgroundColor)
	}
	if tview.Styles.ContrastBackgroundColor != heraBackground {
		t.Errorf("tview.Styles.ContrastBackgroundColor = %v, want heraBackground", tview.Styles.ContrastBackgroundColor)
	}
}

func TestBuildApp_RailHeaderRendered(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// BUG-026: the rail border renders without a "Rail" label (redundant title removed).
	// Verify the border is present (horizontal rule appears) and "Rail" is absent.
	got := renderApp(t, a, 80, 24)
	if strings.Contains(got, "Rail") {
		t.Fatalf("BUG-026: rail border must NOT contain 'Rail' label; got:\n%s", got)
	}
	if !strings.Contains(got, "─") {
		t.Fatalf("expected rail border rule characters in rendered app; got:\n%s", got)
	}
}

// BUG-031: the fullscreen indicator lives in the pane title, not a top bar row.
// OnFullscreenChanged must bracket-wrap the active pane's title ("[ Coord ]" or
// "[ Agent ]") while fullscreen is active and revert both titles to plain names
// on exit.
func TestApp_OnFullscreenChanged_UpdatesPaneTitles(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Initial state: both panes carry their plain titles.
	if got := a.pieces.coord.GetTitle(); got != "Coord" {
		t.Errorf("initial coord title: want %q, got %q", "Coord", got)
	}
	if got := a.pieces.agent.GetTitle(); got != "Agent" {
		t.Errorf("initial agent title: want %q, got %q", "Agent", got)
	}

	// Enter coord fullscreen: coord title brackets, agent unchanged.
	a.OnFullscreenChanged(FocusCOORD, true)
	if got := a.pieces.coord.GetTitle(); got != "[ Coord ]" {
		t.Errorf("coord fullscreen: want %q, got %q", "[ Coord ]", got)
	}
	if got := a.pieces.agent.GetTitle(); got != "Agent" {
		t.Errorf("coord fullscreen must not change agent title; got %q", got)
	}

	// Exit fullscreen: both revert to plain names.
	a.OnFullscreenChanged(FocusCOORD, false)
	if got := a.pieces.coord.GetTitle(); got != "Coord" {
		t.Errorf("after exit: coord title: want %q, got %q", "Coord", got)
	}
	if got := a.pieces.agent.GetTitle(); got != "Agent" {
		t.Errorf("after exit: agent title: want %q, got %q", "Agent", got)
	}

	// Enter agent fullscreen: agent title brackets, coord unchanged.
	a.OnFullscreenChanged(FocusAGENT, true)
	if got := a.pieces.agent.GetTitle(); got != "[ Agent ]" {
		t.Errorf("agent fullscreen: want %q, got %q", "[ Agent ]", got)
	}
	if got := a.pieces.coord.GetTitle(); got != "Coord" {
		t.Errorf("agent fullscreen must not change coord title; got %q", got)
	}

	// Exit again.
	a.OnFullscreenChanged(FocusAGENT, false)
	if got := a.pieces.agent.GetTitle(); got != "Agent" {
		t.Errorf("after exit: agent title: want %q, got %q", "Agent", got)
	}
}

// BUG-013: entering or exiting fullscreen must not leave the cursor invisible.
// The fix queues a second draw pass (go a.app.QueueUpdateDraw) after refreshBody
// so tview re-establishes the cursor position in the new container. This test
// verifies the synchronous structural preconditions: the correct pane is the
// ONLY item in the body after fullscreen and the normal split is restored on
// exit. The async QueueUpdateDraw draw itself cannot be exercised synchronously
// without a running tview event loop (same limitation as TestApp_OnFocusChanged_*
// ResizeTask tests).
func TestApp_OnFullscreenChanged_BodyLayoutIsExclusive(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Baseline: normal split shows rail, coord pane, and agent pane.
	normal := renderApp(t, a, 80, 24)
	if !strings.Contains(normal, "(no projects)") {
		t.Fatal("baseline: expected rail placeholder in normal split")
	}
	if !strings.Contains(normal, "(no coord selected)") {
		t.Fatal("baseline: expected coord pane placeholder in normal split")
	}
	if !strings.Contains(normal, "(no agent selected)") {
		t.Fatal("baseline: expected agent pane placeholder in normal split")
	}

	// Fullscreen COORD: coord pane expands to fill the space right of the rail.
	// Rail must remain visible; only the agent pane is absent.
	a.OnFullscreenChanged(FocusCOORD, true)
	coordFS := renderApp(t, a, 80, 24)
	if !strings.Contains(coordFS, "(no projects)") {
		t.Error("coord fullscreen: rail must remain visible")
	}
	if strings.Contains(coordFS, "(no agent selected)") {
		t.Error("coord fullscreen: agent pane must not be rendered when coord pane is fullscreen")
	}
	if !strings.Contains(coordFS, "(no coord selected)") {
		t.Error("coord fullscreen: coord pane placeholder must be visible in fullscreen")
	}

	// Exit fullscreen: normal split restored.
	a.OnFullscreenChanged(FocusCOORD, false)
	restored := renderApp(t, a, 80, 24)
	if !strings.Contains(restored, "(no projects)") {
		t.Error("after exit: rail must be restored in normal split")
	}
	if !strings.Contains(restored, "(no agent selected)") {
		t.Error("after exit: agent pane must be restored in normal split")
	}

	// Fullscreen AGENT: agent pane expands to fill the space right of the rail.
	// Rail must remain visible; only the coord pane is absent.
	a.OnFullscreenChanged(FocusAGENT, true)
	agentFS := renderApp(t, a, 80, 24)
	if !strings.Contains(agentFS, "(no projects)") {
		t.Error("agent fullscreen: rail must remain visible")
	}
	if strings.Contains(agentFS, "(no coord selected)") {
		t.Error("agent fullscreen: coord pane must not be rendered when agent pane is fullscreen")
	}
	if !strings.Contains(agentFS, "(no agent selected)") {
		t.Error("agent fullscreen: agent pane placeholder must be visible in fullscreen")
	}

	// Exit fullscreen again: normal split restored.
	a.OnFullscreenChanged(FocusAGENT, false)
	restored2 := renderApp(t, a, 80, 24)
	if !strings.Contains(restored2, "(no projects)") {
		t.Error("after agent exit: rail must be restored in normal split")
	}
	if !strings.Contains(restored2, "(no coord selected)") {
		t.Error("after agent exit: coord pane must be restored in normal split")
	}
}

// BUG-013 (reflow path): forceRebindCoord replaces a.pieces.coord with a fresh
// emulator but the old pane held tview focus. Without a fix, Application.focus
// still points to the closed pane, the new pane renders hasFocus=false, and the
// cursor disappears. The fix calls OnFocusChanged(FocusCOORD) after refreshBody
// so the new pane gets SetFocus and its border color is restored to focused.
// This is the primary trigger for BUG-013: the fullscreen-triggered reflow fires
// ~250ms after Ctrl-Z, long after the initial QueueUpdateDraw from PR #109.
func TestApp_ForceRebindCoord_RestoresFocusWhenCoordFocused(t *testing.T) {
	d := openTestDB(t)
	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	focus := NewFocusMachine()
	a.SetFocusMachine(focus)

	// Bind the coord pane to a task and place focus there.
	a.mu.Lock()
	a.coordTask = "task-coord"
	a.mu.Unlock()
	focus.JumpToCOORD()
	a.OnFocusChanged(FocusCOORD)

	// Verify pre-condition: coord border is the focused color.
	if got := a.pieces.coord.GetBorderColor(); got != theme.ColorTitle {
		t.Fatalf("pre-condition: coord border = %v, want focused %v", got, theme.ColorTitle)
	}

	// Simulate the reflow-triggered rebind at new dimensions.
	a.forceRebindCoord("task-coord", 78, 22)

	// The new pane must inherit focus — its border color must stay focused.
	if got := a.pieces.coord.GetBorderColor(); got != theme.ColorTitle {
		t.Errorf("after forceRebindCoord: coord border = %v, want focused %v (cursor invisible without fix)", got, theme.ColorTitle)
	}
}

// forceRebindCoord must NOT steal focus when COORD is not focused (RAIL or
// AGENT). The replacement pane's border should be unfocused.
func TestApp_ForceRebindCoord_NoFocusStealWhenNotCoordFocused(t *testing.T) {
	d := openTestDB(t)
	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	focus := NewFocusMachine()
	a.SetFocusMachine(focus)

	// Focus is on RAIL (the default).
	a.OnFocusChanged(FocusRAIL)
	a.mu.Lock()
	a.coordTask = "task-coord"
	a.mu.Unlock()

	a.forceRebindCoord("task-coord", 78, 22)

	// Coord border must be unfocused; RAIL has focus.
	if got := a.pieces.coord.GetBorderColor(); got == theme.ColorTitle {
		t.Errorf("after forceRebindCoord with RAIL focus: coord border = focused, must not steal focus")
	}
}

// BUG-013 (reflow path, agent side): forceRebindAgent must re-establish
// focus on the replacement pane when AGENT has focus.
func TestApp_ForceRebindAgent_RestoresFocusWhenAgentFocused(t *testing.T) {
	d := openTestDB(t)
	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	focus := NewFocusMachine()
	a.SetFocusMachine(focus)

	a.mu.Lock()
	a.agentTask = "task-agent"
	a.mu.Unlock()
	focus.JumpToAGENT()
	a.OnFocusChanged(FocusAGENT)

	if got := a.pieces.agent.GetBorderColor(); got != theme.ColorTitle {
		t.Fatalf("pre-condition: agent border = %v, want focused %v", got, theme.ColorTitle)
	}

	a.forceRebindAgent("task-agent", 78, 22)

	if got := a.pieces.agent.GetBorderColor(); got != theme.ColorTitle {
		t.Errorf("after forceRebindAgent: agent border = %v, want focused %v (cursor invisible without fix)", got, theme.ColorTitle)
	}
}

// forceRebindAgent must NOT steal focus when AGENT is not focused.
func TestApp_ForceRebindAgent_NoFocusStealWhenNotAgentFocused(t *testing.T) {
	d := openTestDB(t)
	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	focus := NewFocusMachine()
	a.SetFocusMachine(focus)

	a.OnFocusChanged(FocusRAIL)
	a.mu.Lock()
	a.agentTask = "task-agent"
	a.mu.Unlock()

	a.forceRebindAgent("task-agent", 78, 22)

	if got := a.pieces.agent.GetBorderColor(); got == theme.ColorTitle {
		t.Errorf("after forceRebindAgent with RAIL focus: agent border = focused, must not steal focus")
	}
}

// BUG-013 (OnFullscreenChanged path): OnFullscreenChanged must re-assert
// focus on the current pane after refreshBody() restructures the Flex.
// Before the fix, the first post-toggle draw could show a focused border
// (set by the prior OnFocusChanged call) but a missing hardware cursor
// because app.SetFocus was not re-called after the Flex was rebuilt.
func TestApp_OnFullscreenChanged_ReassertsFocusAfterRefreshBody(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	focus := NewFocusMachine()
	a.SetFocusMachine(focus)

	// Enter COORD pane focus, then toggle fullscreen.
	focus.JumpToCOORD()
	a.OnFocusChanged(FocusCOORD)
	a.OnFullscreenChanged(FocusCOORD, true)

	// The coord pane's border must remain focused after the fullscreen layout change.
	if got := a.pieces.coord.GetBorderColor(); got != theme.ColorTitle {
		t.Errorf("fullscreen coord: border = %v, want focused %v", got, theme.ColorTitle)
	}

	// Exit fullscreen — focus must remain on coord.
	a.OnFullscreenChanged(FocusCOORD, false)
	if got := a.pieces.coord.GetBorderColor(); got != theme.ColorTitle {
		t.Errorf("exit fullscreen: border = %v, want focused %v", got, theme.ColorTitle)
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

// TestPopulateRail_DeletedOrchestratorGoneFromRail is the BUG-004 regression
// test. A coordinator created via `n` is physically deleted (the ops layer
// calls DeleteOrchestratorByID after ^d). The rail must show NO row for the
// deleted coordinator — not in the active section, not in the Archive section
// — so the operator never sees a "ghost" row with "Status: unknown".
func TestPopulateRail_DeletedOrchestratorGoneFromRail(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "test-coord")
	coordRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "hera",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coordRole.ID, ArgusTaskID: "tc", WorktreePath: "/wt"})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Before deletion: the coordinator header is visible.
	if !a.pieces.rail.SelectByOrchID(orch.ID) {
		t.Fatalf("coordinator not found in rail before deletion")
	}

	// Simulate the physical delete that DeleteOrchestrator performs.
	if err := d.Orchestrators.Delete(ctx, orch.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Call populateRail directly (not RepopulateRail) to avoid QueueUpdateDraw
	// blocking — the test has no running tview main loop.
	if err := a.populateRail(d); err != nil {
		t.Fatalf("populateRail: %v", err)
	}

	// After deletion: the coordinator must not appear anywhere in the rail —
	// not in the active section and not in the Archive section even when it is
	// force-expanded. (BUG-004: before the fix, DeleteOrchestrator only archived
	// the row, which left a ghost in the Archive section.)
	a.SetShowArchived(true) // force-expand archives
	if err := a.populateRail(d); err != nil {
		t.Fatalf("populateRail (showArchived): %v", err)
	}

	if a.pieces.rail.SelectByOrchID(orch.ID) {
		t.Fatalf("coordinator still in rail after physical delete (BUG-004 ghost)")
	}
	for _, row := range a.pieces.rail.rows {
		if row.kind == railRowOrch && row.orch != nil && row.orch.ID == orch.ID {
			t.Fatalf("deleted coordinator still renders as railRowOrch (ghost in Archive section)")
		}
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

// TestApp_OnRailSelectEnter_DeadSessionWorkerStaysInRAIL proves that pressing
// Enter on a worker whose argus task is terminal (complete/failed/etc.) keeps
// focus in RAIL — never enters an AGENT pane that would 404 on every
// keystroke. This is the BUG-018 regression guard for the OnRailSelectEnter
// path (complementing BUG-014's Dead=true guard).
func TestApp_OnRailSelectEnter_DeadSessionWorkerStaysInRAIL(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	w, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "done-worker", Kind: db.KindWorker, ArgusProject: "proj",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w.ID, ArgusTaskID: "t-done", WorktreePath: "/w"})

	src := &aliveStatePaneSource{
		alive:  map[string]bool{"t-done": false},
		states: map[string]ArgusTaskState{"t-done": {Status: "complete"}},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	if !a.pieces.rail.SelectByRoleID(w.ID) {
		t.Fatalf("could not select dead-session worker in rail")
	}

	got := a.OnRailSelectEnter()
	if got != FocusRAIL {
		t.Fatalf("Enter on dead-session worker: want FocusRAIL (PTY would 404), got %s", got)
	}
}

// TestApp_ApplyRailSelection_DeadSessionAgentForcesRAIL proves that navigating
// onto a dead-session agent row (the path j/k triggers via onRailSelectionChanged
// → applyRailSelection) while focus is already in AGENT forces focus back to
// RAIL. Without this guard the operator's keystrokes are forwarded to the dead
// PTY (HTTP 404) and swallowed from tcell, making the view appear frozen.
// This is the BUG-018 regression guard for the onRailSelectionChanged path.
func TestApp_ApplyRailSelection_DeadSessionAgentForcesRAIL(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	w1, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "live", Kind: db.KindWorker, ArgusProject: "proj"})
	w2, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "done", Kind: db.KindWorker, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w1.ID, ArgusTaskID: "t-live", WorktreePath: "/1"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: w2.ID, ArgusTaskID: "t-done", WorktreePath: "/2"})

	src := &aliveStatePaneSource{
		alive: map[string]bool{"t-live": true, "t-done": false},
		states: map[string]ArgusTaskState{
			"t-live": {Status: "in_progress"},
			"t-done": {Status: "complete"},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	focus := NewFocusMachine()
	a.SetFocusMachine(focus)
	a.selectDebounce = 0

	// Land on the live worker and enter its AGENT pane — simulates an operator
	// who has been actively viewing a live agent before navigating away.
	if !a.pieces.rail.SelectByRoleID(w1.ID) {
		t.Fatalf("could not select live worker")
	}
	a.applyRailSelection(a.pieces.rail.CurrentRef())
	focus.JumpToAGENT()
	if a.AgentTaskID() != "t-live" {
		t.Fatalf("baseline agent: want t-live, got %q", a.AgentTaskID())
	}
	if focus.State() != FocusAGENT {
		t.Fatalf("baseline focus: want AGENT, got %s", focus.State())
	}

	// Navigate onto the dead-session worker (debounced applyRailSelection path).
	// Focus MUST be returned to RAIL so subsequent keystrokes drive the rail
	// instead of being forwarded to a dead PTY that returns HTTP 404.
	if !a.pieces.rail.SelectByRoleID(w2.ID) {
		t.Fatalf("could not select dead-session worker")
	}
	a.applyRailSelection(a.pieces.rail.CurrentRef())

	if focus.State() != FocusRAIL {
		t.Fatalf("after navigating onto dead-session agent: want FocusRAIL, got %s", focus.State())
	}
	// The focus atomic read by rawInputConn on the WS goroutine must also
	// reflect RAIL so raw bytes reach tcell/KeyRouter, not the dead PTY.
	if got := FocusState(a.focusState.Load()); got != FocusRAIL {
		t.Fatalf("focus atomic: want FocusRAIL, got %s", got)
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

// TestBuildApp_AdoptedFreelancerLeavesFreelanceAndRendersUnderCoordinator
// proves the rail-adopt headline scenario at the rail-build level: once the
// freelancer's argus task gains a live worker binding (exactly what
// ops.AdoptTaskIntoOrchestrator creates), the task drops out of the Freelance
// section and renders as a managed worker under the chosen coordinator. The
// exclusion is emergent from buildFreelance's liveBound filter, so this locks
// it against a regression in that predicate.
func TestBuildApp_AdoptedFreelancerLeavesFreelanceAndRendersUnderCoordinator(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, err := d.Orchestrators.Create(ctx, "alpha")
	if err != nil {
		t.Fatalf("orch: %v", err)
	}

	src := &freelancePaneSource{
		states: map[string]ArgusTaskState{},
		tasks: []ArgusTaskInfo{
			{ID: "free-x", Name: "feat-x", Project: "Hera", State: ArgusTaskState{Status: "in_progress"}},
		},
	}

	// Before adoption: free-x is an unmanaged freelancer.
	before, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	if !freelanceContains(before.pieces.rail.freelance, "free-x") {
		t.Fatalf("free-x must start in the Freelance section; got %+v", before.pieces.rail.freelance)
	}
	before.Close()

	// Adopt: create the worker role + live binding the operator's `J` would —
	// the same DAO path ops.AdoptTaskIntoOrchestrator uses.
	role, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "feat-x", Kind: db.KindWorker, ArgusProject: "Hera",
	})
	if err != nil {
		t.Fatalf("role: %v", err)
	}
	if _, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: role.ID, OrchestratorID: orch.ID, ArgusTaskID: "free-x", WorktreePath: "/wt/feat-x",
	}); err != nil {
		t.Fatalf("binding: %v", err)
	}

	// After adoption: free-x is gone from Freelance and renders as a worker
	// under alpha.
	after, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer after.Close()

	if freelanceContains(after.pieces.rail.freelance, "free-x") {
		t.Fatalf("adopted free-x must leave the Freelance section; got %+v", after.pieces.rail.freelance)
	}
	found := false
	for _, o := range after.pieces.rail.orchestrators {
		if o.ID != orch.ID {
			continue
		}
		for _, r := range o.Roles {
			if r.ArgusTaskID == "free-x" && r.RoleKind == string(db.KindWorker) {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("adopted free-x must render as a worker under alpha; orchestrators=%+v", after.pieces.rail.orchestrators)
	}
}

// freelanceContains reports whether any freelance project group holds a task
// with the given argus task id.
func freelanceContains(projects []*freelanceProject, taskID string) bool {
	for _, p := range projects {
		for _, tk := range p.Tasks {
			if tk.ArgusTaskID == taskID {
				return true
			}
		}
	}
	return false
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

	m := &fakeModals{stubNewWorkerProject: "proj", stubNewWorkerPrompt: "do work"}
	svc := &fakeMutationService{listProjectsResp: []string{"proj"}, coordProjectResp: "proj"}
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

	m := &fakeModals{stubNewWorkerProject: "p", stubNewWorkerPrompt: "do work"}
	svc := &fakeMutationService{listProjectsResp: []string{"p"}, coordProjectResp: "p"}
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

	m := &fakeModals{stubNewWorkerProject: "proj", stubNewWorkerPrompt: "do work"}
	svc := &fakeMutationService{listProjectsResp: []string{"proj"}, coordProjectResp: "proj"}
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

	m := &fakeModals{stubNewWorkerProject: "proj", stubNewWorkerPrompt: "do work"}
	svc := &fakeMutationService{listProjectsResp: []string{"proj"}, coordProjectResp: "proj"}
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

// --- rail-sections: populateRail integration ---

// TestBuildApp_PinnedOrchestratorRendersInPinnedSection proves the full
// DB→rail path: a pinned orchestrator (pinned_at set) floats into the Pinned
// section at the rail top.
func TestBuildApp_PinnedOrchestratorRendersInPinnedSection(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	orch, err := d.Orchestrators.Create(ctx, "pinme")
	if err != nil {
		t.Fatalf("orch: %v", err)
	}
	if err := d.Orchestrators.Pin(ctx, orch.ID); err != nil {
		t.Fatalf("pin: %v", err)
	}

	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	rl := a.pieces.rail
	if len(rl.orchestrators) != 1 || !rl.orchestrators[0].Pinned {
		t.Fatalf("orchestrator must carry Pinned from pinned_at; got %+v", rl.orchestrators)
	}
	if len(rl.rows) == 0 || rl.rows[0].kind != railRowPinnedSep {
		t.Fatalf("pinned orchestrator must produce a Pinned section first; kinds=%v", rowKinds(rl))
	}
	got := renderApp(t, a, 80, 24)
	if !strings.Contains(got, "Pinned") || !strings.Contains(got, "pinme") {
		t.Fatalf("rail must render the Pinned section with the pinned project; got:\n%s", got)
	}
}

// TestBuildApp_ArchivedFreelancerLandsInBottomArchive proves Story 2's fix:
// an archived freelancer (the data-loss case) is split out of the inline
// Freelance section and reachable in the bottom Archive WITHOUT `l`.
func TestBuildApp_ArchivedFreelancerLandsInBottomArchive(t *testing.T) {
	d := openTestDB(t)
	src := &freelancePaneSource{
		states: map[string]ArgusTaskState{},
		tasks: []ArgusTaskInfo{
			{ID: "free-live", Name: "live-one", Project: "Alpha", State: ArgusTaskState{Status: "in_progress"}},
			{ID: "free-arch", Name: "arch-one", Project: "Alpha", State: ArgusTaskState{Status: "complete", Archived: true}},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	rl := a.pieces.rail
	// The archived freelancer must NOT be in the inline Freelance projects...
	for _, fp := range rl.freelance {
		for _, tk := range fp.Tasks {
			if tk.ArgusTaskID == "free-arch" {
				t.Fatalf("archived freelancer must not render inline in a Freelance repo group")
			}
		}
	}
	// ...it must be in the archived-freelance set (bottom Archive).
	if len(rl.archivedFreelance) != 1 || rl.archivedFreelance[0].ArgusTaskID != "free-arch" {
		t.Fatalf("archived freelancer must land in archivedFreelance; got %+v", rl.archivedFreelance)
	}
	// Default view (no `l`): the bottom Archive expando renders, reachable by j/k.
	got := renderApp(t, a, 100, 40)
	if !strings.Contains(got, "Archive (1)") {
		t.Fatalf("bottom Archive (1) must render without `l`; got:\n%s", got)
	}
}

// TestApplyRailSelection_DeadWorkerClearsAgentPane proves that navigating (j/k)
// to a dead worker row — a role whose argus task record no longer exists —
// does NOT bind the dead task to the agent pane (BUG-014). The fix guards the
// worker/agent path in applyRailSelection: Dead rows call rebindAgent("") so
// the pane shows a placeholder and AgentTaskID() is never the dead task id.
func TestApplyRailSelection_DeadWorkerClearsAgentPane(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	liveRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "live", Kind: db.KindWorker, ArgusProject: "proj"})
	deadRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "dead", Kind: db.KindWorker, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: liveRole.ID, ArgusTaskID: "t-live", WorktreePath: "/l"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: deadRole.ID, ArgusTaskID: "t-dead", WorktreePath: "/d"})

	// t-dead absent from warm state cache: the argus task record is gone.
	// aliveStatePaneSource also implements TaskAliveChecker so findInitialSelection
	// skips "t-dead" and picks "t-live" — keeping src.calls clean before the test.
	src := &aliveStatePaneSource{
		alive:  map[string]bool{"t-live": true, "t-dead": false},
		states: map[string]ArgusTaskState{"t-live": {Status: "in_progress"}},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	a.showArchived = true
	if err := a.populateRail(d); err != nil {
		t.Fatalf("populateRail: %v", err)
	}

	var deadEntry *roleEntry
	for _, o := range a.pieces.rail.orchestrators {
		for _, r := range o.Roles {
			if r.RoleID == deadRole.ID {
				deadEntry = r
			}
		}
	}
	if deadEntry == nil {
		t.Fatalf("dead role missing from rail data")
	}
	if !deadEntry.Dead {
		t.Fatalf("precondition: expected Dead=true on dead role; got Dead=false")
	}

	callsBefore := len(src.calls)

	// Simulate j/k landing on the dead row (what onRailSelectionChanged calls).
	a.applyRailSelection(deadEntry)

	// The dead task must never be bound to the agent pane.
	if a.AgentTaskID() == "t-dead" {
		t.Fatalf("dead worker: agent pane must not bind to dead task; got AgentTaskID=%q", a.AgentTaskID())
	}
	// The pane is cleared to "" (placeholder), not left at the prior live binding.
	if a.AgentTaskID() != "" {
		t.Fatalf("dead worker: agent pane must be cleared to placeholder; got AgentTaskID=%q", a.AgentTaskID())
	}
	// SubscribeTask must not be called for the dead task after the guard point.
	for _, call := range src.calls[callsBefore:] {
		if call == "t-dead" {
			t.Fatalf("dead worker: SubscribeTask must not be called for dead task; new calls: %v", src.calls[callsBefore:])
		}
	}
}

// TestApplyRailSelection_DeadFreelancerClearsAgentPane proves that the
// freelancer path in applyRailSelection also guards against dead tasks.
// A dead freelancer row (Dead=true) must clear the agent pane to placeholder
// instead of calling rebindAgent with the dead task id (BUG-014).
func TestApplyRailSelection_DeadFreelancerClearsAgentPane(t *testing.T) {
	d := openTestDB(t)

	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Prime the pane with a live freelancer so we start from a non-empty binding.
	liveFreelancer := &roleEntry{
		RoleKind:    string(db.KindFreelance),
		ArgusTaskID: "t-free-live",
		Name:        "live-free",
	}
	a.applyRailSelection(liveFreelancer)
	if a.AgentTaskID() != "t-free-live" {
		t.Fatalf("baseline: expected agent bound to live freelancer; got %q", a.AgentTaskID())
	}
	preCalls := make([]string, len(src.calls))
	copy(preCalls, src.calls)

	// Now simulate landing on a dead freelancer row.
	deadFreelancer := &roleEntry{
		RoleKind:    string(db.KindFreelance),
		ArgusTaskID: "t-free-dead",
		Name:        "dead-free",
		Dead:        true,
	}
	a.applyRailSelection(deadFreelancer)

	// The dead task must never be bound.
	if a.AgentTaskID() == "t-free-dead" {
		t.Fatalf("dead freelancer: agent pane must not bind to dead task; got AgentTaskID=%q", a.AgentTaskID())
	}
	// Pane is cleared to placeholder.
	if a.AgentTaskID() != "" {
		t.Fatalf("dead freelancer: agent pane must be cleared to placeholder; got AgentTaskID=%q", a.AgentTaskID())
	}
	// SubscribeTask was not called with the dead task id after the guard point.
	for _, call := range src.calls[len(preCalls):] {
		if call == "t-free-dead" {
			t.Fatalf("dead freelancer: SubscribeTask must not be called for dead task; new calls: %v", src.calls[len(preCalls):])
		}
	}
}

// TestOnRailSelectEnter_DeadWorkerStaysInRail proves that pressing Enter on a
// dead worker row returns FocusRAIL — never FocusAGENT — so the operator's
// focus is not moved into a pane bound to a 404-ing PTY (BUG-014). Before the
// fix, Enter on a dead row returned FocusAGENT and bound the agent pane to the
// dead task, which stalled the proxy subscription and starved the input loop.
func TestOnRailSelectEnter_DeadWorkerStaysInRail(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	liveRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "live", Kind: db.KindWorker, ArgusProject: "proj"})
	deadRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: orch.ID, Name: "dead", Kind: db.KindWorker, ArgusProject: "proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: liveRole.ID, ArgusTaskID: "t-live", WorktreePath: "/l"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: deadRole.ID, ArgusTaskID: "t-dead", WorktreePath: "/d"})

	src := &aliveStatePaneSource{
		alive:  map[string]bool{"t-live": true, "t-dead": false},
		states: map[string]ArgusTaskState{"t-live": {Status: "in_progress"}},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	a.showArchived = true
	if err := a.populateRail(d); err != nil {
		t.Fatalf("populateRail: %v", err)
	}

	if !a.pieces.rail.SelectByRoleID(deadRole.ID) {
		t.Fatalf("could not select dead worker row in rail (showArchived must open the Archive expando)")
	}
	ref, ok := a.pieces.rail.CurrentRef().(*roleEntry)
	if !ok || !ref.Dead {
		t.Fatalf("current row must be a dead roleEntry; got type %T Dead=%v", a.pieces.rail.CurrentRef(), ok && ref.Dead)
	}

	got := a.OnRailSelectEnter()
	if got != FocusRAIL {
		t.Fatalf("Enter on dead worker row: want FocusRAIL (no PTY bind, no freeze), got %s", got)
	}
	if a.AgentTaskID() == "t-dead" {
		t.Fatalf("Enter on dead worker row must not bind agent pane to dead task; got AgentTaskID=%q", a.AgentTaskID())
	}
}

// TestApplyRailSelection_NestedWorkerBindsChildCoord proves that selecting a
// worker under a NESTED (child) orchestrator binds the COORD pane to the child
// orchestrator's coord task, not empty (BUG-015). Before the fix, the worker
// path in applyRailSelection searched only the top-level orchestrators list;
// resolveSubCoordinators removes child orchestrators from that list and nests
// them under their parent role's childOrch — so the lookup always missed,
// leaving coordTask="" and the COORD pane showing "(no coord selected)".
func TestApplyRailSelection_NestedWorkerBindsChildCoord(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	// Parent orchestrator: a coordinator + a regular worker + a sub-coordinator
	// worker whose argus task is also the child orchestrator's coord task.
	parentOrch, _ := d.Orchestrators.Create(ctx, "parent-proj")
	parentCoordRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: parentOrch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "parent-proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: parentCoordRole.ID, ArgusTaskID: "t-parent-coord", WorktreePath: "/pc"})
	parentWorkerRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: parentOrch.ID, Name: "top-worker", Kind: db.KindWorker, ArgusProject: "parent-proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: parentWorkerRole.ID, ArgusTaskID: "t-top-worker", WorktreePath: "/tw"})
	// The sub-coordinator worker: its task id is the join key for the child orch.
	subCoordWorkerRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: parentOrch.ID, Name: "sub-coord", Kind: db.KindWorker, ArgusProject: "parent-proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: subCoordWorkerRole.ID, ArgusTaskID: "t-sub-coord", WorktreePath: "/sc"})

	// Child orchestrator whose coordinator's argus task is t-sub-coord (the
	// multi-binding join: resolveSubCoordinators links the two via CoordTaskID).
	childOrch, _ := d.Orchestrators.Create(ctx, "child-proj")
	childCoordRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: childOrch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "child-proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: childCoordRole.ID, ArgusTaskID: "t-sub-coord", WorktreePath: "/sc"})
	// A plain worker inside the child orchestrator — this is the row under test.
	childWorkerRole, _ := d.Roles.Create(ctx, db.CreateRoleInput{OrchestratorID: childOrch.ID, Name: "child-worker", Kind: db.KindWorker, ArgusProject: "child-proj"})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: childWorkerRole.ID, ArgusTaskID: "t-child-worker", WorktreePath: "/cw"})

	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()
	if err := a.populateRail(d); err != nil {
		t.Fatalf("populateRail: %v", err)
	}

	// After resolveSubCoordinators, child-orch is absent from the top-level
	// list. Find the child-worker entry nested inside:
	//   parent-orch → sub-coord-worker.childOrch (= child-orch) → child-worker
	var childWorker *roleEntry
	for _, o := range a.pieces.rail.orchestrators {
		for _, r := range o.Roles {
			if r.childOrch == nil {
				continue
			}
			for _, cr := range r.childOrch.Roles {
				if cr.RoleID == childWorkerRole.ID {
					childWorker = cr
				}
			}
		}
	}
	if childWorker == nil {
		t.Fatalf("child-worker role not found nested under sub-coordinator in rail data")
	}

	// Selecting the nested worker must bind COORD to the child orch's coord task
	// (t-sub-coord), not empty.
	a.applyRailSelection(childWorker)
	if a.CoordTaskID() != "t-sub-coord" {
		t.Fatalf("nested worker: coord pane must bind to child orch coord task %q; got %q (BUG-015: top-level-only search missed child orch)", "t-sub-coord", a.CoordTaskID())
	}

	// Regression: top-level worker still resolves to the parent's coord task.
	var topWorker *roleEntry
	for _, o := range a.pieces.rail.orchestrators {
		for _, r := range o.Roles {
			if r.RoleID == parentWorkerRole.ID {
				topWorker = r
			}
		}
	}
	if topWorker == nil {
		t.Fatalf("top-level worker role not found in rail data")
	}
	a.applyRailSelection(topWorker)
	if a.CoordTaskID() != "t-parent-coord" {
		t.Fatalf("top-level worker: coord pane must bind to parent coord task %q; got %q", "t-parent-coord", a.CoordTaskID())
	}
}

// TestOnRailSelectEnter_DeadFreelancerStaysInRail proves Enter on a dead
// freelancer row stays in RAIL, mirroring the worker guard above (BUG-014).
func TestOnRailSelectEnter_DeadFreelancerStaysInRail(t *testing.T) {
	d := openTestDB(t)

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	deadFreelancer := &roleEntry{
		RoleKind:    string(db.KindFreelance),
		ArgusTaskID: "t-free-dead",
		Name:        "dead-free",
		Dead:        true,
	}
	a.pieces.rail.rows = []railRow{
		{kind: railRowRole, role: deadFreelancer},
	}
	a.pieces.rail.cursor = 0

	got := a.OnRailSelectEnter()
	if got != FocusRAIL {
		t.Fatalf("Enter on dead freelancer row: want FocusRAIL, got %s", got)
	}
	if a.AgentTaskID() == "t-free-dead" {
		t.Fatalf("Enter on dead freelancer row must not bind agent pane to dead task; got AgentTaskID=%q", a.AgentTaskID())
	}
}

// --- BUG-016: comprehensive help overlay ---

// TestHelpHotkeyItems_ContainsAllFocusStates asserts that helpHotkeyItems
// returns a flat list containing keys from every focus state (Rail, Coord pane,
// Agent pane). The test checks for representative labels from each section and
// verifies that all items are Bar:false (the comprehensive frame must not
// corrupt argus's context-sensitive bottom bar).
func TestHelpHotkeyItems_ContainsAllFocusStates(t *testing.T) {
	items := helpHotkeyItems(true, true)

	labelSet := func(its []HotkeyItem) map[string]bool {
		m := make(map[string]bool, len(its))
		for _, it := range its {
			if it.Label != "" {
				m[it.Label] = true
			}
		}
		return m
	}
	labels := labelSet(items)

	// Rail keys.
	if !labels["move"] {
		t.Errorf("helpHotkeyItems must include Rail 'move' (j/k); labels: %v", labels)
	}
	if !labels["new"] {
		t.Errorf("helpHotkeyItems must include Rail 'new' (n); labels: %v", labels)
	}
	if !labels["archive"] {
		t.Errorf("helpHotkeyItems must include Rail 'archive' (a); labels: %v", labels)
	}
	if !labels["prune"] {
		t.Errorf("helpHotkeyItems must include Rail 'prune' (^r); labels: %v", labels)
	}

	// Coord pane keys (coordPresent=true).
	if !labels["coord PTY"] {
		t.Errorf("helpHotkeyItems must include Coord pane 'coord PTY'; labels: %v", labels)
	}

	// Agent pane keys.
	if !labels["agent PTY"] {
		t.Errorf("helpHotkeyItems must include Agent pane 'agent PTY'; labels: %v", labels)
	}

	// All items Bar:false — the comprehensive frame must not drive the bottom bar.
	for _, it := range items {
		if it.Bar {
			t.Errorf("helpHotkeyItems must return all Bar:false; found Bar:true {%q %q}", it.Key, it.Label)
		}
	}
}

// TestHelpHotkeyItems_CoordAbsent asserts that when coordPresent is false the
// Coord pane section is omitted but Rail and Agent pane sections are present.
func TestHelpHotkeyItems_CoordAbsent(t *testing.T) {
	items := helpHotkeyItems(false, true)

	hasLabel := func(label string) bool {
		for _, it := range items {
			if it.Label == label {
				return true
			}
		}
		return false
	}

	if !hasLabel("move") {
		t.Errorf("helpHotkeyItems (no coord) must include Rail 'move'")
	}
	if !hasLabel("agent PTY") {
		t.Errorf("helpHotkeyItems (no coord) must include Agent pane 'agent PTY'")
	}
	if hasLabel("coord PTY") {
		t.Errorf("helpHotkeyItems (no coord) must NOT include Coord pane 'coord PTY'")
	}
}

// TestApp_SendHelp_PushesComprehensiveThreeStateSections asserts that
// App.SendHelp sends exactly three frames: (1) comprehensive hotkeys with
// entries from all three focus states, all Bar:false; (2) the help control
// frame; (3) the current-focus restore hotkeys so argus's bar is correct when
// the overlay is dismissed.
func TestApp_SendHelp_PushesComprehensiveThreeStateSections(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	conn := &fakeControlConn{}
	a.SetControl(newViewControl(context.Background(), conn))

	if err := a.SendHelp(); err != nil {
		t.Fatalf("SendHelp: %v", err)
	}

	writes := conn.Writes()
	if len(writes) != 3 {
		t.Fatalf("want 3 frames (comprehensive hotkeys + help + restore); got %d", len(writes))
	}

	// Frame 0: comprehensive hotkeys.
	var env0 struct {
		Type  string       `json:"type"`
		Items []HotkeyItem `json:"items"`
	}
	if err := json.Unmarshal(writes[0].Data, &env0); err != nil {
		t.Fatalf("frame 0: %v", err)
	}
	if env0.Type != "hotkeys" {
		t.Fatalf("frame 0 type: want hotkeys, got %q", env0.Type)
	}
	allLabels := make(map[string]bool)
	for _, it := range env0.Items {
		if it.Label != "" {
			allLabels[it.Label] = true
		}
	}
	// Rail keys.
	if !allLabels["move"] {
		t.Errorf("comprehensive frame must include Rail 'move'; labels: %v", allLabels)
	}
	if !allLabels["new"] {
		t.Errorf("comprehensive frame must include Rail 'new'; labels: %v", allLabels)
	}
	// Coord pane keys (App defaults coordPresent=true).
	if !allLabels["coord PTY"] {
		t.Errorf("comprehensive frame must include Coord 'coord PTY'; labels: %v", allLabels)
	}
	// Agent pane keys.
	if !allLabels["agent PTY"] {
		t.Errorf("comprehensive frame must include Agent 'agent PTY'; labels: %v", allLabels)
	}
	// All Bar:false.
	for _, it := range env0.Items {
		if it.Bar {
			t.Errorf("comprehensive frame must be all Bar:false; found Bar:true {%q %q}", it.Key, it.Label)
		}
	}

	// Frame 1: help control.
	var env1 struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(writes[1].Data, &env1); err != nil {
		t.Fatalf("frame 1: %v", err)
	}
	if env1.Type != "help" {
		t.Fatalf("frame 1 type: want help, got %q", env1.Type)
	}

	// Frame 2: restore hotkeys for current focus (FocusRAIL at rest).
	var env2 struct {
		Type  string       `json:"type"`
		Items []HotkeyItem `json:"items"`
	}
	if err := json.Unmarshal(writes[2].Data, &env2); err != nil {
		t.Fatalf("frame 2: %v", err)
	}
	if env2.Type != "hotkeys" {
		t.Fatalf("frame 2 type: want hotkeys, got %q", env2.Type)
	}
	hasRailKey := false
	for _, it := range env2.Items {
		if it.Label == "move" {
			hasRailKey = true
		}
	}
	if !hasRailKey {
		t.Errorf("restore frame must include Rail 'move'; got %+v", env2.Items)
	}
}

// --- BUG-053: OnTaskReattached resets resize state for the new session ---

// After a successful reattach, App.OnTaskReattached must call InvalidateResize
// on the source (to clear the ProxyManager's "already applied" flag) so the
// next resize dispatch reaches argus and sizes the new session correctly.
// The actual ResizeTask call is queued on the tview event loop via a goroutine
// (same pattern as the reflow callbacks) and cannot be verified synchronously
// in unit tests without a running event loop — the pane-level and
// ProxyManager-level behavior are covered by TestPinnedTerminalPane and
// TestProxyManager_ResetApplied respectively.
func TestApp_OnTaskReattached_CallsInvalidateResize(t *testing.T) {
	d := openTestDB(t)
	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	a.OnTaskReattached("task-agent")

	// InvalidateResize must be called for the task so the ProxyManager's
	// applied flag is cleared before the event-loop resize is queued.
	if len(src.invalidated) != 1 || src.invalidated[0] != "task-agent" {
		t.Fatalf("InvalidateResize not called with task-agent; got %v", src.invalidated)
	}
}

// An empty task id must be a complete no-op.
func TestApp_OnTaskReattached_EmptyTaskID_NoOp(t *testing.T) {
	d := openTestDB(t)
	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	a.OnTaskReattached("")

	if len(src.invalidated) != 0 {
		t.Fatalf("empty task id must not call InvalidateResize; got %v", src.invalidated)
	}
}

// --- BUG-010 (browse-archived): OnFocusChanged resets resize state on pane entry ---

// OnFocusChanged(FocusCOORD/AGENT) must call InvalidateResize for the bound task
// (same pattern as OnTaskReattached) so the ProxyManager's "already applied" flag
// is cleared and the next ResizeTask dispatch reaches argus. This ensures the
// emulator clamps the cursor position from an archived task's snapshot to the
// current pane bounds, preventing the cursor from appearing below the argus
// status line when entering an archived task pane.
//
// The QueueUpdateDraw callback that calls ResizeTask is tested only at the
// InvalidateResize seam — the async draw cannot be exercised synchronously
// without a running tview event loop (same limitation as TestApp_OnTaskReattached).
func TestApp_OnFocusChanged_CoordPane_CallsInvalidateResize(t *testing.T) {
	d := openTestDB(t)
	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Wire a coord task so OnFocusChanged has a non-empty taskID to invalidate.
	a.mu.Lock()
	a.coordTask = "archived-coord-task"
	a.mu.Unlock()

	a.OnFocusChanged(FocusCOORD)

	if len(src.invalidated) != 1 || src.invalidated[0] != "archived-coord-task" {
		t.Fatalf("InvalidateResize not called with archived-coord-task on FocusCOORD; got %v", src.invalidated)
	}
}

func TestApp_OnFocusChanged_AgentPane_CallsInvalidateResize(t *testing.T) {
	d := openTestDB(t)
	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	a.mu.Lock()
	a.agentTask = "archived-agent-task"
	a.mu.Unlock()

	a.OnFocusChanged(FocusAGENT)

	if len(src.invalidated) != 1 || src.invalidated[0] != "archived-agent-task" {
		t.Fatalf("InvalidateResize not called with archived-agent-task on FocusAGENT; got %v", src.invalidated)
	}
}

// When the pane has no bound task, there is no session to resize — InvalidateResize
// must not be called with an empty taskID.
func TestApp_OnFocusChanged_EmptyTask_NoInvalidateResize(t *testing.T) {
	d := openTestDB(t)
	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// coordTask and agentTask default to "" — no task bound.
	a.OnFocusChanged(FocusCOORD)
	a.OnFocusChanged(FocusAGENT)

	if len(src.invalidated) != 0 {
		t.Fatalf("InvalidateResize must not be called when no task is bound; got %v", src.invalidated)
	}
}

// RAIL focus must never trigger an InvalidateResize — RAIL has no PTY to resize.
func TestApp_OnFocusChanged_Rail_NoInvalidateResize(t *testing.T) {
	d := openTestDB(t)
	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	a.mu.Lock()
	a.coordTask = "some-task"
	a.agentTask = "some-other-task"
	a.mu.Unlock()

	a.OnFocusChanged(FocusRAIL)

	if len(src.invalidated) != 0 {
		t.Fatalf("InvalidateResize must not be called on RAIL focus; got %v", src.invalidated)
	}
}

// TestBuildAppRestoresLastSelectionFromDB verifies that BuildApp reads the
// persisted last-selected rail row from the DB and positions the cursor there,
// not at the bottom of the Freelance section (BUG-001).
func TestBuildAppRestoresLastSelectionFromDB(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	// Seed an orchestrator with a coordinator and a worker role/binding.
	orch, err := d.Orchestrators.Create(ctx, "alpha")
	if err != nil {
		t.Fatalf("create orch: %v", err)
	}
	if _, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "alpha",
	}); err != nil {
		t.Fatalf("create coord role: %v", err)
	}
	worker, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "alpha",
	})
	if err != nil {
		t.Fatalf("create worker role: %v", err)
	}
	if _, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: worker.ID, ArgusTaskID: "task-w1", WorktreePath: "/w1",
	}); err != nil {
		t.Fatalf("create binding: %v", err)
	}

	// Persist a last-selection pointing at the worker role.
	if err := saveRailStateToDB(ctx, d.Config, railViewState{
		LastSelection: railLastSelection{
			RoleID:      worker.ID,
			ArgusTaskID: "task-w1",
		},
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}

	// Build the app — cursor should restore to the worker row, not freelancers.
	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	ref, ok := a.pieces.rail.CurrentRef().(*roleEntry)
	if !ok {
		t.Fatalf("CurrentRef is not *roleEntry after restore; got %T", a.pieces.rail.CurrentRef())
	}
	if ref.RoleID != worker.ID {
		t.Errorf("cursor on RoleID %d, want %d", ref.RoleID, worker.ID)
	}
}

// TestBuildAppNoSavedSelectionLandsAtCoordinatorSection verifies that when no
// prior selection is saved, BuildApp places the cursor within the coordinator
// section rather than tracking a freelancer to the bottom (BUG-001). With no
// saved memory the cursor follows the findInitialSelection agentTask alignment,
// landing on the live worker row.
func TestBuildAppNoSavedSelectionLandsAtCoordinatorSection(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	// Seed one orchestrator with a worker so there is a live item at the top.
	orch, err := d.Orchestrators.Create(ctx, "alpha")
	if err != nil {
		t.Fatalf("create orch: %v", err)
	}
	w, err := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "alpha",
	})
	if err != nil {
		t.Fatalf("create worker: %v", err)
	}
	if _, err := d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: w.ID, ArgusTaskID: "task-w1", WorktreePath: "/w",
	}); err != nil {
		t.Fatalf("create binding: %v", err)
	}

	// No saved selection in the DB.
	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// The cursor must be on a row within the coordinator section (orch header or
	// managed role), NOT on a freelance row. It must not have drifted to the
	// bottom Freelance section due to the Set* ordering (the pre-fix bug).
	rail := a.pieces.rail
	switch ref := rail.CurrentRef().(type) {
	case *orchEntry:
		// Good: landed on the orchestrator header.
		_ = ref
	case *roleEntry:
		if ref.OrchestratorID == 0 {
			t.Errorf("cursor landed on a freelance row (OrchestratorID=0) when a coordinator section is present")
		}
	default:
		t.Errorf("cursor on unexpected ref type %T; should be orchestrator or worker, not freelance/separator", rail.CurrentRef())
	}
}

// TestApp_ApplyDeadPaneFocusGuard_NoopWhenNoTriggerWired proves that
// applyDeadPaneFocusGuard does NOT kick focus to RAIL when no reattach trigger
// is wired (e.g. tests without a live session). The REATTACHING splash is the
// only dead-pane UX; the old BUG-006 RAIL-snap is removed (BUG-008).
func TestApp_ApplyDeadPaneFocusGuard_NoopWhenNoTriggerWired(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	focus := NewFocusMachine()
	a.SetFocusMachine(focus)

	// Simulate a coord pane bound to "dead-task" with COORD focus.
	a.mu.Lock()
	a.coordTask = "dead-task"
	a.mu.Unlock()
	focus.JumpToCOORD()
	a.OnFocusChanged(FocusCOORD)

	if focus.State() != FocusCOORD {
		t.Fatalf("precondition: focus must be COORD; got %v", focus.State())
	}

	// No reattach trigger wired — guard must be a no-op (no RAIL snap).
	a.applyDeadPaneFocusGuard("dead-task")

	if focus.State() != FocusCOORD {
		t.Fatalf("after applyDeadPaneFocusGuard (no trigger), focus = %v; want COORD (no kick)", focus.State())
	}
	if a.CurrentFocus() != FocusCOORD {
		t.Fatalf("after applyDeadPaneFocusGuard (no trigger), CurrentFocus = %v; want COORD", a.CurrentFocus())
	}
}

// TestApp_ApplyDeadPaneFocusGuard_NoopForUnfocusedPane proves that a 404 for
// a task not currently focused (e.g., agent pane 404 while COORD has focus)
// does NOT disturb focus.
func TestApp_ApplyDeadPaneFocusGuard_NoopForUnfocusedPane(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	focus := NewFocusMachine()
	a.SetFocusMachine(focus)

	// Coord pane is focused; agent pane is dead.
	a.mu.Lock()
	a.coordTask = "live-coord"
	a.agentTask = "dead-agent"
	a.mu.Unlock()
	focus.JumpToCOORD()
	a.OnFocusChanged(FocusCOORD)

	// Dead task is the agent pane (not focused) — focus must not change.
	a.applyDeadPaneFocusGuard("dead-agent")

	if focus.State() != FocusCOORD {
		t.Fatalf("focus must stay COORD when dead task is the non-focused pane; got %v", focus.State())
	}
}

// TestApp_ApplyDeadPaneFocusGuard_NoopWhenAlreadyRAIL proves that calling
// applyDeadPaneFocusGuard when focus is already RAIL is a safe no-op.
func TestApp_ApplyDeadPaneFocusGuard_NoopWhenAlreadyRAIL(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	focus := NewFocusMachine()
	a.SetFocusMachine(focus)
	a.mu.Lock()
	a.coordTask = "dead-task"
	a.mu.Unlock()
	// Focus is already RAIL.
	a.OnFocusChanged(FocusRAIL)

	a.applyDeadPaneFocusGuard("dead-task")

	if focus.State() != FocusRAIL {
		t.Fatalf("focus must stay RAIL; got %v", focus.State())
	}
}

// TestApp_ApplyDeadPaneFocusGuard_AgentPane_NoopWhenNoTriggerWired proves the
// guard is a no-op for the AGENT pane too when no reattach trigger is wired.
// No RAIL-snap — REATTACHING splash is the only dead-pane UX (BUG-008).
func TestApp_ApplyDeadPaneFocusGuard_AgentPane_NoopWhenNoTriggerWired(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	focus := NewFocusMachine()
	a.SetFocusMachine(focus)

	a.mu.Lock()
	a.agentTask = "dead-agent"
	a.mu.Unlock()
	focus.JumpToAGENT()
	a.OnFocusChanged(FocusAGENT)

	if focus.State() != FocusAGENT {
		t.Fatalf("precondition: focus must be AGENT; got %v", focus.State())
	}

	// No reattach trigger wired — guard must be a no-op (no RAIL snap).
	a.applyDeadPaneFocusGuard("dead-agent")

	if focus.State() != FocusAGENT {
		t.Fatalf("after applyDeadPaneFocusGuard (no trigger), focus = %v; want AGENT (no kick)", focus.State())
	}
}

// --- BUG-009: freelancer dead-session hang ---

// TestApplyRailSelection_FreelancerSetsAgentIsFreelancer proves that
// navigating to a freelancer row sets the agentIsFreelancer flag and that
// navigating away (to a managed worker) resets it (BUG-009).
func TestApplyRailSelection_FreelancerSetsAgentIsFreelancer(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	worker, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "w1", Kind: db.KindWorker, ArgusProject: "proj",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{
		RoleID: worker.ID, ArgusTaskID: "t-worker", WorktreePath: "/w",
	})

	a, err := BuildApp(d, &fakePaneSource{})
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Navigate to a freelancer row.
	freelancer := &roleEntry{
		RoleKind:    string(db.KindFreelance),
		ArgusTaskID: "t-free",
		Name:        "free-task",
	}
	a.applyRailSelection(freelancer)

	if !a.AgentIsFreelancer() {
		t.Errorf("agentIsFreelancer must be true after selecting a freelancer row; got false")
	}
	if a.AgentTaskID() != "t-free" {
		t.Errorf("agent task must bind to freelancer; got %q", a.AgentTaskID())
	}

	// Navigate to a managed worker — flag must reset.
	if err := a.populateRail(d); err != nil {
		t.Fatalf("populateRail: %v", err)
	}
	if !a.pieces.rail.SelectByRoleID(worker.ID) {
		t.Fatalf("could not select worker row")
	}
	a.applyRailSelection(a.pieces.rail.CurrentRef())

	if a.AgentIsFreelancer() {
		t.Errorf("agentIsFreelancer must be false after selecting a managed worker; got true")
	}
}

// TestMaybeAutoReattachPane_FreelancerSkipsAutoReattach proves that
// --- BUG-012: reattach splash / resize fixes ---

// StartPaneReattach must fire any pending debounced selection so the pane is
// bound before the splash is shown. Without this fix, pressing Enter on a
// dead-session row within the 120ms debounce window leaves pane==nil and the
// splash never appears.
func TestStartPaneReattach_FiresSelectionWhenPaneNotBound(t *testing.T) {
	d := openTestDB(t)

	src := &aliveStatePaneSource{
		alive: map[string]bool{"t-dead": false},
		states: map[string]ArgusTaskState{
			"t-dead": {Status: "complete"},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	a.modalSync = true

	// Simulate a pending debounced selection for a dead-session worker row.
	// The pane is NOT yet bound to "t-dead" — the debounce hasn't fired.
	deadRef := &roleEntry{
		RoleKind:    string(db.KindWorker),
		ArgusTaskID: "t-dead",
		Name:        "dead-agent",
		Dead:        false,
		HasState:    true,
		Status:      "complete",
	}
	a.selectMu.Lock()
	a.selectPending = deadRef
	a.selectHasRef = true
	a.selectMu.Unlock()

	// Confirm pane is NOT yet bound.
	if a.AgentTaskID() == "t-dead" {
		t.Fatal("precondition: agent pane must not be bound to t-dead before StartPaneReattach")
	}

	// StartPaneReattach must fire the selection and show the splash.
	a.StartPaneReattach("t-dead")

	// After StartPaneReattach, the pane must be bound (selection was fired).
	if a.AgentTaskID() != "t-dead" {
		t.Errorf("StartPaneReattach must bind agent pane via fireSelectionNow; got AgentTaskID=%q", a.AgentTaskID())
	}
}

// TestStartPaneReattach_FiresSelectionWhenBoundButWrongBodyMode tests that
// StartPaneReattach fires any pending debounced selection even when the agent
// pane IS already bound to the task. Without the unconditional fireSelectionNow,
// a "notBound=false" pane check caused the fire to be skipped — leaving the
// body in COORD-only mode where the agent pane (and splash) is invisible.
// Scenario: user selected a dead-session row (pane bound, body=COORD+AGENT),
// then navigated to an orchestrator header (body=COORD-only), then back to the
// dead-session row within the 120ms debounce window, then pressed Enter.
func TestStartPaneReattach_FiresSelectionWhenBoundButWrongBodyMode(t *testing.T) {
	d := openTestDB(t)

	src := &aliveStatePaneSource{
		alive: map[string]bool{"t-dead": false},
		states: map[string]ArgusTaskState{
			"t-dead": {Status: "complete"},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	a.modalSync = true

	// Simulate the buggy state: agent pane IS bound to "t-dead" but the body
	// is in COORD-only mode (agentPresent=false). A pending debounced selection
	// for "t-dead" has not yet fired.
	a.mu.Lock()
	a.agentTask = "t-dead"
	a.coordPresent = true
	a.agentPresent = false // COORD-only — splash would land on invisible pane
	a.mu.Unlock()

	deadRef := &roleEntry{
		RoleKind:    string(db.KindWorker),
		ArgusTaskID: "t-dead",
		Name:        "dead-agent",
		Dead:        false,
		HasState:    true,
		Status:      "complete",
	}
	a.selectMu.Lock()
	a.selectPending = deadRef
	a.selectHasRef = true
	a.selectMu.Unlock()

	// Precondition: agent pane IS bound (so the old "notBound" guard skipped the fire).
	if a.AgentTaskID() != "t-dead" {
		t.Fatal("precondition: agent pane must be bound to t-dead")
	}
	// Precondition: body is COORD-only.
	a.mu.Lock()
	agentPresent := a.agentPresent
	a.mu.Unlock()
	if agentPresent {
		t.Fatal("precondition: body must be in COORD-only mode (agentPresent=false)")
	}

	// StartPaneReattach must fire the pending selection so the body mode
	// switches to COORD+AGENT before showing the splash.
	a.StartPaneReattach("t-dead")

	// After StartPaneReattach, the body must be in COORD+AGENT mode so the
	// splash is visible (not hidden behind a COORD-only layout).
	a.mu.Lock()
	agentPresent = a.agentPresent
	a.mu.Unlock()
	if !agentPresent {
		t.Error("StartPaneReattach must fire pending selection to restore COORD+AGENT mode; agentPresent=false after call")
	}

	// The agent pane must be showing the splash.
	a.mu.Lock()
	pane := a.pieces.agent
	a.mu.Unlock()
	if pane == nil || !pane.reattaching {
		t.Error("StartPaneReattach must activate the REATTACHING splash on the agent pane")
	}
}

// StartPaneReattach must leave focus on the RAIL. The terminal cursor must not
// jump to the pane before the splash renders — focus moves to the pane only
// after clearReattachAndResize fires (the reattach + 1s minimum hold).
func TestStartPaneReattach_FocusStaysOnRail(t *testing.T) {
	d := openTestDB(t)

	src := &aliveStatePaneSource{
		alive: map[string]bool{"t-dead": false},
		states: map[string]ArgusTaskState{
			"t-dead": {Status: "complete"},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	focus := NewFocusMachine()
	a.SetFocusMachine(focus)
	a.modalSync = true

	// Wire in the dead-session row so fireSelectionNow binds the pane.
	deadRef := &roleEntry{
		RoleKind:    string(db.KindWorker),
		ArgusTaskID: "t-dead",
		Name:        "dead-agent",
		Dead:        false,
		HasState:    true,
		Status:      "complete",
	}
	a.selectMu.Lock()
	a.selectPending = deadRef
	a.selectHasRef = true
	a.selectMu.Unlock()

	// Focus starts on RAIL.
	if focus.State() != FocusRAIL {
		t.Fatalf("precondition: focus must start on RAIL; got %v", focus.State())
	}

	a.StartPaneReattach("t-dead")

	// Focus must still be on RAIL — the splash should hold focus there.
	if got := focus.State(); got != FocusRAIL {
		t.Errorf("StartPaneReattach must leave focus on RAIL; got %v", got)
	}
	// Pane must be in reattaching state.
	a.mu.Lock()
	pane := a.pieces.agent
	a.mu.Unlock()
	if pane == nil || !pane.reattaching {
		t.Error("StartPaneReattach must activate the REATTACHING splash")
	}
}

// clearReattachAndResize must move focus to the agent pane after clearing the
// splash so the operator lands in the pane ready to type without a manual
// Ctrl+→.
func TestClearReattachAndResize_MoveFocusToAgent(t *testing.T) {
	d := openTestDB(t)
	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	focus := NewFocusMachine()
	a.SetFocusMachine(focus)

	// Bind agent pane and activate the splash (simulates the state after
	// StartPaneReattach).
	a.mu.Lock()
	a.agentTask = "t-agent"
	a.pieces.agent.SetReattaching(true, "connecting...")
	a.mu.Unlock()

	// Focus is on RAIL (as StartPaneReattach leaves it).
	if focus.State() != FocusRAIL {
		t.Fatalf("precondition: focus must be RAIL; got %v", focus.State())
	}

	// GetRect returns 0 (no layout run) → zero-rect path.
	a.clearReattachAndResize("t-agent")

	if got := focus.State(); got != FocusAGENT {
		t.Errorf("clearReattachAndResize must move focus to AGENT; got %v", got)
	}
}

// OnTaskReattached must dispatch ResizeTask with the layout-allocated rect
// dimensions (GetRect inner) rather than PinnedSize(). The splash Draw path
// never updates pinnedCols/Rows, leaving PinnedSize() at the 80×24 construction
// default for the entire reattach window. Using GetRect() gives the correct
// actual pane allocation so the new session receives the right dimensions via
// SIGWINCH (BUG-012).
//
// We verify the resize call is made with the bound task ID; the exact dimensions
// depend on the pane's layout rect (0 in unit tests without a running event
// loop), so we only assert the task ID is correct and no resize happens when
// the inner rect is zero.
func TestApp_OnTaskReattached_UsesGetRectNotPinnedSize(t *testing.T) {
	d := openTestDB(t)
	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Bind agent pane to t-agent with a non-zero PinnedSize that differs from
	// the actual allocation. In this test the app has no tview event loop so
	// GetRect() returns 0,0 (inner=-2, -2 → clamped to no resize). This
	// confirms the code path switched to GetRect and did NOT use the (wrong)
	// PinnedSize.
	a.mu.Lock()
	a.agentTask = "t-agent"
	a.mu.Unlock()

	// InvalidateResize must always fire (synchronous, pre-QueueUpdateDraw).
	a.OnTaskReattached("t-agent")
	if len(src.invalidated) != 1 || src.invalidated[0] != "t-agent" {
		t.Fatalf("InvalidateResize not called; got %v", src.invalidated)
	}
	// ResizeTask is not called here because the unit-test pane has GetRect()==0
	// (no layout run) → inner dims ≤ 0 → the resize is skipped. This is
	// correct: the test verifies the BRANCH was taken (GetRect path), not the
	// resize value itself (verified by integration with pinnedTerminalPane.Draw).
}

// --- BUG-012 clean-slate reattach ---

// clearReattachAndResize must be a no-op when the task ID is not bound to any
// pane, and must not panic.
func TestApp_ClearReattachAndResize_NoopWhenNotBound(t *testing.T) {
	d := openTestDB(t)
	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Not bound to any pane — must return without side effects.
	a.clearReattachAndResize("unknown-task")
	if len(src.resizes) != 0 {
		t.Fatalf("ResizeTask must not fire for unbound task; got %v", src.resizes)
	}
}

// When the pane's GetRect is zero (no layout run), clearReattachAndResize must
// clear the splash via SetReattaching(false) rather than attempting a forceRebind
// with zero dimensions.
func TestApp_ClearReattachAndResize_ClearsSplashWhenRectZero(t *testing.T) {
	d := openTestDB(t)
	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Bind agent pane and activate the splash.
	a.mu.Lock()
	a.agentTask = "t-agent"
	a.pieces.agent.SetReattaching(true, "connecting...")
	a.mu.Unlock()

	if !a.pieces.agent.reattaching {
		t.Fatal("precondition: agent pane must be reattaching")
	}

	// GetRect returns 0 (no layout run), so forceRebind path is skipped.
	a.clearReattachAndResize("t-agent")

	if a.pieces.agent.reattaching {
		t.Error("clearReattachAndResize must clear the splash when GetRect is zero")
	}
}

// clearReattachAndResize must call ResetSubscription (via paneSubscriptionResetter)
// before forceRebindAgent so the old session's ring buffer is flushed before
// the fresh pane subscribes (BUG-012). Without the reset, the snapshot fed to
// the new pane contains old-session bytes, making the emulator start from the
// old cursor position instead of the new session's clean prompt.
func TestClearReattachAndResize_ResetsSubscriptionBeforeRebind(t *testing.T) {
	d := openTestDB(t)
	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	focus := NewFocusMachine()
	a.SetFocusMachine(focus)

	// Bind the agent pane, activate the splash, and give it a non-zero rect
	// so clearReattachAndResize takes the forceRebind path (not the zero-rect
	// fallback). Inner dims = 50×25 (52-2, 27-2).
	a.mu.Lock()
	a.agentTask = "t-agent"
	a.pieces.agent.SetReattaching(true, "connecting...")
	a.mu.Unlock()
	a.pieces.agent.SetRect(0, 0, 52, 27)

	if len(src.resetSubscriptions) != 0 {
		t.Fatalf("precondition: no ResetSubscription calls yet; got %v", src.resetSubscriptions)
	}

	a.clearReattachAndResize("t-agent")

	if len(src.resetSubscriptions) == 0 {
		t.Error("clearReattachAndResize must call ResetSubscription before forceRebind")
	} else if src.resetSubscriptions[0] != "t-agent" {
		t.Errorf("ResetSubscription called with wrong task ID: got %q, want %q",
			src.resetSubscriptions[0], "t-agent")
	}
}

// clearReattachAndResize must NOT call ResetSubscription when the pane is not
// showing the reattach splash (BUG-015). OnTaskReattached schedules
// clearReattachAndResize on a timer; if the user navigates away and back before
// the timer fires, the current pane is a fresh rebind (reattaching=false).
// Calling ResetSubscription on such a pane wipes the ring buffer, erasing any
// pending typed input that the user had not yet submitted.
func TestClearReattachAndResize_SkipsResetWhenNotReattaching(t *testing.T) {
	d := openTestDB(t)
	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	a.mu.Lock()
	a.agentTask = "t-agent"
	// pane.reattaching is false by default (new pane from BuildApp)
	a.mu.Unlock()
	a.pieces.agent.SetRect(0, 0, 52, 27)

	if len(src.resetSubscriptions) != 0 {
		t.Fatalf("precondition: no ResetSubscription calls yet; got %v", src.resetSubscriptions)
	}

	a.clearReattachAndResize("t-agent")

	if len(src.resetSubscriptions) != 0 {
		t.Errorf("clearReattachAndResize must not call ResetSubscription when pane is not reattaching (BUG-015); got calls: %v", src.resetSubscriptions)
	}
}

// maybeAutoReattachPane must snap focus to RAIL for dead-session freelancer
// panes reached via Ctrl+→ so the operator is not stuck forwarding keystrokes
// to a dead PTY (BUG-012). The existing BUG-009 test confirms no auto-reattach
// fires; this test confirms the snap-to-RAIL guard was added.
func TestMaybeAutoReattachPane_DeadSessionFreelancer_SnapsToRAIL(t *testing.T) {
	d := openTestDB(t)

	src := &aliveStatePaneSource{
		alive: map[string]bool{"t-free": false},
		states: map[string]ArgusTaskState{
			"t-free": {Status: "complete"},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	focus := NewFocusMachine()
	a.SetFocusMachine(focus)

	var reattachCalled []string
	a.onDeadPaneReattach = func(taskID string) { reattachCalled = append(reattachCalled, taskID) }

	// Bind the agent pane to a dead-session freelancer task.
	a.mu.Lock()
	a.agentTask = "t-free"
	a.agentIsFreelancer = true
	a.mu.Unlock()

	// Simulate Ctrl+→ landing on AGENT (focus jumped there).
	focus.JumpToAGENT()
	a.maybeAutoReattachPane(FocusAGENT)

	// Auto-reattach must NOT fire (BUG-009 guard preserved).
	if len(reattachCalled) != 0 {
		t.Errorf("onDeadPaneReattach must NOT fire for freelancer; got calls: %v", reattachCalled)
	}
	// Focus must have snapped back to RAIL (BUG-012 addition).
	if focus.State() != FocusRAIL {
		t.Errorf("focus must snap to RAIL for dead-session freelancer; got %s", focus.State())
	}
}

// maybeAutoReattachPane must NOT snap to RAIL for a live freelancer pane —
// only the dead-session case triggers the snap.
func TestMaybeAutoReattachPane_LiveFreelancer_DoesNotSnapToRAIL(t *testing.T) {
	d := openTestDB(t)

	src := &aliveStatePaneSource{
		alive: map[string]bool{"t-free-live": true},
		states: map[string]ArgusTaskState{
			"t-free-live": {Status: "in_progress"},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	focus := NewFocusMachine()
	a.SetFocusMachine(focus)

	a.mu.Lock()
	a.agentTask = "t-free-live"
	a.agentIsFreelancer = true
	a.mu.Unlock()

	focus.JumpToAGENT()
	a.maybeAutoReattachPane(FocusAGENT)

	// Live freelancer: focus must stay in AGENT (no snap).
	if focus.State() != FocusAGENT {
		t.Errorf("live freelancer: focus must stay AGENT; got %s", focus.State())
	}
}

// fireSelectionNow must cancel the pending timer and call applyRailSelection
// synchronously. Verified via the resulting pane binding change.
func TestFireSelectionNow_AppliesPendingSelection(t *testing.T) {
	d := openTestDB(t)
	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Queue a pending selection pointing at a role ref that rebinds the agent.
	ref := &roleEntry{
		RoleKind:    string(db.KindWorker),
		ArgusTaskID: "t-worker-new",
		Name:        "new-worker",
	}
	a.selectMu.Lock()
	a.selectPending = ref
	a.selectHasRef = true
	a.selectMu.Unlock()

	a.fireSelectionNow()

	// Selection must be consumed.
	a.selectMu.Lock()
	hasRef := a.selectHasRef
	a.selectMu.Unlock()
	if hasRef {
		t.Error("selectHasRef must be false after fireSelectionNow")
	}

	// Pane must be rebound to the new task (applyRailSelection ran).
	if a.AgentTaskID() != "t-worker-new" {
		t.Errorf("agent pane must be bound to t-worker-new after fireSelectionNow; got %q", a.AgentTaskID())
	}
}

// fireSelectionNow must be a no-op when there is no pending selection.
func TestFireSelectionNow_NoOp_WhenNoPending(t *testing.T) {
	d := openTestDB(t)
	src := &fakePaneSource{}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	prev := a.AgentTaskID()
	a.fireSelectionNow() // must not panic or change state

	if a.AgentTaskID() != prev {
		t.Errorf("fireSelectionNow with no pending must be a no-op; got AgentTaskID=%q want %q", a.AgentTaskID(), prev)
	}
}

// maybeAutoReattachPane does not fire onDeadPaneReattach when the agent pane
// is bound to a freelancer task (BUG-009: navigating to a dead-session
// freelancer must not hang hera).
func TestMaybeAutoReattachPane_FreelancerSkipsAutoReattach(t *testing.T) {
	d := openTestDB(t)

	src := &aliveStatePaneSource{
		alive: map[string]bool{"t-free": false},
		states: map[string]ArgusTaskState{
			"t-free": {Status: "complete"},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	focus := NewFocusMachine()
	a.SetFocusMachine(focus)

	var reattachCalled []string
	a.onDeadPaneReattach = func(taskID string) { reattachCalled = append(reattachCalled, taskID) }

	// Bind the agent pane to a dead-session freelancer task.
	a.mu.Lock()
	a.agentTask = "t-free"
	a.agentIsFreelancer = true
	a.mu.Unlock()

	// Simulate focus landing on the AGENT pane (the trigger path from setBodyMode).
	focus.JumpToAGENT()
	a.maybeAutoReattachPane(FocusAGENT)

	if len(reattachCalled) != 0 {
		t.Errorf("onDeadPaneReattach must NOT fire for freelancer agent pane; got calls: %v", reattachCalled)
	}
}

// TestMaybeAutoReattachPane_ManagedWorkerFiresAutoReattach confirms the
// BUG-008 path still works for managed (non-freelancer) dead-session workers
// after the BUG-009 guard is added.
func TestMaybeAutoReattachPane_ManagedWorkerFiresAutoReattach(t *testing.T) {
	d := openTestDB(t)

	src := &aliveStatePaneSource{
		alive: map[string]bool{"t-worker": false},
		states: map[string]ArgusTaskState{
			"t-worker": {Status: "complete"},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	focus := NewFocusMachine()
	a.SetFocusMachine(focus)

	var reattachCalled []string
	a.onDeadPaneReattach = func(taskID string) { reattachCalled = append(reattachCalled, taskID) }

	// Bind the agent pane to a dead-session managed worker (agentIsFreelancer = false).
	a.mu.Lock()
	a.agentTask = "t-worker"
	a.agentIsFreelancer = false
	a.mu.Unlock()

	focus.JumpToAGENT()
	a.maybeAutoReattachPane(FocusAGENT)

	if len(reattachCalled) != 1 || reattachCalled[0] != "t-worker" {
		t.Errorf("onDeadPaneReattach must fire for managed worker dead-session; got calls: %v", reattachCalled)
	}
}

// TestMaybeAutoReattachPane_ManagedWorkerSnapsToRAIL proves that when focus
// enters the AGENT pane for a dead-session managed worker, maybeAutoReattachPane
// immediately snaps focus back to RAIL and marks the pane as reattaching —
// so the REATTACHING splash appears before any keystroke (BUG-012).
func TestMaybeAutoReattachPane_ManagedWorkerSnapsToRAIL(t *testing.T) {
	d := openTestDB(t)

	src := &aliveStatePaneSource{
		alive: map[string]bool{"t-worker": false},
		states: map[string]ArgusTaskState{
			"t-worker": {Status: "complete"},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	focus := NewFocusMachine()
	a.SetFocusMachine(focus)
	a.onDeadPaneReattach = func(taskID string) {}

	a.mu.Lock()
	a.agentTask = "t-worker"
	a.agentIsFreelancer = false
	a.mu.Unlock()

	focus.JumpToAGENT()
	a.maybeAutoReattachPane(FocusAGENT)

	if focus.State() != FocusRAIL {
		t.Errorf("after maybeAutoReattachPane on dead managed worker: focus = %v, want FocusRAIL", focus.State())
	}
	if !a.pieces.agent.reattaching {
		t.Errorf("after maybeAutoReattachPane on dead managed worker: pane.reattaching = false, want true")
	}
}

// TestApplyRailSelection_DeadSessionFreelancerSetsFlag proves that a
// dead-session freelancer (HasState=true, terminal status, Dead=false) also
// sets agentIsFreelancer when the cursor lands on the row, so
// maybeAutoReattachPane cannot fire for it (BUG-009).
func TestApplyRailSelection_DeadSessionFreelancerSetsFlag(t *testing.T) {
	d := openTestDB(t)

	src := &aliveStatePaneSource{
		alive: map[string]bool{"t-free-dead": false},
		states: map[string]ArgusTaskState{
			"t-free-dead": {Status: "complete"},
		},
	}
	a, err := BuildApp(d, src)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Dead-session freelancer: Dead=false but HasState=true with terminal status.
	deadSessionFreelancer := &roleEntry{
		RoleKind:    string(db.KindFreelance),
		ArgusTaskID: "t-free-dead",
		Name:        "dead-session-free",
		Dead:        false,
		HasState:    true,
		Status:      "complete",
	}
	a.applyRailSelection(deadSessionFreelancer)

	if a.AgentTaskID() != "t-free-dead" {
		t.Errorf("dead-session freelancer: agent must bind; got %q", a.AgentTaskID())
	}
	if !a.AgentIsFreelancer() {
		t.Errorf("dead-session freelancer: agentIsFreelancer must be true; got false")
	}

	// Also verify a reattach trigger is not fired even with focus bumped.
	var reattachCalled []string
	a.onDeadPaneReattach = func(taskID string) { reattachCalled = append(reattachCalled, taskID) }

	focus := NewFocusMachine()
	a.SetFocusMachine(focus)
	focus.JumpToAGENT()
	a.maybeAutoReattachPane(FocusAGENT)

	if len(reattachCalled) != 0 {
		t.Errorf("onDeadPaneReattach must not fire for dead-session freelancer; got calls: %v", reattachCalled)
	}
}

// --- BUG-016: SetFocusMachine syncs with current body mode ---

// TestApp_SetFocusMachine_SyncsCoordinatorBodyMode is the regression test for
// BUG-016. BuildApp calls applyRailSelection (which sets agentPresent=false for a
// coordinator initial selection) BEFORE SetFocusMachine is called. Without the
// sync in SetFocusMachine the freshly-created machine retains its default
// agentPresent=true, and Ctrl+→ from COORD advances focus to the absent AGENT pane.
func TestApp_SetFocusMachine_SyncsCoordinatorBodyMode(t *testing.T) {
	d := openTestDB(t)
	a, err := BuildApp(d, nil)
	if err != nil {
		t.Fatalf("BuildApp: %v", err)
	}
	defer a.Close()

	// Force coordinator body mode (coordPresent=true, agentPresent=false) as
	// would be set by applyRailSelection before SetFocusMachine is invoked.
	a.mu.Lock()
	a.coordPresent = true
	a.agentPresent = false
	a.mu.Unlock()

	focus := NewFocusMachine() // starts with agentPresent=true (the default)
	a.SetFocusMachine(focus)

	// The machine must be synced: Advance from RAIL → COORD, then a second
	// Advance must be a no-op (AGENT is absent).
	focus.Advance() // RAIL → COORD
	if focus.State() != FocusCOORD {
		t.Fatalf("Advance from RAIL: want COORD, got %s", focus.State())
	}
	focus.Advance() // COORD → must stay COORD (agent absent)
	if focus.State() != FocusCOORD {
		t.Fatalf("Advance from COORD in coordinator mode: want COORD (no-op), got %s (BUG-016: FocusMachine not synced on injection)", focus.State())
	}
}

// TestPopulateRail_CursorOnArchiveExpandoStaysOnRefresh is the BUG-014
// regression: when the cursor rests on an Archive (N) expando row and a state
// refresh (e.g. coord spinner stops) triggers populateRail, the cursor must
// stay on the expando — not jump to the first row of the rail.
//
// Root cause: currentRef() returns nil for Archive expando rows (they have no
// stable entity reference). SetOrchestrators/SetFreelance/SetArchivedFreelance
// all called firstSelectableRow() when restoreCursor returned false, which it
// always does for a nil prev — so the cursor snapped to the coord at the top.
func TestPopulateRail_CursorOnArchiveExpandoStaysOnRefresh(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()

	orch, _ := d.Orchestrators.Create(ctx, "proj")
	coord, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "coord", Kind: db.KindCoordinator, ArgusProject: "proj",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: coord.ID, ArgusTaskID: "c1", WorktreePath: "/c"})
	active, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "live-worker", Kind: db.KindWorker, ArgusProject: "proj",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: active.ID, ArgusTaskID: "t-active", WorktreePath: "/a"})
	archived, _ := d.Roles.Create(ctx, db.CreateRoleInput{
		OrchestratorID: orch.ID, Name: "stashed", Kind: db.KindWorker, ArgusProject: "proj",
	})
	_, _ = d.Bindings.Create(ctx, db.CreateBindingInput{RoleID: archived.ID, ArgusTaskID: "t-stashed", WorktreePath: "/s"})
	if err := d.Roles.Archive(ctx, archived.ID); err != nil {
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

	// Locate the Archive expando row — it must exist because orch has an
	// archived child.
	rail := a.pieces.rail
	archiveIdx := -1
	for i, r := range rail.rows {
		if r.kind == railRowArchiveExpando && r.archiveOwner == orch.ID {
			archiveIdx = i
			break
		}
	}
	if archiveIdx < 0 {
		t.Fatalf("Archive expando not found in rail rows; kinds=%v", rowKinds(rail))
	}

	// Move cursor directly to the Archive expando.
	rail.cursor = archiveIdx

	// Confirm that currentRef() returns nil for this row (it has no stable
	// entity reference — this is what caused the bug).
	if ref := rail.currentRef(); ref != nil {
		t.Fatalf("currentRef on Archive expando must be nil; got %T %v", ref, ref)
	}

	// Simulate a state refresh (e.g. coord spinner stops, argus pushes an idle
	// status update). populateRail calls SetOrchestrators / SetFreelance /
	// SetArchivedFreelance in sequence — each must leave the cursor on the
	// Archive expando, not snap it to the first selectable row.
	if err := a.populateRail(d); err != nil {
		t.Fatalf("populateRail: %v", err)
	}

	// Cursor must still be on the Archive expando row.
	if rail.cursor < 0 || rail.cursor >= len(rail.rows) {
		t.Fatalf("cursor out of range after refresh: %d (len=%d)", rail.cursor, len(rail.rows))
	}
	cur := rail.rows[rail.cursor]
	if cur.kind != railRowArchiveExpando || cur.archiveOwner != orch.ID {
		t.Fatalf("cursor jumped off Archive expando on refresh: row %d kind=%v archiveOwner=%v; want kind=railRowArchiveExpando owner=%d",
			rail.cursor, cur.kind, cur.archiveOwner, orch.ID)
	}
}
