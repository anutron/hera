package view

import (
	"fmt"
	"time"

	"github.com/anutron/argus-sdk/theme"
	"github.com/anutron/argus-sdk/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// railList is the rail's display widget. It renders orchestrators as
// section headers (collapse indicator + name + count) followed by
// indented role rows (moon icon + name + right-aligned elapsed time),
// mirroring argus's task-list layout so the operator feels like they
// haven't left argus.
//
// Coord rows are intentionally NOT rendered: the orchestrator header
// owns the coord task implicitly. Each orchestrator's CoordTaskID feeds
// the COORD pane when an agent (or the header itself) is selected.
//
// Selection is row-based with j/k or ↑/↓; Enter and the rail-only
// mutation keys are consumed upstream by the KeyRouter, so railList
// only handles cursor movement and collapse toggle inside its own
// InputHandler.
type railList struct {
	*tview.Box

	orchestrators []*orchEntry
	freelance     []*freelanceProject
	rows          []railRow
	cursor        int
	offset        int

	// collapsed tracks which orchestrators are collapsed. Default is
	// expanded; an entry is created the first time the operator hides
	// a section so the (zero-value) default behavior is "expanded".
	collapsed map[int64]bool

	// freelanceCollapsed tracks which Freelance repo groups are collapsed,
	// keyed by project name. Default expanded — surfacing freelancers is the
	// whole point (the operator should never leave hera to notice an
	// unmanaged agent needs attention).
	freelanceCollapsed map[string]bool

	// showArchived, when true, includes archived orchestrators and roles
	// in the rendered rows below the Archive separator.
	showArchived bool

	// onSelectionChanged is invoked whenever the cursor lands on a
	// different selectable row (orchestrator header or role). The arg
	// is the new currentRef (*orchEntry, *roleEntry, or nil). Callers
	// debounce upstream when they want to coalesce j/k bursts.
	onSelectionChanged func(ref any)

	// lastFiredCursor remembers the cursor index the last selection-
	// change callback fired against, so a no-op move (or an internal
	// rebuild that lands on the same row) doesn't re-fire.
	lastFiredCursor int

	// now is overridable for deterministic elapsed-time rendering in
	// tests. nil means use time.Now.
	now func() time.Time
}

// orchEntry is one orchestrator and its agent roles, in render order.
// Coord roles do not appear in Roles; the orchestrator-level CoordTaskID
// is the binding the COORD pane targets when this orchestrator is
// selected. Archived entries float to the bottom behind a separator
// when showArchived is true.
type orchEntry struct {
	ID          int64
	Name        string
	Archived    bool
	CoordTaskID string
	Roles       []*roleEntry
}

// roleEntry is one role under an orchestrator. Live indicates an open
// binding; StartedAt is the binding's StartedAt when live, else the
// role's CreatedAt — used to render the right-aligned elapsed time.
// Dead means the DB binding is still open but argus reports the
// underlying task as gone (archived / completed / 404). Dead rows are
// hidden by default and rendered dimmed when showArchived is true.
type roleEntry struct {
	OrchestratorID int64
	RoleID         int64
	RoleKind       string
	Name           string
	Live           bool
	Dead           bool
	ArgusTaskID    string
	Archived       bool
	StartedAt      time.Time

	// argus-reported state for this role's bound task, populated from the
	// ArgusStateCache when available. Drives the rail icon and archived
	// hiding so the rail reflects argus reality rather than hera's binding
	// bookkeeping. HasState is false when argus has no entry for the task
	// (unknown / cache cold) — the icon then falls back to binding state.
	HasState      bool
	Status        string // pending | in_progress | in_review | complete
	ArgusIdle     bool
	NeedsInput    bool
	ArgusArchived bool

	// ElapsedOverride, when non-empty, is rendered verbatim in the elapsed
	// column instead of computing from StartedAt. Freelance rows use argus's
	// pre-formatted age string so their column matches argus's own rail.
	ElapsedOverride string
}

// freelanceProject is one repo's worth of freelance agents — unmanaged
// argus tasks (no hera binding) grouped under the Freelance section by
// argus project, "the same way Argus shows them". Tasks are roleEntry
// values with RoleKind == "freelance" and OrchestratorID == 0.
type freelanceProject struct {
	Project string
	Tasks   []*roleEntry
}

type railRowKind uint8

const (
	railRowOrch railRowKind = iota
	railRowRole
	railRowArchiveSep
	railRowFreelanceSep
	railRowFreelanceProj
	railRowEmpty
)

type railRow struct {
	kind  railRowKind
	orch  *orchEntry
	role  *roleEntry
	fproj *freelanceProject
}

// newRailList constructs an empty rail widget. SetBorder is enabled so
// OnFocusChanged's SetBorderColor still drives the focus-color paint.
func newRailList() *railList {
	rl := &railList{
		Box:                tview.NewBox(),
		collapsed:          map[int64]bool{},
		freelanceCollapsed: map[string]bool{},
		lastFiredCursor:    -1,
	}
	rl.SetBorder(true)
	rl.SetTitle("Rail")
	return rl
}

// SetOnSelectionChanged registers a callback fired whenever the cursor
// lands on a new selectable row. The callback runs on the goroutine that
// triggered the cursor move (the tview input pump for j/k, or whichever
// goroutine called the Select* / SetOrchestrators / ToggleCollapse
// helpers). It is invoked with the new currentRef (*orchEntry,
// *roleEntry, or nil); callers must defer expensive work (subscription
// rewiring, etc.) to keep the event pump non-blocking.
func (rl *railList) SetOnSelectionChanged(fn func(ref any)) {
	rl.onSelectionChanged = fn
}

// maybeFireSelectionChanged invokes the registered callback when the
// cursor row has changed since the last fire. No-op when the cursor
// is on the same row. lastFiredCursor is updated even when no callback
// is wired so wiring the callback after initial setup does not
// erroneously fire on the next cursor settle.
func (rl *railList) maybeFireSelectionChanged() {
	if rl.cursor == rl.lastFiredCursor {
		return
	}
	rl.lastFiredCursor = rl.cursor
	if rl.onSelectionChanged != nil {
		rl.onSelectionChanged(rl.currentRef())
	}
}

// SetOrchestrators replaces the rail's input data and rebuilds rows.
// The cursor is preserved on the same orchestrator/role when possible;
// otherwise it lands on the first selectable row. Fires the selection-
// changed callback when the cursor lands on a different row than
// before.
func (rl *railList) SetOrchestrators(orchs []*orchEntry) {
	prev := rl.currentRef()
	rl.orchestrators = orchs
	rl.buildRows()
	if !rl.restoreCursor(prev) {
		rl.cursor = rl.firstSelectableRow()
	}
	rl.maybeFireSelectionChanged()
}

// SetFreelance replaces the rail's Freelance-section data (repo groups of
// unmanaged argus tasks) and rebuilds rows, preserving the cursor where
// possible. Called by populateRail alongside SetOrchestrators.
func (rl *railList) SetFreelance(projects []*freelanceProject) {
	prev := rl.currentRef()
	rl.freelance = projects
	rl.buildRows()
	if !rl.restoreCursor(prev) {
		rl.cursor = rl.firstSelectableRow()
	}
	rl.maybeFireSelectionChanged()
}

// SetShowArchived toggles archive-section visibility and rebuilds.
func (rl *railList) SetShowArchived(v bool) {
	if rl.showArchived == v {
		return
	}
	rl.showArchived = v
	prev := rl.currentRef()
	rl.buildRows()
	rl.restoreCursor(prev)
	rl.maybeFireSelectionChanged()
}

// ShowArchived reports the current archive visibility.
func (rl *railList) ShowArchived() bool { return rl.showArchived }

// CurrentRef returns the orchEntry or roleEntry the cursor is on, or
// nil for the archive separator / no rows. Callers type-switch on the
// returned value.
func (rl *railList) CurrentRef() any {
	return rl.currentRef()
}

func (rl *railList) currentRef() any {
	if rl.cursor < 0 || rl.cursor >= len(rl.rows) {
		return nil
	}
	r := rl.rows[rl.cursor]
	switch r.kind {
	case railRowOrch:
		return r.orch
	case railRowRole:
		return r.role
	case railRowFreelanceProj:
		return r.fproj
	}
	return nil
}

// SelectByRoleID moves the cursor to the row matching roleID. Returns
// true on success. Fires the selection-changed callback when the
// cursor lands on a different row.
func (rl *railList) SelectByRoleID(id int64) bool {
	for i, r := range rl.rows {
		if r.kind == railRowRole && r.role != nil && r.role.RoleID == id {
			rl.cursor = i
			rl.clampOffset()
			rl.maybeFireSelectionChanged()
			return true
		}
	}
	return false
}

// SelectByOrchID moves the cursor to the row matching orchID. Returns
// true on success. Fires the selection-changed callback when the
// cursor lands on a different row.
func (rl *railList) SelectByOrchID(id int64) bool {
	for i, r := range rl.rows {
		if r.kind == railRowOrch && r.orch != nil && r.orch.ID == id {
			rl.cursor = i
			rl.clampOffset()
			rl.maybeFireSelectionChanged()
			return true
		}
	}
	return false
}

// SelectByProject moves the cursor to the Freelance repo-group header for
// the given project. Returns true on success.
func (rl *railList) SelectByProject(project string) bool {
	for i, r := range rl.rows {
		if r.kind == railRowFreelanceProj && r.fproj != nil && r.fproj.Project == project {
			rl.cursor = i
			rl.clampOffset()
			rl.maybeFireSelectionChanged()
			return true
		}
	}
	return false
}

// CursorDown / CursorUp move selection by one selectable row and fire
// the selection-changed callback when the cursor actually moves.
func (rl *railList) CursorDown() { rl.move(1) }
func (rl *railList) CursorUp()   { rl.move(-1) }

func (rl *railList) move(dir int) {
	if len(rl.rows) == 0 {
		return
	}
	c := rl.cursor + dir
	for c >= 0 && c < len(rl.rows) {
		if rl.selectable(c) {
			rl.cursor = c
			rl.clampOffset()
			rl.maybeFireSelectionChanged()
			return
		}
		c += dir
	}
}

func (rl *railList) selectable(i int) bool {
	if i < 0 || i >= len(rl.rows) {
		return false
	}
	switch rl.rows[i].kind {
	case railRowOrch, railRowRole, railRowFreelanceProj:
		return true
	}
	return false
}

func (rl *railList) firstSelectableRow() int {
	for i := range rl.rows {
		if rl.selectable(i) {
			return i
		}
	}
	return 0
}

// ToggleCollapse flips the collapsed state of the orchestrator at the
// cursor. When the cursor is on a role row, the role's parent
// orchestrator is toggled.
func (rl *railList) ToggleCollapse() {
	if rl.cursor < 0 || rl.cursor >= len(rl.rows) {
		return
	}
	var orch *orchEntry
	var fproj *freelanceProject
	switch r := rl.rows[rl.cursor]; r.kind {
	case railRowOrch:
		orch = r.orch
	case railRowFreelanceProj:
		fproj = r.fproj
	case railRowRole:
		// A freelance task row (OrchestratorID 0) collapses its repo group;
		// a worker row collapses its orchestrator.
		if r.role != nil && r.role.OrchestratorID == 0 {
			for _, fp := range rl.freelance {
				for _, t := range fp.Tasks {
					if t == r.role {
						fproj = fp
						break
					}
				}
			}
		} else {
			for _, o := range rl.orchestrators {
				if r.role != nil && o.ID == r.role.OrchestratorID {
					orch = o
					break
				}
			}
		}
	}
	switch {
	case orch != nil:
		rl.collapsed[orch.ID] = !rl.collapsed[orch.ID]
	case fproj != nil:
		rl.freelanceCollapsed[fproj.Project] = !rl.freelanceCollapsed[fproj.Project]
	default:
		return
	}
	prev := rl.currentRef()
	rl.buildRows()
	rl.restoreCursor(prev)
	rl.maybeFireSelectionChanged()
}

func (rl *railList) restoreCursor(prev any) bool {
	switch ref := prev.(type) {
	case *roleEntry:
		if ref != nil && rl.SelectByRoleID(ref.RoleID) {
			return true
		}
		if ref != nil && rl.SelectByOrchID(ref.OrchestratorID) {
			return true
		}
	case *orchEntry:
		if ref != nil && rl.SelectByOrchID(ref.ID) {
			return true
		}
	case *freelanceProject:
		if ref != nil && rl.SelectByProject(ref.Project) {
			return true
		}
	}
	if rl.cursor >= len(rl.rows) {
		rl.cursor = len(rl.rows) - 1
	}
	if rl.cursor < 0 {
		rl.cursor = 0
	}
	if !rl.selectable(rl.cursor) {
		rl.cursor = rl.firstSelectableRow()
	}
	rl.clampOffset()
	return false
}

func (rl *railList) buildRows() {
	rl.rows = nil
	var active, archived []*orchEntry
	for _, o := range rl.orchestrators {
		if o.Archived {
			archived = append(archived, o)
		} else {
			active = append(active, o)
		}
	}

	appendOrch := func(o *orchEntry) {
		rl.rows = append(rl.rows, railRow{kind: railRowOrch, orch: o})
		if rl.collapsed[o.ID] {
			return
		}
		for _, role := range o.Roles {
			// Hidden by default unless `l listall`: hera-archived roles,
			// argus-archived tasks (so the active rail mirrors argus's
			// non-archived set), and dead bindings (DB-live but the argus
			// task is gone).
			if (role.Archived || role.ArgusArchived) && !rl.showArchived {
				continue
			}
			if role.Dead && !rl.showArchived {
				continue
			}
			rl.rows = append(rl.rows, railRow{kind: railRowRole, role: role})
		}
	}

	for _, o := range active {
		appendOrch(o)
	}

	// Freelance section: unmanaged argus tasks grouped by repo, rendered
	// below all project rows and above the Archive separator. The "Freelance"
	// separator only appears when at least one freelance repo group has live
	// rows, so the operator never lands on an empty section.
	if len(rl.freelance) > 0 {
		rl.rows = append(rl.rows, railRow{kind: railRowFreelanceSep})
		for _, fp := range rl.freelance {
			rl.rows = append(rl.rows, railRow{kind: railRowFreelanceProj, fproj: fp})
			if rl.freelanceCollapsed[fp.Project] {
				continue
			}
			for _, t := range fp.Tasks {
				if (t.Archived || t.ArgusArchived) && !rl.showArchived {
					continue
				}
				if t.Dead && !rl.showArchived {
					continue
				}
				rl.rows = append(rl.rows, railRow{kind: railRowRole, role: t})
			}
		}
	}

	if rl.showArchived && len(archived) > 0 {
		rl.rows = append(rl.rows, railRow{kind: railRowArchiveSep})
		for _, o := range archived {
			appendOrch(o)
		}
	}
	if len(rl.rows) == 0 {
		rl.rows = append(rl.rows, railRow{kind: railRowEmpty})
	}
}

// InputHandler routes KeyUp/KeyDown to the cursor and the spacebar to
// the collapse toggle. Every other key is left to the KeyRouter (Enter,
// j/k, rail mutation keys, focus traversal). The router translates j/k
// to KeyDown/KeyUp before they reach this widget.
func (rl *railList) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return rl.WrapInputHandler(func(e *tcell.EventKey, _ func(tview.Primitive)) {
		switch e.Key() {
		case tcell.KeyDown:
			rl.CursorDown()
		case tcell.KeyUp:
			rl.CursorUp()
		case tcell.KeyRune:
			if e.Rune() == ' ' {
				rl.ToggleCollapse()
			}
		}
	})
}

// Draw renders the rail's box, then walks visible rows.
func (rl *railList) Draw(screen tcell.Screen) {
	rl.Box.DrawForSubclass(screen, rl)
	x, y, w, h := rl.GetInnerRect()
	if w <= 0 || h <= 0 {
		return
	}
	if len(rl.rows) == 0 {
		return
	}

	rl.clampOffset()
	if rl.cursor < rl.offset {
		rl.offset = rl.cursor
	}
	if rl.cursor >= rl.offset+h {
		rl.offset = rl.cursor - h + 1
	}
	if rl.offset < 0 {
		rl.offset = 0
	}

	for i := 0; i < h; i++ {
		idx := rl.offset + i
		if idx >= len(rl.rows) {
			break
		}
		row := rl.rows[idx]
		cursor := idx == rl.cursor && rl.selectable(idx)
		switch row.kind {
		case railRowOrch:
			rl.drawOrchRow(screen, x, y+i, w, row.orch, cursor)
		case railRowRole:
			rl.drawRoleRow(screen, x, y+i, w, row.role, cursor)
		case railRowArchiveSep:
			rl.drawSeparator(screen, x, y+i, w, " Archive ")
		case railRowFreelanceSep:
			rl.drawSeparator(screen, x, y+i, w, " Freelance ")
		case railRowFreelanceProj:
			rl.drawFreelanceProjRow(screen, x, y+i, w, row.fproj, cursor)
		case railRowEmpty:
			widget.DrawText(screen, x, y+i, w, "(no projects)", theme.StyleDimmed)
		}
	}
}

func (rl *railList) clampOffset() {
	if rl.cursor < 0 {
		rl.cursor = 0
	}
	if rl.cursor >= len(rl.rows) && len(rl.rows) > 0 {
		rl.cursor = len(rl.rows) - 1
	}
}

func (rl *railList) drawOrchRow(screen tcell.Screen, x, y, w int, o *orchEntry, cursor bool) {
	if o == nil {
		return
	}
	if cursor {
		rl.fillBackground(screen, x, y, w)
	}

	chevron := '▾'
	if rl.collapsed[o.ID] {
		chevron = '▸'
	}
	col := x
	screen.SetContent(col, y, chevron, nil, tcell.StyleDefault.Foreground(theme.ColorDimmed))
	col += 2

	nameStyle := tcell.StyleDefault.Foreground(theme.ColorProject).Bold(true)
	if o.Archived {
		nameStyle = tcell.StyleDefault.Foreground(theme.ColorDimmed).Bold(true)
	}
	name := o.Name
	count := fmt.Sprintf(" (%d)", rl.visibleRoleCount(o))
	maxName := w - (col - x) - runeLen(count)
	if maxName < 0 {
		maxName = 0
	}
	name = truncRunes(name, maxName)
	widget.DrawText(screen, col, y, maxName, name, nameStyle)
	col += runeLen(name)
	if col-x+runeLen(count) <= w {
		widget.DrawText(screen, col, y, runeLen(count), count, tcell.StyleDefault.Foreground(theme.ColorDimmed))
	}
}

func (rl *railList) visibleRoleCount(o *orchEntry) int {
	if o == nil {
		return 0
	}
	if rl.showArchived {
		return len(o.Roles)
	}
	n := 0
	for _, r := range o.Roles {
		if r.Archived || r.Dead || r.ArgusArchived {
			continue
		}
		n++
	}
	return n
}

func (rl *railList) drawRoleRow(screen tcell.Screen, x, y, w int, r *roleEntry, cursor bool) {
	if r == nil {
		return
	}
	if cursor {
		rl.fillBackground(screen, x, y, w)
	}

	// Layout: " <icon> name           10m"
	prefix := " "
	col := x
	widget.DrawText(screen, col, y, runeLen(prefix), prefix, theme.StyleDefault)
	col += runeLen(prefix)

	icon, iconStyle := rl.roleIcon(r)
	screen.SetContent(col, y, icon, nil, iconStyle)
	col += 2 // icon + space

	nameStyle := theme.StyleNormal
	if r.Archived || r.Dead || r.ArgusArchived {
		nameStyle = theme.StyleDimmed
	}
	if cursor {
		nameStyle = theme.StyleSelected
	}

	elapsed := rl.elapsed(r)

	maxName := w - (col - x) - runeLen(elapsed) - 1
	if maxName < 0 {
		maxName = 0
	}
	name := truncRunes(r.Name, maxName)
	widget.DrawText(screen, col, y, maxName, name, nameStyle)
	col += runeLen(name)

	if elapsed != "" {
		elapsedCol := x + w - runeLen(elapsed)
		if elapsedCol > col {
			widget.DrawText(screen, elapsedCol, y, runeLen(elapsed), elapsed, tcell.StyleDefault.Foreground(theme.ColorElapsed))
		}
	}
}

// drawFreelanceProjRow renders a Freelance repo-group header: a collapse
// chevron, the project name, and the right-aligned live count. Mirrors the
// orchestrator header so the operator reads the rail uniformly.
func (rl *railList) drawFreelanceProjRow(screen tcell.Screen, x, y, w int, fp *freelanceProject, cursor bool) {
	if fp == nil {
		return
	}
	if cursor {
		rl.fillBackground(screen, x, y, w)
	}
	chevron := '▾'
	if rl.freelanceCollapsed[fp.Project] {
		chevron = '▸'
	}
	col := x
	screen.SetContent(col, y, chevron, nil, tcell.StyleDefault.Foreground(theme.ColorDimmed))
	col += 2

	nameStyle := tcell.StyleDefault.Foreground(theme.ColorProject).Bold(true)
	name := fp.Project
	count := fmt.Sprintf(" (%d)", rl.visibleFreelanceCount(fp))
	maxName := w - (col - x) - runeLen(count)
	if maxName < 0 {
		maxName = 0
	}
	name = truncRunes(name, maxName)
	widget.DrawText(screen, col, y, maxName, name, nameStyle)
	col += runeLen(name)
	if col-x+runeLen(count) <= w {
		widget.DrawText(screen, col, y, runeLen(count), count, tcell.StyleDefault.Foreground(theme.ColorDimmed))
	}
}

func (rl *railList) visibleFreelanceCount(fp *freelanceProject) int {
	if fp == nil {
		return 0
	}
	if rl.showArchived {
		return len(fp.Tasks)
	}
	n := 0
	for _, t := range fp.Tasks {
		if t.Archived || t.Dead || t.ArgusArchived {
			continue
		}
		n++
	}
	return n
}

func (rl *railList) drawSeparator(screen tcell.Screen, x, y, w int, label string) {
	style := tcell.StyleDefault.Foreground(theme.ColorDimmed)
	dashes := w - runeLen(label)
	if dashes < 0 {
		dashes = 0
	}
	left := dashes / 2
	right := dashes - left
	col := x
	for i := 0; i < left; i++ {
		screen.SetContent(col, y, '─', nil, style)
		col++
	}
	widget.DrawText(screen, col, y, runeLen(label), label, style.Bold(true))
	col += runeLen(label)
	for i := 0; i < right; i++ {
		screen.SetContent(col, y, '─', nil, style)
		col++
	}
}

func (rl *railList) fillBackground(screen tcell.Screen, x, y, w int) {
	bg := tcell.StyleDefault.Background(theme.ColorHighlight)
	for i := 0; i < w; i++ {
		screen.SetContent(x+i, y, ' ', nil, bg)
	}
}

// roleIcon picks the moon-style icon for a role based on its live
// binding state. Live → IconMoonStars (active / needs attention). Idle
// (no live binding) → IconMoonOutline. Archived or dead (binding open
// in DB but argus task is gone) → dimmed circle.
func (rl *railList) roleIcon(r *roleEntry) (rune, tcell.Style) {
	if r.Archived || r.Dead || r.ArgusArchived {
		return '○', theme.StyleDimmed
	}
	// Prefer argus-reported state so the rail mirrors argus reality:
	// needs-input, then done (complete/in_review), then in-progress
	// idle/working, then pending.
	if r.HasState {
		switch {
		case r.NeedsInput:
			return theme.IconNeedsInput, theme.StyleNeedsInput
		case r.Status == "complete":
			return '✓', theme.StyleComplete
		case r.Status == "in_review":
			return '✓', theme.StyleInReview
		case r.Status == "in_progress" && r.ArgusIdle:
			return theme.IconMoonOutline, theme.StyleDimmed
		case r.Status == "in_progress":
			return theme.IconMoonStars, theme.StyleInProgress
		case r.Status == "pending":
			return theme.IconMoonOutline, theme.StylePending
		}
	}
	// Fallback when argus state is unknown: hera binding presence.
	if r.Live {
		return theme.IconMoonStars, theme.StyleInReview
	}
	return theme.IconMoonOutline, tcell.StyleDefault.Foreground(theme.ColorInReview)
}

// elapsed formats the time since r.StartedAt using argus's "10s/10m/10h/10d"
// shape. Returns empty when the role has no meaningful start time.
func (rl *railList) elapsed(r *roleEntry) string {
	if r.ElapsedOverride != "" {
		return r.ElapsedOverride
	}
	if r.StartedAt.IsZero() {
		return ""
	}
	now := time.Now
	if rl.now != nil {
		now = rl.now
	}
	d := now().Sub(r.StartedAt)
	if d < 0 {
		return ""
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	}
}

func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

func truncRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if runeLen(s) <= max {
		return s
	}
	out := make([]rune, 0, max)
	for _, r := range s {
		if len(out) >= max {
			break
		}
		out = append(out, r)
	}
	return string(out)
}
