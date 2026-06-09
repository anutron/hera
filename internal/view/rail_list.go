package view

import (
	"fmt"
	"maps"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/anutron/argus-sdk/theme"
	"github.com/anutron/argus-sdk/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/anutron/hera/internal/db"
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
	// archivedFreelance holds archived freelancers (unmanaged argus tasks the
	// operator archived with `a`) that would otherwise vanish from the rail.
	// They render ONLY inside the bottom-of-rail Archive section (never inline
	// in their Freelance repo group), reachable WITHOUT `l` — the fix for the
	// archived-freelancer data-loss gap.
	archivedFreelance []*roleEntry
	rows              []railRow
	cursor            int
	offset            int

	// lastSnapCursor is the cursor value observed by the previous Draw. The
	// viewport snaps to keep the cursor visible ONLY when the cursor has
	// moved since then — so a wheel pan (PanBy) survives refresh repaints
	// (DAO ticks redraw the rail constantly) until the next j/k.
	lastSnapCursor int

	// lastHeight is the inner-rect height observed by the previous Draw.
	// PanBy uses it to clamp the viewport to the content without waiting
	// for the next draw; 0 means "never drawn" (clamp loosely, Draw fixes).
	lastHeight int

	// collapsed tracks the operator's EXPLICIT fold choices, keyed by
	// orchestrator ID. An entry present means the operator toggled that
	// coordinator (true=collapsed, false=expanded) and that choice persists
	// for the session; an absent key means "never touched" and the DEFAULT
	// applies — expanded, except a coordinator with zero non-archived
	// children defaults collapsed (orchCollapsed resolves the effective
	// state). Reads go through orchCollapsed, never this map directly.
	collapsed map[int64]bool

	// freelanceCollapsed tracks which Freelance repo groups are collapsed,
	// keyed by project name. Default expanded — surfacing freelancers is the
	// whole point (the operator should never leave hera to notice an
	// unmanaged agent needs attention).
	freelanceCollapsed map[string]bool

	// pinnedFreelance tracks which freelance tasks (keyed by ArgusTaskID)
	// are pinned. Freelancers have no hera DB row, so their pin state lives
	// here alongside freelanceCollapsed rather than in the roles table.
	// A pinned freelancer floats to the root level of the Pinned block.
	pinnedFreelance map[string]bool

	// archiveExpanded tracks which Archive expandos are folded open, keyed
	// by owner: an orchestrator ID for a per-coordinator Archive expando, or
	// archiveTopLevelOwner (0) for the consolidated bottom Archive. Default
	// (zero value) is collapsed — archived items stay tucked away until the
	// operator folds them open (D14). showArchived (`l`) forces every expando open.
	archiveExpanded map[int64]bool

	// archiveGroupExpanded tracks which sub-groups inside the consolidated bottom
	// Archive are folded open (BUG-026), keyed by group name ("Hera sessions",
	// "ARGUS", "Hera", …). Default (zero value) is collapsed — sub-groups stay
	// tucked until the operator expands them. Persisted via railViewState.
	archiveGroupExpanded map[string]bool

	// showArchived, when true, force-expands every Archive expando and reveals
	// dead bindings — the `l` listall convenience. The per-owner expandos are
	// always reachable regardless of this flag (D14).
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

	// filtering reports whether the rail is in search INPUT mode (the operator
	// pressed `/` and is typing). While true the KeyRouter yields keys to the
	// rail so they become filter input rather than mutation triggers; Enter
	// accepts (leaves input mode, keeps the query) and Esc clears + exits.
	filtering bool

	// filter is the active search query. An empty string means "no filter"
	// (the full rail renders); a non-empty query narrows buildRows to rows
	// whose name matches (case-insensitive substring, whitespace-separated
	// terms), ancestry-preserving. Survives leaving input mode (Enter accept)
	// until Esc / ClearFilter restores the full rail.
	filter string

	// probeMarker gates the `›` selection-marker glyph. The operator does not
	// want the marker in normal use (selection is shown by theme.StyleSelected
	// alone); the live-probe harness relies on it to locate the cursor in
	// colorless text captures. Set from the probe env (HERA_LIVE_PROBE) at
	// construction so the daemon renders the glyph only under a probe run;
	// tests set it directly. The marker GUTTER is reserved regardless, so
	// toggling it never shifts row content.
	probeMarker bool

	// onStateChanged fires after ToggleCollapse changes any fold state.
	// The App wires this to persist fold choices to the ConfigDAO so they
	// survive daemon restarts. Runs on the tview event loop; must not block.
	onStateChanged func()

	// hasRunning reports whether any visible row renders the animated
	// spinner (a known in_progress state, not idle, not blocked on input).
	// Recomputed by buildRows on the event loop; read atomically by the
	// App's spinner driver goroutine to decide whether to schedule spinner
	// repaints (mirroring argus's spinnerLoop: tick only when live work).
	hasRunning atomic.Bool
}

// HasRunningRows reports whether the rail currently contains a row in the
// running state (animated spinner). Safe to call from any goroutine.
func (rl *railList) HasRunningRows() bool {
	return rl.hasRunning.Load()
}

// rowRunning reports whether a rail row renders the animated spinner: a KNOWN
// in_progress state that is neither idle nor blocked on input. Archived rows
// count too — they render the spinner dimmed (the glyph never lies).
func rowRunning(r railRow) bool {
	switch r.kind {
	case railRowRole:
		if r.role != nil {
			return r.role.HasState && r.role.Status == "in_progress" && !r.role.ArgusIdle && !r.role.NeedsInput
		}
	case railRowOrch:
		if r.orch != nil {
			return r.orch.CoordHasState && r.orch.CoordStatus == "in_progress" && !r.orch.CoordIdle && !r.orch.CoordNeedsInput
		}
	}
	return false
}

// orchEntry is one orchestrator and its agent roles, in render order.
// Coord roles do not appear in Roles; the orchestrator-level CoordTaskID
// is the binding the COORD pane targets when this orchestrator is
// selected. Archived entries float to the bottom behind a separator
// when showArchived is true.
type orchEntry struct {
	ID       int64
	Name     string
	Archived bool
	// Pinned floats this coordinator into the Pinned section at the rail top
	// (with its subtree), mirroring argus. Pin and archive are mutually
	// exclusive, so a Pinned orchestrator is never Archived.
	Pinned      bool
	CoordTaskID string
	// CoordRoleID is the orchestrator's coord role id, captured so the
	// resurrect-on-Enter flow can target the coord role when the operator
	// presses Enter on this (archived root coordinator) header. Zero when the
	// orchestrator has no coord role.
	CoordRoleID int64
	Roles       []*roleEntry

	// Coord-task argus state, populated from the ArgusStateCache for the
	// orchestrator's CoordTaskID. Drives the status icon on the coordinator
	// header so it mirrors argus reality (☾ working, ○ idle, ✓ complete/review,
	// ? needs-input) the same way a worker row does. CoordHasState is false when
	// argus has no entry for the coord task (cold cache / no coord) — the icon
	// then falls back to coord-binding presence.
	CoordHasState   bool
	CoordStatus     string // pending | in_progress | in_review | complete
	CoordIdle       bool
	CoordNeedsInput bool

	// CoordArgusArchived is the coord task's argus-side archived bit. With the
	// orchestrator itself displayed ACTIVE (Archived false) this is the
	// MIXED-COORD state — only external argus-side archiving produces it — and
	// the header renders the ⊘ repair cue instead of the status glyph, while
	// `a` repairs (unarchives the coord task) instead of cascade-archiving.
	CoordArgusArchived bool

	// CoordArgusName is the argus task name for the coord's task (the name
	// shown in the argus view, e.g. "the-hera-detail-when-viewing"). Populated
	// by populateRail from the state cache via TaskInfoProvider; empty when the
	// cache has no entry for the coord task (cold cache, archived, or dead).
	CoordArgusName string

	// CoordWorktreePath is the filesystem path of the coord task's git worktree,
	// as reported by argus (e.g. "/Users/aaron/.argus/worktrees/Hera/foo").
	// Populated by populateRail from the state cache via TaskInfoProvider;
	// buildCoordDetails falls back to the hera DB binding when this is empty.
	CoordWorktreePath string
}

// roleEntry is one role under an orchestrator. Live indicates an open
// binding; StartedAt is the binding's StartedAt when live, else the
// role's CreatedAt — used to render the right-aligned elapsed time.
// Dead means the argus task RECORD no longer exists (404 / pruned —
// absent from the warm state cache). Task STATUS never sets Dead: a
// completed task whose record exists renders active with its ✓ glyph.
// Dead rows are hidden by default and rendered dimmed when showArchived
// is true.
type roleEntry struct {
	OrchestratorID int64
	RoleID         int64
	RoleKind       string
	Name           string
	Live           bool
	Dead           bool
	ArgusTaskID    string
	Archived       bool
	// Pinned floats this agent OUT of its coordinator into the Pinned section
	// at the rail top, as a standalone row (unless its coordinator is itself
	// pinned, in which case it renders nested under the pinned coordinator).
	// Mutually exclusive with the archived state.
	Pinned    bool
	StartedAt time.Time

	// CoordRoleID is the coord role id of the orchestrator this role belongs
	// to (the same value as the owning orchEntry.CoordRoleID, captured when the
	// row is built). Carried on the row so CurrentRailSelection can pass it to
	// `w` (OnNewWorker), which spawns the new worker under that coordinator.
	// Zero when the orchestrator has no coord role.
	CoordRoleID int64

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

	// PRState is the GitHub PR review state string from argus's daemon poll
	// (awaiting-review / changes-requested / approved / ...). Empty when argus
	// does not populate the field or the state is non-actionable. Read from
	// the ArgusStateCache — no per-row HTTP calls.
	PRState string

	// ElapsedOverride, when non-empty, is rendered verbatim in the elapsed
	// column instead of computing from StartedAt. Freelance rows use argus's
	// pre-formatted age string so their column matches argus's own rail.
	ElapsedOverride string

	// WorktreePath is the argus task's worktree directory, carried on
	// freelance rows so `^p` can open a PR straight from it (a freelancer has
	// no hera binding to resolve the path from). Empty for managed roles.
	WorktreePath string

	// Project is the argus repo this freelance task belongs to, carried on
	// freelance rows so `J` adoption records it as the adopted worker role's
	// argus_project. Empty for managed roles.
	Project string

	// childOrch is set when this worker role is ALSO the coordinator of another
	// orchestrator — a sub-coordinator (a multi-binding: this role's ArgusTaskID
	// equals childOrch.CoordTaskID). When non-nil, the rail renders this row as a
	// foldable coordinator row (chevron + 󰹻 marker + count) and nests
	// childOrch's roles one level deeper, recursively. populateRail promotes the
	// role's RoleKind to coordinator when it sets this, so selection/PR/icon
	// logic treats it like any other coordinator. nil for plain leaf workers.
	childOrch *orchEntry
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
	railRowFreelanceSep
	railRowFreelanceProj
	railRowArchiveExpando
	railRowPinnedSep
	// railRowPinnedEnd is a non-selectable closing separator rule drawn
	// immediately below the last row of the Pinned block, so the operator can
	// see clearly where Pinned ends and the active tree begins.
	railRowPinnedEnd
	// railRowPinnedBreadcrumb is a SELECTABLE row that renders line 1 of a
	// two-line pinned-sub-item entry (BUG-025): the status icon + dimmed
	// ancestry trail ("kbtest › nested-sub › "). The immediately-following
	// railRowRole with isBreadcrumbContinuation renders line 2 (the name +
	// age) and is non-selectable — the two lines act as one unit for j/k.
	railRowPinnedBreadcrumb
	// railRowSectionRule is a non-selectable plain horizontal rule used to
	// delineate rail sections (BUG-026): between the active coord tree and the
	// Freelance section, and between the Freelance section and the consolidated
	// Archive section. Renders identically to railRowPinnedEnd (plain rule, no
	// label). Kept separate so tests can distinguish it from the Pinned closing rule.
	railRowSectionRule
	// railRowArchiveGroup is a SELECTABLE sub-group header inside the
	// consolidated bottom Archive section (BUG-026): "Hera sessions" for archived
	// root coordinators, and per-project groups for archived freelancers. Renders
	// like a Freelance project header (chevron + name + count) but keyed by
	// archiveGroup name rather than freelanceCollapsed.
	railRowArchiveGroup
	railRowEmpty
)

// archiveTopLevelOwner is the owner key for the consolidated bottom Archive
// expando (BUG-026). Per-coordinator Archive expandos are keyed by their
// orchestrator's ID (always > 0). The bottom Archive holds archived root
// coordinators ("Hera sessions" sub-group) and archived freelancers
// (per-project sub-groups keyed by archiveGroup name).
const archiveTopLevelOwner int64 = 0

// iconCoord marks a coordinator/orchestrator row, drawn between the chevron and
// the name in addition to the status icon. A coordinator is a node that
// dispatches work to a team of agents; the marker makes coord rows scannable
// at a glance (the prototype's ◆ is superseded by argus's moon/✓/? status set
// for state, so the coordinator identity needs its own glyph). nf-md U+F0E7B.
const iconCoord = rune(0x0F0E7B) // 󰹻

// iconFreelance is the (F)-in-a-box marker drawn on every freelance row (both
// in the Freelance section and when pinned) so freelancers are visually
// distinct from managed agents and coordinators at a glance. Uses
// nf-md-alpha_f_box_outline (U+F0BFA), sourced from the post-table glyph names
// of HackNerdFont-Regular.ttf v3 (confirmed present; prior codepoint U+F0229
// was md-file_presentation_box — wrong icon). The filled variant (alpha_f_box)
// is at U+F0B0D in the same font.
const iconFreelance = rune(0x0F0BFA)

// iconCoordBroken is the mixed-coord repair cue: rendered at a coordinator
// header's status-icon cell (in error red) when the orchestrator displays as
// ACTIVE while its coord-pane binding's argus task is ARCHIVED. Cue choice
// (documented per the operator ruling): ⊘ — U+2298 CIRCLED DIVISION SLASH —
// reads "void/blocked"; the error-red styling is distinct from the orange
// needs-input ?, from the dimmed-archived treatment, and from the cyan 󰹻
// coord marker that keeps rendering beside it. Plain Unicode (no Nerd Font
// dependency), one cell wide.
const iconCoordBroken = '⊘'

type railRow struct {
	kind  railRowKind
	orch  *orchEntry
	role  *roleEntry
	fproj *freelanceProject

	// depth is the row's nesting level, used for indentation (depth*indentStep
	// columns). A root coordinator is depth 0; its direct children are depth 1;
	// a sub-coordinator's children are depth 2; and so on. An Archive expando
	// sits at its owner's child depth, and its archived children one deeper.
	depth int

	// archiveOwner / archiveCount populate a railRowArchiveExpando: the owner
	// is an orchestrator ID (per-coordinator) or archiveTopLevelOwner.
	archiveOwner int64
	archiveCount int

	// archiveGroup is the sub-group name for railRowArchiveGroup rows (BUG-026):
	// "Hera sessions" for archived root coordinators, or a project name for
	// archived freelancers. Empty for all other row kinds.
	archiveGroup string

	// breadcrumb is the ancestry trail text for railRowPinnedBreadcrumb rows
	// (BUG-025), e.g. "kbtest › nested-sub › ". Empty for all other row kinds.
	breadcrumb string

	// isBreadcrumbContinuation marks a railRowRole as the non-selectable name
	// line (line 2) of a two-line pinned-sub-item entry (BUG-025). The cursor
	// anchors on the preceding railRowPinnedBreadcrumb; this row carries the
	// item's name + age and is always rendered at depth > 0 (indented under
	// its breadcrumb line).
	isBreadcrumbContinuation bool
}

// pinnedRole pairs a floating pinned managed role with its computed ancestry
// trail (BUG-025). The ancestry is always non-empty for managed roles (they
// always have a parent coordinator) and has the form "root › sub › ".
type pinnedRole struct {
	role     *roleEntry
	ancestry string
}

// indentStep is the per-depth indentation in columns. Mirrors the prototype's
// 16px-per-level nesting (rail-nav.html `pad=8+depth*16`) at terminal scale.
const indentStep = 2

// selectionMarker is the color-independent selection indicator. The rail
// reserves a markerGutter-wide column at the start of EVERY row and draws
// this glyph there on the selected row only (in ADDITION to
// theme.StyleSelected text); all other rows draw a space, so nothing shifts
// when the cursor moves. The styling alone is invisible in any monochrome
// context — the live-probe grid renderer strips styling, and screen readers
// and reduced-color terminals never see it — which made blind rail
// navigation land mutation keys on the wrong rows.
const selectionMarker = '›'

// markerGutter is the width of the selection-marker gutter: the marker cell
// plus a separating space.
const markerGutter = 2

// newRailList constructs an empty rail widget. SetBorder is enabled so
// OnFocusChanged's SetBorderColor still drives the focus-color paint.
// The rail title is empty by default (BUG-026: "Rail" label removed); it only
// shows the active filter query (e.g. "/scout") while a search is in progress.
func newRailList() *railList {
	rl := &railList{
		Box:                  tview.NewBox(),
		collapsed:            map[int64]bool{},
		freelanceCollapsed:   map[string]bool{},
		archiveExpanded:      map[int64]bool{},
		archiveGroupExpanded: map[string]bool{},
		pinnedFreelance:      map[string]bool{},
		lastFiredCursor:      -1,
		probeMarker:          probeMarkerFromEnv(),
	}
	rl.SetBorder(true)
	return rl
}

// probeMarkerFromEnv reports whether the `›` selection marker should render,
// gated on the same env var that gates the live-probe harness
// (HERA_LIVE_PROBE). Unset in normal operation (no glyph); set when the daemon
// is launched for a probe run so text captures can still locate the cursor.
func probeMarkerFromEnv() bool { return os.Getenv("HERA_LIVE_PROBE") == "1" }

// Filtering reports whether the rail is in search INPUT mode (the operator is
// typing a query). The KeyRouter consults this (via the RailFilter gate) to
// yield keys to the rail while filtering. False after Enter accepts the filter,
// even though the query stays applied — so j/k resume navigating.
func (rl *railList) Filtering() bool { return rl.filtering }

// Filter returns the active search query ("" when no filter is applied).
func (rl *railList) Filter() string { return rl.filter }

// filterActive reports whether a query is currently narrowing the rail,
// independent of input mode (a query accepted with Enter is still active).
func (rl *railList) filterActive() bool { return rl.filter != "" }

// BeginFilter enters search input mode (the `/` key). The query is unchanged;
// the bottom-of-rail input line appears on the next draw.
func (rl *railList) BeginFilter() {
	rl.filtering = true
}

// AcceptFilter leaves input mode while KEEPING the query applied (the Enter
// key) so j/k navigate the filtered set.
func (rl *railList) AcceptFilter() {
	rl.filtering = false
}

// ClearFilter clears the query and leaves input mode (the Esc key), restoring
// the full unfiltered rail.
func (rl *railList) ClearFilter() {
	rl.filter = ""
	rl.filtering = false
	rl.applyFilter()
}

// SetFilter replaces the query and rebuilds. Primarily a test seam; the live
// path appends/deletes runes via HandleFilterKey.
func (rl *railList) SetFilter(q string) {
	rl.filter = q
	rl.applyFilter()
}

// applyFilter rebuilds rows under the current query, preserves the cursor where
// possible (falling to the first selectable row when the selection filtered
// out), refreshes the title, and fires the selection-changed callback.
func (rl *railList) applyFilter() {
	prev := rl.currentRef()
	rl.buildRows()
	if !rl.restoreCursor(prev) && prev != nil {
		rl.cursor = rl.firstSelectableRow()
	}
	rl.updateTitle()
	rl.maybeFireSelectionChanged()
}

// updateTitle reflects the active query in the rail border title so the
// operator always sees what is filtering the view (e.g. "/scout"). The title
// is empty when no filter is active — the "Rail" label was removed (BUG-026:
// redundant label, rail identity is self-evident from position and content).
func (rl *railList) updateTitle() {
	if rl.filter != "" {
		rl.SetTitle("/" + rl.filter)
		return
	}
	rl.SetTitle("")
}

// HandleFilterKey processes one key while in search input mode: Esc clears and
// exits, Enter accepts (keeps the query, exits input mode), Backspace deletes
// the last rune, Up/Down navigate the filtered set, and any other rune is
// appended to the query. Called by the rail InputHandler (the KeyRouter yields
// to it while filtering).
func (rl *railList) HandleFilterKey(e *tcell.EventKey) {
	switch e.Key() {
	case tcell.KeyEsc:
		rl.ClearFilter()
	case tcell.KeyEnter:
		rl.AcceptFilter()
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if rl.filter != "" {
			r := []rune(rl.filter)
			rl.filter = string(r[:len(r)-1])
			rl.applyFilter()
		}
	case tcell.KeyUp:
		rl.CursorUp()
	case tcell.KeyDown:
		rl.CursorDown()
	case tcell.KeyRune:
		rl.filter += string(e.Rune())
		rl.applyFilter()
	}
}

// matchesFilter reports whether name satisfies the active query: every
// whitespace-separated term must be a case-insensitive substring of name. An
// empty query matches everything.
func (rl *railList) matchesFilter(name string) bool {
	return rl.matchesFilterFields(name)
}

// matchesFilterFields is matchesFilter across MULTIPLE candidate fields: every
// query term must match at least one field (case-insensitive substring). Used
// for freelance rows, where a term may match the task name OR its repo —
// mirroring argus's name-or-project filter.
func (rl *railList) matchesFilterFields(fields ...string) bool {
	if rl.filter == "" {
		return true
	}
	terms := strings.Fields(strings.ToLower(rl.filter))
	lowered := make([]string, len(fields))
	for i, f := range fields {
		lowered[i] = strings.ToLower(f)
	}
	for _, term := range terms {
		hit := false
		for _, f := range lowered {
			if strings.Contains(f, term) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// orchMatches reports whether a coordinator stays visible under the active
// query: its own name matches, or any descendant role's name matches (walking
// sub-coordinators), so a matching agent always keeps its ancestry. seen guards
// cyclic multi-binding chains.
func (rl *railList) orchMatches(o *orchEntry) bool {
	return rl.orchMatchesSeen(o, map[int64]bool{})
}

func (rl *railList) orchMatchesSeen(o *orchEntry, seen map[int64]bool) bool {
	if o == nil || seen[o.ID] {
		return false
	}
	seen[o.ID] = true
	if rl.matchesFilter(o.Name) {
		return true
	}
	for _, r := range o.Roles {
		if rl.matchesFilter(r.Name) {
			return true
		}
		if r.childOrch != nil && rl.orchMatchesSeen(r.childOrch, seen) {
			return true
		}
	}
	return false
}

// roleVisible reports whether role under coordinator o renders under the active
// query: the coordinator name matches (show the whole team), the role name
// matches, or the role is a sub-coordinator with a matching descendant
// (ancestry to a deeper match). Always true when no filter is active.
func (rl *railList) roleVisible(o *orchEntry, role *roleEntry) bool {
	if !rl.filterActive() {
		return true
	}
	if o != nil && rl.matchesFilter(o.Name) {
		return true
	}
	if rl.matchesFilter(role.Name) {
		return true
	}
	return role.childOrch != nil && rl.orchMatches(role.childOrch)
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

// SetOnStateChanged registers a callback that fires after ToggleCollapse
// changes any fold state (coordinator collapse, freelance group collapse,
// archive expando). Runs on the tview event loop; the callback must not block.
func (rl *railList) SetOnStateChanged(fn func()) {
	rl.onStateChanged = fn
}

// maybeFireStateChanged invokes the registered fold-state callback if any.
func (rl *railList) maybeFireStateChanged() {
	if rl.onStateChanged != nil {
		rl.onStateChanged()
	}
}

// ViewState returns a snapshot of the rail's current fold choices (coordinator
// collapse, freelance group collapse, archive expando, archive sub-group
// expando) and the last-selected row identity (BUG-001). The maps are cloned
// so the caller can hand them to a background goroutine without data races.
// Called on the tview event loop (inside the onStateChanged / selection-save
// callbacks).
func (rl *railList) ViewState() railViewState {
	s := railViewState{
		Collapsed:            maps.Clone(rl.collapsed),
		FreelanceCollapsed:   maps.Clone(rl.freelanceCollapsed),
		ArchiveExpanded:      maps.Clone(rl.archiveExpanded),
		PinnedFreelance:      maps.Clone(rl.pinnedFreelance),
		ArchiveGroupExpanded: maps.Clone(rl.archiveGroupExpanded),
	}
	// Capture the current cursor identity so the next hera-view open can
	// restore the operator to the same row (BUG-001). FreelanceProject headers
	// carry no stable ID, so they fall back to zero (topmost item on restore).
	switch ref := rl.currentRef().(type) {
	case *roleEntry:
		if ref != nil {
			s.LastSelection.RoleID = ref.RoleID
			s.LastSelection.ArgusTaskID = ref.ArgusTaskID
		}
	case *orchEntry:
		if ref != nil {
			s.LastSelection.OrchID = ref.ID
		}
	}
	return s
}

// RestoreViewState applies a previously-saved fold state. Only non-nil maps
// overwrite the existing maps; a zero-value (fresh DB) is a no-op.
// Called before the first populateRail/SetOrchestrators, so no buildRows
// call is needed here — the subsequent SetOrchestrators drives that.
func (rl *railList) RestoreViewState(s railViewState) {
	if s.Collapsed != nil {
		rl.collapsed = s.Collapsed
	}
	if s.FreelanceCollapsed != nil {
		rl.freelanceCollapsed = s.FreelanceCollapsed
	}
	if s.ArchiveExpanded != nil {
		rl.archiveExpanded = s.ArchiveExpanded
	}
	if s.PinnedFreelance != nil {
		rl.pinnedFreelance = s.PinnedFreelance
	}
	if s.ArchiveGroupExpanded != nil {
		rl.archiveGroupExpanded = s.ArchiveGroupExpanded
	}
}

// IsFreelancePinned reports whether the freelance task with the given ArgusTaskID
// is pinned. Used by CurrentRailSelection to populate Pinned for freelance rows.
func (rl *railList) IsFreelancePinned(argusTaskID string) bool {
	return argusTaskID != "" && rl.pinnedFreelance[argusTaskID]
}

// ToggleFreelancePin flips the pinned state of the freelance task identified by
// argusTaskID, rebuilds rows, and fires the state-changed callback so the new
// pin state persists to the DB (same pattern as ToggleCollapse / freelanceCollapsed).
// Must be called on the tview event loop.
func (rl *railList) ToggleFreelancePin(argusTaskID string) {
	if argusTaskID == "" {
		return
	}
	rl.pinnedFreelance[argusTaskID] = !rl.pinnedFreelance[argusTaskID]
	// Clean up explicit false entries so the map stays compact (same as
	// orchCollapsed's approach: absent key = default).
	if !rl.pinnedFreelance[argusTaskID] {
		delete(rl.pinnedFreelance, argusTaskID)
	}
	prev := rl.currentRef()
	rl.buildRows()
	rl.restoreCursor(prev)
	rl.maybeFireSelectionChanged()
	rl.maybeFireStateChanged()
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
	if !rl.restoreCursor(prev) && prev != nil {
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
	if !rl.restoreCursor(prev) && prev != nil {
		rl.cursor = rl.firstSelectableRow()
	}
	rl.maybeFireSelectionChanged()
}

// SetArchivedFreelance replaces the rail's archived-freelancer set (archived
// unmanaged argus tasks) and rebuilds, preserving the cursor where possible.
// These render only inside the bottom-of-rail Archive section. Called by
// populateRail alongside SetFreelance / SetOrchestrators.
func (rl *railList) SetArchivedFreelance(tasks []*roleEntry) {
	prev := rl.currentRef()
	rl.archivedFreelance = tasks
	rl.buildRows()
	if !rl.restoreCursor(prev) && prev != nil {
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
	case railRowPinnedBreadcrumb:
		// Two-line pinned entry (BUG-025): cursor is on the breadcrumb line;
		// the ref is the role, identical to selecting the name line below it.
		return r.role
	}
	// railRowArchiveExpando is selectable (so space/Enter folds it) but is
	// not pane-bindable, so currentRef reports nil for it like a separator.
	return nil
}

// SelectByRoleID moves the cursor to the row matching roleID. Returns
// true on success. Fires the selection-changed callback when the
// cursor lands on a different row. For two-line pinned entries (BUG-025),
// the cursor lands on the railRowPinnedBreadcrumb row (line 1).
func (rl *railList) SelectByRoleID(id int64) bool {
	for i, r := range rl.rows {
		switch {
		case r.kind == railRowPinnedBreadcrumb && r.role != nil && r.role.RoleID == id:
			rl.cursor = i
			rl.clampOffset()
			rl.maybeFireSelectionChanged()
			return true
		case r.kind == railRowRole && !r.isBreadcrumbContinuation && r.role != nil && r.role.RoleID == id:
			rl.cursor = i
			rl.clampOffset()
			rl.maybeFireSelectionChanged()
			return true
		}
	}
	return false
}

// SelectByArgusTaskID moves the cursor to the role row whose bound argus task
// matches id. Returns true on success. This is the stable restore identity for
// freelance rows, which carry RoleID==0 (so SelectByRoleID can't disambiguate
// between freelancers) but always carry a unique ArgusTaskID. For two-line
// pinned entries (BUG-025), the cursor lands on the breadcrumb row (line 1).
func (rl *railList) SelectByArgusTaskID(id string) bool {
	if id == "" {
		return false
	}
	for i, r := range rl.rows {
		switch {
		case r.kind == railRowPinnedBreadcrumb && r.role != nil && r.role.ArgusTaskID == id:
			rl.cursor = i
			rl.clampOffset()
			rl.maybeFireSelectionChanged()
			return true
		case r.kind == railRowRole && !r.isBreadcrumbContinuation && r.role != nil && r.role.ArgusTaskID == id:
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

// SelectByArchiveOwner moves the cursor to the Archive expando header for
// the given owner (an orchestrator ID, or archiveTopLevelOwner for the
// bottom-of-rail Archive). Returns true on success.
func (rl *railList) SelectByArchiveOwner(owner int64) bool {
	for i, r := range rl.rows {
		if r.kind == railRowArchiveExpando && r.archiveOwner == owner {
			rl.cursor = i
			rl.clampOffset()
			rl.maybeFireSelectionChanged()
			return true
		}
	}
	return false
}

// SelectByArchiveGroup moves the cursor to the Archive sub-group header for
// the given group name (BUG-026). Returns true on success.
func (rl *railList) SelectByArchiveGroup(group string) bool {
	for i, r := range rl.rows {
		if r.kind == railRowArchiveGroup && r.archiveGroup == group {
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

// StepToBindable moves the cursor to the next (dir>0) or previous (dir<0)
// PANE-BINDABLE row, skipping non-bindable selectable rows (Freelance repo
// headers, Archive expandos) as well as separators. A pane-bindable row is a
// coordinator header (orchEntry) or a role row (worker / sub-coordinator /
// freelancer) with a usable task target. Returns true when the cursor moved.
// Fires the selection-changed callback on a successful move.
//
// Drives the ⌘↑/⌘↓ in-pane navigation (D15): flip through agents without
// returning to the rail. The cursor lands only on rows whose primary pane the
// operator can be dropped into.
func (rl *railList) StepToBindable(dir int) bool {
	if len(rl.rows) == 0 || dir == 0 {
		return false
	}
	c := rl.cursor + sign(dir)
	for c >= 0 && c < len(rl.rows) {
		if rl.bindable(c) {
			rl.cursor = c
			rl.clampOffset()
			rl.maybeFireSelectionChanged()
			return true
		}
		c += sign(dir)
	}
	return false
}

// NavToParent moves the cursor to the nearest ancestor row: the first selectable
// row above the cursor with a strictly shallower depth. No-op when the cursor is
// already at depth 0 (root coordinators, top-level Archive expando, Freelance
// project headers). Fires the selection-changed callback on a successful move.
//
// This drives plain ← (left arrow) in RAIL focus (BUG-005): jump to parent
// without collapsing. Examples:
//   - worker (depth 1) → coordinator header (depth 0)
//   - sub-coordinator role (depth 1) → root coordinator header (depth 0)
//   - worker under sub-coordinator (depth 2) → sub-coordinator row (depth 1)
//   - archived child inside open per-coordinator Archive (depth 2) → Archive expando (depth 1)
//   - per-coordinator Archive expando (depth 1) → coordinator header (depth 0)
//   - Archive sub-group item (depth 2) → sub-group header (depth 1)
//   - Archive sub-group header (depth 1) → top-level Archive expando (depth 0)
func (rl *railList) NavToParent() {
	if rl.cursor < 0 || rl.cursor >= len(rl.rows) {
		return
	}
	curDepth := rl.rows[rl.cursor].depth
	if curDepth == 0 {
		return // already at root; no-op
	}
	for i := rl.cursor - 1; i >= 0; i-- {
		if rl.selectable(i) && rl.rows[i].depth < curDepth {
			rl.cursor = i
			rl.clampOffset()
			rl.maybeFireSelectionChanged()
			return
		}
	}
}

// bindable reports whether row i is a pane-bindable selection: a coordinator
// header or a role row whose ref carries a task target. Freelance repo headers
// and Archive expandos are selectable for folding but are NOT pane-bindable.
func (rl *railList) bindable(i int) bool {
	if i < 0 || i >= len(rl.rows) {
		return false
	}
	switch r := rl.rows[i]; r.kind {
	case railRowOrch:
		return r.orch != nil
	case railRowRole:
		return r.role != nil && r.role.ArgusTaskID != "" && !r.isBreadcrumbContinuation
	case railRowPinnedBreadcrumb:
		// Two-line pinned entry (BUG-025): the breadcrumb row is the cursor
		// target and carries a task target via role.ArgusTaskID.
		return r.role != nil && r.role.ArgusTaskID != ""
	}
	return false
}

func sign(n int) int {
	if n < 0 {
		return -1
	}
	return 1
}

func (rl *railList) selectable(i int) bool {
	if i < 0 || i >= len(rl.rows) {
		return false
	}
	r := rl.rows[i]
	switch r.kind {
	case railRowOrch, railRowFreelanceProj, railRowArchiveExpando, railRowArchiveGroup, railRowPinnedBreadcrumb:
		return true
	case railRowRole:
		// Breadcrumb-continuation rows are non-selectable: the cursor anchors
		// on the preceding railRowPinnedBreadcrumb (BUG-025).
		return !r.isBreadcrumbContinuation
	}
	return false // railRowPinnedSep, railRowPinnedEnd, railRowSectionRule, railRowFreelanceSep, railRowEmpty
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
	case railRowArchiveExpando:
		// Toggle the Archive expando's fold directly and rebuild.
		rl.archiveExpanded[r.archiveOwner] = !rl.archiveExpanded[r.archiveOwner]
		prev := rl.currentRef()
		rl.buildRows()
		rl.restoreCursorToArchiveOwner(r.archiveOwner, prev)
		rl.maybeFireSelectionChanged()
		rl.maybeFireStateChanged()
		return
	case railRowArchiveGroup:
		// Toggle the Archive sub-group's fold (BUG-026) and rebuild.
		group := r.archiveGroup
		rl.archiveGroupExpanded[group] = !rl.archiveGroupExpanded[group]
		if !rl.archiveGroupExpanded[group] {
			delete(rl.archiveGroupExpanded, group) // compact: absent = collapsed
		}
		prev := rl.currentRef()
		rl.buildRows()
		rl.restoreCursorToArchiveGroup(group, prev)
		rl.maybeFireSelectionChanged()
		rl.maybeFireStateChanged()
		return
	case railRowRole:
		// A sub-coordinator row (a worker that is also another orchestrator's
		// coord) folds ITS OWN nested children, keyed on its childOrch. The
		// toggle flips the EFFECTIVE state (orchCollapsed), pinning an explicit
		// choice — a raw map flip would mis-toggle a default-collapsed empty
		// coordinator (zero-value false → "collapse" an already-folded row).
		if r.role != nil && r.role.childOrch != nil {
			child := r.role.childOrch
			rl.collapsed[child.ID] = !rl.orchCollapsed(child)
			prev := rl.currentRef()
			rl.buildRows()
			rl.restoreCursor(prev)
			rl.maybeFireSelectionChanged()
			rl.maybeFireStateChanged()
			return
		}
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
		// Flip the EFFECTIVE state so the first toggle on a default-collapsed
		// empty coordinator expands it; the explicit entry persists thereafter.
		rl.collapsed[orch.ID] = !rl.orchCollapsed(orch)
	case fproj != nil:
		rl.freelanceCollapsed[fproj.Project] = !rl.freelanceCollapsed[fproj.Project]
	default:
		return
	}
	prev := rl.currentRef()
	rl.buildRows()
	rl.restoreCursor(prev)
	rl.maybeFireSelectionChanged()
	rl.maybeFireStateChanged()
}

// restoreCursorToArchiveOwner re-pins the cursor on an Archive expando
// header after its fold toggles. The expando's currentRef is nil, so the
// generic restoreCursor can't find it; we look it up by owner and fall back
// to the generic restore otherwise.
func (rl *railList) restoreCursorToArchiveOwner(owner int64, prev any) {
	for i, r := range rl.rows {
		if r.kind == railRowArchiveExpando && r.archiveOwner == owner {
			rl.cursor = i
			rl.clampOffset()
			return
		}
	}
	rl.restoreCursor(prev)
}

// restoreCursorToArchiveGroup re-pins the cursor on an Archive sub-group header
// (BUG-026) after its fold toggles. Falls back to the generic restore.
func (rl *railList) restoreCursorToArchiveGroup(group string, prev any) {
	for i, r := range rl.rows {
		if r.kind == railRowArchiveGroup && r.archiveGroup == group {
			rl.cursor = i
			rl.clampOffset()
			return
		}
	}
	rl.restoreCursor(prev)
}

func (rl *railList) restoreCursor(prev any) bool {
	switch ref := prev.(type) {
	case *roleEntry:
		if ref == nil {
			break
		}
		// Freelance rows carry RoleID==0 (managed roles always have RoleID>0),
		// so prefer their stable ArgusTaskID to avoid matching the FIRST
		// freelancer on rebuild. Managed rows still restore by RoleID below.
		if ref.RoleID == 0 && rl.SelectByArgusTaskID(ref.ArgusTaskID) {
			return true
		}
		if rl.SelectByRoleID(ref.RoleID) {
			return true
		}
		if rl.SelectByOrchID(ref.OrchestratorID) {
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

// roleArchived reports whether a role belongs in an Archive expando rather
// than the coordinator's active list: hera-archived, argus-archived, or dead
// (the argus task RECORD no longer exists). These three — and ONLY these —
// bucket a row; task STATUS never does (a completed row stays in the active
// list with its ✓). This is the partition the prototype draws between active
// children and the per-coordinator Archive fold.
func roleArchived(r *roleEntry) bool {
	return r.Archived || r.ArgusArchived || r.Dead
}

// rolePinnedOut reports whether role r floats OUT of orchestrator o into the
// top Pinned section: r is pinned and its coordinator o is NOT itself pinned
// (a child under a pinned coordinator stays nested, never double-renders).
// Sub-coordinators (childOrch != nil) float to the Pinned section along with
// their own children; they render as foldable coord rows at depth 0 inside the
// Pinned block (BUG-021). Full ancestry (showing the parent chain A→B→C) is a
// separate, deferred design question; the Pinned block shows the sub-coord and
// its own subtree without the parent chain.
func rolePinnedOut(o *orchEntry, r *roleEntry) bool {
	return r.Pinned && (o == nil || !o.Pinned)
}

// collectPinnedRoles walks the full orchestrator tree (including sub-
// coordinators via childOrch) and returns every pinned role that floats into
// the Pinned section (rolePinnedOut), paired with its computed ancestry trail
// (BUG-025). Walking the whole tree keeps a pinned role nested under a sub-
// coordinator from vanishing: appendOrchChildren skips it (rolePinnedOut), so
// it must be collected here to render standalone. When a filter is active,
// only roles that pass roleVisible are collected. When a pinned sub-coordinator
// floats OUT, its subtree renders under it in the Pinned block — so we do NOT
// also walk into its childOrch (double-collect prevention). seen guards cycles
// in a malformed multi-binding chain.
//
// ancestorPath carries the accumulated ancestry from root to the current
// orchestrator o, inclusive of o.Name, e.g. "root › " or "root › sub › ".
// This becomes the breadcrumb text for roles inside o (BUG-025).
func (rl *railList) collectPinnedRoles() []pinnedRole {
	var out []pinnedRole
	seen := map[int64]bool{}
	fActive := rl.filterActive()
	var walk func(o *orchEntry, ancestorPath string)
	walk = func(o *orchEntry, ancestorPath string) {
		if o == nil || seen[o.ID] {
			return
		}
		seen[o.ID] = true
		for _, r := range o.Roles {
			if rolePinnedOut(o, r) {
				// BUG-022: apply filter — skip non-matching pinned roles.
				if fActive && !rl.roleVisible(nil, r) {
					continue
				}
				out = append(out, pinnedRole{role: r, ancestry: ancestorPath})
				// Pinned sub-coordinator: its children render under it inside
				// the Pinned block, so skip walking into childOrch here.
			} else if r.childOrch != nil {
				// Recurse with the accumulated path extended to include this
				// sub-coordinator's orch name, so its children's ancestry is
				// "ancestorPath + childOrch.Name + ' › '".
				walk(r.childOrch, ancestorPath+r.childOrch.Name+" › ")
			}
		}
	}
	for _, o := range rl.orchestrators {
		walk(o, o.Name+" › ")
	}
	return out
}

// archiveOpen reports whether the Archive expando for owner is folded open.
// The `l` listall convenience (showArchived) force-expands every expando;
// otherwise the per-owner toggle decides (default collapsed, D14).
func (rl *railList) archiveOpen(owner int64) bool {
	// An active filter force-expands archive expandos so matching archived
	// rows are reachable without folding.
	return rl.filterActive() || rl.showArchived || rl.archiveExpanded[owner]
}

// archiveGroupOpen reports whether a sub-group header inside the consolidated
// bottom Archive (BUG-026) is folded open. Default is collapsed (zero value);
// the operator toggles each group with Space/Enter. An active filter or
// showArchived (`l`) force-expands sub-groups so matching items are reachable.
func (rl *railList) archiveGroupOpen(group string) bool {
	return rl.filterActive() || rl.showArchived || rl.archiveGroupExpanded[group]
}

// orchCollapsed resolves a coordinator's EFFECTIVE fold state. An explicit
// operator toggle (an entry in collapsed) always wins; otherwise the default
// applies: a coordinator with zero non-archived children collapses so dead and
// finished orchestrators fold away instead of burning rail rows, while a
// coordinator with at least one active child stays expanded. showArchived
// (`l`) overrides the collapsed DEFAULT — an untouched empty coordinator
// expands so its archived children are reachable — but never an explicit
// collapse. Applies to root coordinators and nested sub-coordinators alike;
// all fold reads route through here rather than the collapsed map.
func (rl *railList) orchCollapsed(o *orchEntry) bool {
	// An active filter force-expands every coordinator (over an explicit
	// collapse too) so matching rows are never hidden behind a fold.
	if rl.filterActive() {
		return false
	}
	if v, ok := rl.collapsed[o.ID]; ok {
		return v
	}
	if rl.showArchived {
		return false
	}
	return rl.visibleRoleCount(o) == 0
}

func (rl *railList) buildRows() {
	rl.rows = nil
	// An active filter pre-drops coordinators with no match anywhere in their
	// subtree (orchMatches), so a coordinator only reaches the tree when it or
	// one of its (recursive) roles matches — ancestry-preserving. appendOrch's
	// per-row gating (roleVisible) then narrows the children shown beneath it.
	//
	// Partition the survivors: pinned float to the top Pinned section, archived
	// to the bottom Archive section, the rest into the active tree. Pinned wins
	// over archived (mutual exclusivity — argus's SetPinned forces Archived
	// false — but partition defensively in case of a stale mixed row).
	fActive := rl.filterActive()
	var pinnedOrchs, active, archived []*orchEntry
	for _, o := range rl.orchestrators {
		if fActive && !rl.orchMatches(o) {
			continue
		}
		switch {
		case o.Pinned:
			pinnedOrchs = append(pinnedOrchs, o)
		case o.Archived:
			archived = append(archived, o)
		default:
			active = append(active, o)
		}
	}
	// Pinned roles float OUT of their coordinator into the Pinned section.
	// Pinned sub-coordinators float with their own subtrees (BUG-021). A pinned
	// child of a pinned coordinator stays nested (never double-renders) — see
	// rolePinnedOut. Collected by a full-tree walk so a pinned role nested under
	// a sub-coordinator (skipped by appendOrchChildren) still surfaces.
	// Active filters are applied inside collectPinnedRoles (BUG-022).
	pinnedRoles := rl.collectPinnedRoles()

	// Pinned freelancers (BUG-024): freelancers pinned via the in-memory
	// pinnedFreelance map. They have no coordinator, so they render at ROOT
	// level in the Pinned block, intermixed with pinned coords. Pinned
	// freelancers are skipped in the Freelance section (no double-render).
	var pinnedFree []*roleEntry
	for _, fp := range rl.freelance {
		for _, t := range fp.Tasks {
			if !rl.pinnedFreelance[t.ArgusTaskID] {
				continue
			}
			if fActive && !rl.matchesFilterFields(t.Name, fp.Project) {
				continue
			}
			pinnedFree = append(pinnedFree, t)
		}
	}

	// appendOrch renders a coordinator (orchestrator root or, recursively, a
	// sub-coordinator) at the given depth and, when expanded, its active
	// children folders-first followed by a per-coordinator Archive (N) expando
	// holding its archived children (collapsed by default, D14). Children that
	// are themselves coordinators (a roleEntry carrying childOrch — a
	// multi-binding) recurse one level deeper, so the rail is a true tree.
	//
	// seen guards against cycles (a malformed multi-binding chain): an
	// orchestrator already on the current ancestry path is not re-expanded.
	appendOrch := func(o *orchEntry, depth int) {
		rl.rows = append(rl.rows, railRow{kind: railRowOrch, orch: o, depth: depth})
		if rl.orchCollapsed(o) {
			return
		}
		appendOrchChildren(rl, o, depth+1, map[int64]bool{o.ID: true})
	}

	// Pinned section at the very TOP of the rail (above the orchestrator list),
	// mirroring argus. The separator only appears when at least one pinned
	// coordinator, agent, or freelancer exists, so the operator never lands on
	// an empty section. Pinned coordinators carry their subtrees; pinned roles
	// render at depth 0 (pinned sub-coordinators also expand their own children
	// here). Pinned freelancers render at root level (depth 0) intermixed with
	// pinned coords — they have no coordinator ancestry.
	// A closing rule (railRowPinnedEnd) delineates the Pinned block from the
	// active tree below (BUG-021).
	if len(pinnedOrchs) > 0 || len(pinnedRoles) > 0 || len(pinnedFree) > 0 {
		rl.rows = append(rl.rows, railRow{kind: railRowPinnedSep})
		for _, o := range pinnedOrchs {
			appendOrch(o, 0)
		}
		// BUG-025: pinned managed roles render as a two-line breadcrumb entry.
		// Line 1 (railRowPinnedBreadcrumb, selectable): dimmed status icon +
		// ancestry trail. Line 2 (railRowRole continuation, non-selectable):
		// full-bright name + right-aligned age. A pinned sub-coordinator also
		// expands its own children one level deeper (BUG-021).
		for _, pr := range pinnedRoles {
			r := pr.role
			rl.rows = append(rl.rows, railRow{
				kind:       railRowPinnedBreadcrumb,
				role:       r,
				depth:      0,
				breadcrumb: pr.ancestry,
			})
			rl.rows = append(rl.rows, railRow{
				kind:                     railRowRole,
				role:                     r,
				depth:                    1,
				isBreadcrumbContinuation: true,
			})
			// A pinned sub-coordinator floats WITH its own children (BUG-021).
			// The children are NOT rendered elsewhere (appendOrchChildren skips
			// rolePinnedOut roles), so expand them here when not collapsed.
			if r.childOrch != nil && !rl.orchCollapsed(r.childOrch) {
				appendOrchChildren(rl, r.childOrch, 2, map[int64]bool{r.childOrch.ID: true})
			}
		}
		// BUG-024: pinned freelancers render at root level alongside pinned
		// coords. No subtree to expand (freelancers are leaf nodes).
		for _, r := range pinnedFree {
			rl.rows = append(rl.rows, railRow{kind: railRowRole, role: r, depth: 0})
		}
		// Closing separator rule — mirrors argus's Pinned delineation (BUG-021).
		rl.rows = append(rl.rows, railRow{kind: railRowPinnedEnd})
	}

	hadActive := len(active) > 0
	for _, o := range active {
		appendOrch(o, 0)
	}

	// Group archived freelancers by project (stable input order) for the
	// consolidated bottom Archive. Each project name in archivedFreeProjects
	// appears exactly once, in first-seen order from archivedFreelance.
	archivedFreeByProject := map[string][]*roleEntry{}
	var archivedFreeProjects []string
	for _, t := range rl.archivedFreelance {
		if _, exists := archivedFreeByProject[t.Project]; !exists {
			archivedFreeProjects = append(archivedFreeProjects, t.Project)
		}
		archivedFreeByProject[t.Project] = append(archivedFreeByProject[t.Project], t)
	}

	// Freelance section (BUG-026): ONLY live (non-archived) freelancers grouped
	// by project. No per-project Archive expandos here — archived freelancers
	// live exclusively in the consolidated bottom Archive. A railRowSectionRule
	// precedes the section when live coordinator rows were emitted above.
	{
		freelanceSepEmitted := false
		emitFreelanceSep := func() {
			if !freelanceSepEmitted {
				if hadActive {
					rl.rows = append(rl.rows, railRow{kind: railRowSectionRule})
				}
				rl.rows = append(rl.rows, railRow{kind: railRowFreelanceSep})
				freelanceSepEmitted = true
			}
		}

		if fActive {
			// Filtered freelance: match live tasks by name OR project (argus
			// parity). Auto-expand (collapse ignored). Archived freelancers are
			// not shown in this section; they appear in the bottom Archive's
			// filtered sub-groups instead.
			for _, fp := range rl.freelance {
				var live []*roleEntry
				for _, t := range fp.Tasks {
					if roleArchived(t) || rl.pinnedFreelance[t.ArgusTaskID] {
						continue
					}
					if rl.matchesFilterFields(t.Name, fp.Project) {
						live = append(live, t)
					}
				}
				if len(live) == 0 {
					continue
				}
				emitFreelanceSep()
				rl.rows = append(rl.rows, railRow{kind: railRowFreelanceProj, fproj: fp})
				for _, t := range live {
					rl.rows = append(rl.rows, railRow{kind: railRowRole, role: t})
				}
			}
		} else {
			for _, fp := range rl.freelance {
				// Collect non-archived, non-pinned tasks for this project.
				var live []*roleEntry
				for _, t := range fp.Tasks {
					if roleArchived(t) || rl.pinnedFreelance[t.ArgusTaskID] {
						continue
					}
					live = append(live, t)
				}
				if len(live) == 0 {
					continue // no live tasks: skip this project in the Freelance section
				}
				emitFreelanceSep()
				rl.rows = append(rl.rows, railRow{kind: railRowFreelanceProj, fproj: fp})
				if !rl.freelanceCollapsed[fp.Project] {
					for _, t := range live {
						rl.rows = append(rl.rows, railRow{kind: railRowRole, role: t})
					}
				}
			}
		}

		// Consolidated bottom Archive section (BUG-026): ONE archive below the
		// Freelance section holding:
		//   • "Hera sessions" sub-group — archived root coordinators
		//   • per-project sub-groups    — archived freelancers (carry (F) marker)
		// A railRowSectionRule precedes the Archive when any content was emitted
		// above (active coords or Freelance section). Sub-groups default collapsed;
		// the operator expands each independently (fold state persisted).
		totalArchived := len(archived) + len(rl.archivedFreelance)
		if totalArchived > 0 || (fActive && (len(archived) > 0 || len(rl.archivedFreelance) > 0)) {
			// Compute filtered totals when a filter is active so the count
			// shown on the Archive header reflects the visible subset.
			archiveCount := 0
			filteredArchived := archived
			filteredFreeByProject := archivedFreeByProject
			filteredFreeProjects := archivedFreeProjects
			if fActive {
				// Only count archived coords that match the filter.
				filteredArchived = nil
				for _, o := range archived {
					if rl.orchMatches(o) {
						filteredArchived = append(filteredArchived, o)
					}
				}
				// Only count archived freelancers that match the filter.
				filteredFreeByProject = map[string][]*roleEntry{}
				filteredFreeProjects = nil
				for _, proj := range archivedFreeProjects {
					var match []*roleEntry
					for _, t := range archivedFreeByProject[proj] {
						if rl.matchesFilterFields(t.Name, proj) {
							match = append(match, t)
						}
					}
					if len(match) > 0 {
						filteredFreeByProject[proj] = match
						filteredFreeProjects = append(filteredFreeProjects, proj)
					}
				}
				archiveCount = len(filteredArchived)
				for _, tasks := range filteredFreeByProject {
					archiveCount += len(tasks)
				}
			} else {
				archiveCount = totalArchived
			}

			if archiveCount > 0 {
				if hadActive || freelanceSepEmitted {
					rl.rows = append(rl.rows, railRow{kind: railRowSectionRule})
				}
				rl.rows = append(rl.rows, railRow{
					kind:         railRowArchiveExpando,
					archiveOwner: archiveTopLevelOwner,
					archiveCount: archiveCount,
				})
				if rl.archiveOpen(archiveTopLevelOwner) {
					// "Hera sessions" sub-group: archived root coordinators.
					if len(filteredArchived) > 0 {
						const heraGroup = "Hera sessions"
						rl.rows = append(rl.rows, railRow{
							kind:         railRowArchiveGroup,
							archiveGroup: heraGroup,
							archiveCount: len(filteredArchived),
							depth:        1,
						})
						if rl.archiveGroupOpen(heraGroup) {
							for _, o := range filteredArchived {
								appendOrch(o, 2)
							}
						}
					}
					// Per-project sub-groups: archived freelancers (carry (F) marker).
					for _, proj := range filteredFreeProjects {
						tasks := filteredFreeByProject[proj]
						rl.rows = append(rl.rows, railRow{
							kind:         railRowArchiveGroup,
							archiveGroup: proj,
							archiveCount: len(tasks),
							depth:        1,
						})
						if rl.archiveGroupOpen(proj) {
							for _, t := range tasks {
								rl.rows = append(rl.rows, railRow{kind: railRowRole, role: t, depth: 2})
							}
						}
					}
				}
			}
		}
	}
	if len(rl.rows) == 0 {
		rl.rows = append(rl.rows, railRow{kind: railRowEmpty})
	}

	// Recompute the running flag for the spinner driver: any visible row
	// rendering the animated spinner keeps the 150ms repaint ticking.
	running := false
	for _, r := range rl.rows {
		if rowRunning(r) {
			running = true
			break
		}
	}
	rl.hasRunning.Store(running)
}

// appendOrchChildren emits a coordinator's children at childDepth: its active
// children folders-first (a sub-coordinator child recurses one level deeper via
// recurse), then a per-coordinator Archive (N) expando (collapsed by default,
// D14) whose archived children sit one level DEEPER than the expando header.
// seen carries the current ancestry path so a cyclic multi-binding chain never
// loops; the expando header is keyed by the owning orchestrator's ID.
func appendOrchChildren(rl *railList, o *orchEntry, childDepth int, seen map[int64]bool) {
	var activeRoles, archivedRoles []*roleEntry
	for _, role := range o.Roles {
		// Skip a child here when EITHER: an active filter narrows it out
		// (ancestry-preserving via roleVisible — coordinator name matches shows
		// the whole team, role name matches, or a sub-coordinator with a deeper
		// match), OR it's a pinned agent under a non-pinned coordinator that
		// floated to the Pinned section (skip so it doesn't double-render).
		if !rl.roleVisible(o, role) || rolePinnedOut(o, role) {
			continue
		}
		if roleArchived(role) {
			archivedRoles = append(archivedRoles, role)
		} else {
			activeRoles = append(activeRoles, role)
		}
	}
	for _, role := range sortFoldersFirst(activeRoles) {
		rl.rows = append(rl.rows, railRow{kind: railRowRole, role: role, depth: childDepth})
		// A sub-coordinator (multi-binding) is a foldable coord row: nest its
		// child orchestrator's roles one level deeper, recursively, unless it
		// is collapsed or would form a cycle (its child already on the ancestry
		// path). The role row itself is drawn coord-style by drawRoleRow.
		if role.childOrch != nil && !seen[role.childOrch.ID] && !rl.orchCollapsed(role.childOrch) {
			seen[role.childOrch.ID] = true
			appendOrchChildren(rl, role.childOrch, childDepth+1, seen)
			delete(seen, role.childOrch.ID)
		}
	}
	// Per-coordinator Archive expando: present whenever this coordinator has
	// archived direct children, always reachable, collapsed by default. The
	// expando header sits at the children's depth; its archived children indent
	// one level deeper.
	if len(archivedRoles) > 0 {
		rl.rows = append(rl.rows, railRow{
			kind:         railRowArchiveExpando,
			archiveOwner: o.ID,
			archiveCount: len(archivedRoles),
			depth:        childDepth,
		})
		if rl.archiveOpen(o.ID) {
			for _, role := range sortFoldersFirst(archivedRoles) {
				rl.rows = append(rl.rows, railRow{kind: railRowRole, role: role, depth: childDepth + 1})
			}
		}
	}
}

// sortFoldersFirst returns roles ordered with sub-coordinators (RoleKind ==
// coordinator) before leaf workers, preserving each group's input order
// (stable). Mirrors the prototype's `ordered()` folders-first sort.
func sortFoldersFirst(roles []*roleEntry) []*roleEntry {
	if len(roles) < 2 {
		return roles
	}
	out := make([]*roleEntry, 0, len(roles))
	for _, r := range roles {
		if r.RoleKind == string(db.KindCoordinator) {
			out = append(out, r)
		}
	}
	for _, r := range roles {
		if r.RoleKind != string(db.KindCoordinator) {
			out = append(out, r)
		}
	}
	return out
}

// InputHandler routes KeyUp/KeyDown to the cursor and the spacebar to
// the collapse toggle. Every other key is left to the KeyRouter (Enter,
// j/k, rail mutation keys, focus traversal). The router translates j/k
// to KeyDown/KeyUp before they reach this widget.
//
// `/` enters search input mode (the key is unbound in the router, so it
// propagates here). While in input mode the router yields every key to this
// handler (via the RailFilter gate), so they route to HandleFilterKey as filter
// input rather than firing mutations.
func (rl *railList) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return rl.WrapInputHandler(func(e *tcell.EventKey, _ func(tview.Primitive)) {
		if rl.filtering {
			rl.HandleFilterKey(e)
			return
		}
		switch e.Key() {
		case tcell.KeyDown:
			rl.CursorDown()
		case tcell.KeyUp:
			rl.CursorUp()
		case tcell.KeyLeft:
			rl.NavToParent()
		case tcell.KeyRune:
			switch e.Rune() {
			case ' ':
				rl.ToggleCollapse()
			case '/':
				rl.BeginFilter()
			}
		}
	})
}

// Draw renders the rail's box, then walks visible rows.
func (rl *railList) Draw(screen tcell.Screen) {
	rl.DrawForSubclass(screen, rl)
	x, y, w, h := rl.GetInnerRect()
	if w <= 0 || h <= 0 {
		return
	}
	if len(rl.rows) == 0 {
		return
	}

	// While in search input mode the bottom inner row is reserved for the
	// `/ <query>` filter input line, so the row viewport is one shorter. The
	// gutter math and snapping use this effective height so no row hides behind
	// the input line.
	listH := h
	if rl.filtering {
		listH = h - 1
		if listH < 0 {
			listH = 0
		}
	}

	rl.clampOffset()
	// Cursor-follow snap fires only when the cursor MOVED since the last
	// draw — an unchanged cursor means any viewport displacement came from a
	// wheel pan (PanBy), which must persist across refresh repaints.
	if rl.cursor != rl.lastSnapCursor {
		if rl.cursor < rl.offset {
			rl.offset = rl.cursor
		}
		// For a two-line pinned entry (BUG-025), the cursor is on the
		// railRowPinnedBreadcrumb (line 1); also ensure line 2 (continuation)
		// is visible when snapping down.
		snapLow := rl.cursor
		if rl.cursor >= 0 && rl.cursor < len(rl.rows) &&
			rl.rows[rl.cursor].kind == railRowPinnedBreadcrumb {
			snapLow = rl.cursor + 1
		}
		if snapLow >= rl.offset+listH {
			rl.offset = snapLow - listH + 1
		}
	}
	rl.lastSnapCursor = rl.cursor
	rl.lastHeight = listH
	if rl.offset < 0 {
		rl.offset = 0
	}
	if max := len(rl.rows) - listH; rl.offset > max {
		if max < 0 {
			max = 0
		}
		rl.offset = max
	}

	// Row content renders right of the selection-marker gutter; the gutter
	// itself is painted per row below.
	cx, cw := x+markerGutter, w-markerGutter
	if cw < 0 {
		cw = 0
	}
	for i := 0; i < listH; i++ {
		idx := rl.offset + i
		if idx >= len(rl.rows) {
			break
		}
		row := rl.rows[idx]
		cursor := idx == rl.cursor && rl.selectable(idx)
		// Selection-marker gutter: the `›` glyph renders on the cursor row ONLY
		// when the probe gate is set (the operator does not want it in normal
		// use; the live-probe harness relies on it). The gutter is reserved in
		// both states so nothing shifts when the marker toggles or the cursor
		// moves; selection is otherwise shown by theme.StyleSelected text.
		marker := ' '
		if cursor && rl.probeMarker {
			marker = selectionMarker
		}
		screen.SetContent(x, y+i, marker, nil, theme.StyleSelected)
		switch row.kind {
		case railRowOrch:
			rl.drawOrchRow(screen, cx, y+i, cw, row.orch, row.depth, cursor)
		case railRowRole:
			if row.isBreadcrumbContinuation {
				// Line 2 of a two-line pinned entry (BUG-025): bright name +
				// age only. The cursor is on the preceding breadcrumb row; render
				// name in StyleSelected when that breadcrumb row is selected.
				prevSelected := idx > 0 && rl.rows[idx-1].kind == railRowPinnedBreadcrumb &&
					idx-1 == rl.cursor
				rl.drawBreadcrumbNameRow(screen, cx, y+i, cw, row.role, row.depth, prevSelected)
			} else {
				rl.drawRoleRow(screen, cx, y+i, cw, row.role, row.depth, cursor)
			}
		case railRowPinnedBreadcrumb:
			// Line 1 of a two-line pinned entry (BUG-025): dimmed status icon +
			// ancestry trail. The selection marker is already painted above.
			rl.drawPinnedBreadcrumbRow(screen, cx, y+i, cw, row.role, row.breadcrumb)
		case railRowPinnedSep:
			rl.drawSeparator(screen, x, y+i, w, " Pinned ")
		case railRowPinnedEnd:
			rl.drawSeparator(screen, x, y+i, w, "")
		case railRowSectionRule:
			rl.drawSeparator(screen, x, y+i, w, "")
		case railRowArchiveGroup:
			rl.drawArchiveGroupRow(screen, cx, y+i, cw, row.archiveGroup, row.archiveCount, row.depth, cursor)
		case railRowFreelanceSep:
			rl.drawSeparator(screen, x, y+i, w, " Freelance ")
		case railRowFreelanceProj:
			rl.drawFreelanceProjRow(screen, cx, y+i, cw, row.fproj, cursor)
		case railRowArchiveExpando:
			rl.drawArchiveExpando(screen, cx, y+i, cw, row.archiveOwner, row.archiveCount, row.depth, cursor)
		case railRowEmpty:
			widget.DrawText(screen, cx, y+i, cw, "(no projects)", theme.StyleDimmed)
		}
	}

	// Filter input line on the reserved bottom row (input mode only): a
	// `/`-prefixed prompt with the live query, mirroring argus's filter bar.
	if rl.filtering && listH < h {
		rl.drawFilterInput(screen, x, y+listH, w)
	}
}

// drawFilterInput renders the `/ <query>` search prompt on the rail's reserved
// bottom row while in input mode, so the operator always sees the live query.
func (rl *railList) drawFilterInput(screen tcell.Screen, x, y, w int) {
	style := tcell.StyleDefault.Foreground(theme.ColorTitle)
	widget.DrawText(screen, x, y, 2, "/ ", style)
	widget.DrawText(screen, x+2, y, w-2, rl.filter, theme.StyleNormal)
}

// PanBy moves the rail viewport by delta rows (positive reveals later rows,
// negative earlier ones) WITHOUT moving the cursor — wheel panning must never
// change the selection, because selection changes rebind the panes. The
// offset clamps to [0, max(0, rows−height)] using the height of the last
// draw; when the content fits the viewport the call is a no-op. Driven by
// App.applyWheel on the tview event loop.
func (rl *railList) PanBy(delta int) {
	rl.offset += delta
	max := len(rl.rows) - rl.lastHeight
	if rl.lastHeight <= 0 {
		// Never drawn: clamp loosely to the row count; the first Draw
		// re-clamps against the real height.
		max = len(rl.rows) - 1
	}
	if max < 0 {
		max = 0
	}
	if rl.offset > max {
		rl.offset = max
	}
	if rl.offset < 0 {
		rl.offset = 0
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

func (rl *railList) drawOrchRow(screen tcell.Screen, x, y, w int, o *orchEntry, depth int, cursor bool) {
	if o == nil {
		return
	}

	col := x + depth*indentStep
	// Status icon first (argus task-panel order: <icon> <chevron> <name>), so a
	// coordinator header reads with the same ☾/○/✓/? vocabulary as its workers.
	icon, iconStyle := rl.orchIcon(o)
	screen.SetContent(col, y, icon, nil, iconStyle)
	col += 2

	chevron := '▾'
	if rl.orchCollapsed(o) {
		chevron = '▸'
	}
	screen.SetContent(col, y, chevron, nil, tcell.StyleDefault.Foreground(theme.ColorDimmed))
	col += 2

	// Coordinator marker (󰹻), drawn between the chevron and the name to flag the
	// row as a coordinator independent of its transient status icon.
	markerStyle := tcell.StyleDefault.Foreground(theme.ColorProject)
	if o.Archived {
		markerStyle = theme.StyleDimmed
	}
	screen.SetContent(col, y, iconCoord, nil, markerStyle)
	col += 2

	nameStyle := tcell.StyleDefault.Foreground(theme.ColorProject).Bold(true)
	if o.Archived {
		nameStyle = tcell.StyleDefault.Foreground(theme.ColorDimmed).Bold(true)
	}
	if cursor {
		nameStyle = theme.StyleSelected
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

// visibleRoleCount is the live-child (N) count shown on a coordinator
// header: only its active (non-archived) children. Archived children live in
// the per-coordinator Archive expando and never inflate this count, even with
// `l` listall on (D14) — the count tracks the active list the chevron folds.
func (rl *railList) visibleRoleCount(o *orchEntry) int {
	if o == nil {
		return 0
	}
	n := 0
	for _, r := range o.Roles {
		if roleArchived(r) {
			continue
		}
		// A pinned agent floated to the Pinned section (coordinator not pinned)
		// no longer renders under this header, so it must not inflate the count.
		if rolePinnedOut(o, r) {
			continue
		}
		n++
	}
	return n
}

func (rl *railList) drawRoleRow(screen tcell.Screen, x, y, w int, r *roleEntry, depth int, cursor bool) {
	if r == nil {
		return
	}
	// A sub-coordinator (a worker role that is also another orchestrator's
	// coord) renders as a foldable coordinator row — chevron + 󰹻 marker + count
	// + status icon — just like a root coordinator header, only nested.
	if r.childOrch != nil {
		rl.drawSubCoordRow(screen, x, y, w, r, depth, cursor)
		return
	}

	// Layout: "<icon> [PR] name           10m"
	col := x + depth*indentStep

	icon, iconStyle := rl.roleIcon(r)
	screen.SetContent(col, y, icon, nil, iconStyle)
	col += 2 // icon + space

	// Freelance marker (BUG-024): drawn for every freelance row so freelancers
	// are visually distinct from managed agents in both the Freelance section
	// and the Pinned block. The marker occupies the same slot as the PR
	// indicator — PR state is never set on unmanaged tasks (no hera binding),
	// so the two are mutually exclusive in practice.
	// White (high-contrast) so the glyph is clearly readable; the previous
	// dim variant was too faint against the dark background (BUG-031).
	if r.RoleKind == string(db.KindFreelance) {
		markerStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite)
		screen.SetContent(col, y, iconFreelance, nil, markerStyle)
		col += 2
	} else if prIcon, prStyle, ok := prGlyph(r.PRState); ok {
		// PR review indicator: only consumes width when actionable; the name
		// reclaims the space otherwise (mirrors argus's task-row PR cell).
		screen.SetContent(col, y, prIcon, nil, prStyle)
		col += 2 // PR glyph + space
	}

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

// drawSubCoordRow renders a sub-coordinator (a worker role that is also another
// orchestrator's coord) as a NESTED foldable coordinator row, mirroring
// drawOrchRow's layout — status icon, chevron (keyed on childOrch.ID), 󰹻 coord
// marker, name, live-child (N) count — at the given depth. The status icon
// comes from the role's own argus state (roleIcon), so the row reads with the
// same ☾/○/✓/? vocabulary as any coordinator.
func (rl *railList) drawSubCoordRow(screen tcell.Screen, x, y, w int, r *roleEntry, depth int, cursor bool) {
	col := x + depth*indentStep

	icon, iconStyle := rl.roleIcon(r)
	screen.SetContent(col, y, icon, nil, iconStyle)
	col += 2

	chevron := '▾'
	if rl.orchCollapsed(r.childOrch) {
		chevron = '▸'
	}
	screen.SetContent(col, y, chevron, nil, tcell.StyleDefault.Foreground(theme.ColorDimmed))
	col += 2

	markerStyle := tcell.StyleDefault.Foreground(theme.ColorProject)
	if r.Archived || r.Dead || r.ArgusArchived {
		markerStyle = theme.StyleDimmed
	}
	screen.SetContent(col, y, iconCoord, nil, markerStyle)
	col += 2

	nameStyle := tcell.StyleDefault.Foreground(theme.ColorProject).Bold(true)
	if r.Archived || r.Dead || r.ArgusArchived {
		nameStyle = tcell.StyleDefault.Foreground(theme.ColorDimmed).Bold(true)
	}
	if cursor {
		nameStyle = theme.StyleSelected
	}
	name := r.Name
	count := fmt.Sprintf(" (%d)", rl.visibleRoleCount(r.childOrch))
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

// drawFreelanceProjRow renders a Freelance repo-group header: a collapse
// chevron, the project name, and the right-aligned live count. Mirrors the
// orchestrator header so the operator reads the rail uniformly.
func (rl *railList) drawFreelanceProjRow(screen tcell.Screen, x, y, w int, fp *freelanceProject, cursor bool) {
	if fp == nil {
		return
	}
	chevron := '▾'
	if rl.freelanceCollapsed[fp.Project] {
		chevron = '▸'
	}
	col := x
	screen.SetContent(col, y, chevron, nil, tcell.StyleDefault.Foreground(theme.ColorDimmed))
	col += 2

	nameStyle := tcell.StyleDefault.Foreground(theme.ColorProject).Bold(true)
	if cursor {
		nameStyle = theme.StyleSelected
	}
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

// drawArchiveExpando renders an "Archive (N)" fold header: a chevron
// (▾ open / ▸ collapsed) + the label + a trailing rule, indented by its tree
// depth — a per-coordinator expando sits one level under its coordinator (a
// nested sub-coord's deeper still); the top-level Archive sits flush left. The
// expando is selectable so space/Enter toggles its fold.
func (rl *railList) drawArchiveExpando(screen tcell.Screen, x, y, w int, owner int64, count, depth int, cursor bool) {
	style := tcell.StyleDefault.Foreground(theme.ColorDimmed)
	labelStyle := style.Bold(true)
	if cursor {
		labelStyle = theme.StyleSelected
	}
	// Indent by tree depth so a per-coordinator Archive expando sits one level
	// under its coordinator (and a nested sub-coord's deeper still), matching
	// the prototype's nesting. The top-level Archive (depth 0) sits flush left.
	col := x + depth*indentStep
	chevron := '▸'
	if rl.archiveOpen(owner) {
		chevron = '▾'
	}
	screen.SetContent(col, y, chevron, nil, style)
	col += 2
	label := fmt.Sprintf("Archive (%d)", count)
	widget.DrawText(screen, col, y, w-(col-x), label, labelStyle)
	col += runeLen(label)
	// Trailing rule to mirror the separator rows.
	for col < x+w {
		screen.SetContent(col, y, '─', nil, style)
		col++
	}
}

// drawArchiveGroupRow renders a sub-group header inside the consolidated bottom
// Archive (BUG-026): chevron (\u25BE open / \u25B8 collapsed) + group name + count.
// Mirrors drawFreelanceProjRow's layout at the given depth so the operator
// reads the Archive sub-groups with the same vocabulary as Freelance project
// headers.
func (rl *railList) drawArchiveGroupRow(screen tcell.Screen, x, y, w int, group string, count, depth int, cursor bool) {
	style := tcell.StyleDefault.Foreground(theme.ColorDimmed)
	nameStyle := tcell.StyleDefault.Foreground(theme.ColorProject).Bold(true)
	if cursor {
		nameStyle = theme.StyleSelected
	}
	col := x + depth*indentStep
	chevron := '\u25B8'
	if rl.archiveGroupOpen(group) {
		chevron = '\u25BE'
	}
	screen.SetContent(col, y, chevron, nil, style)
	col += 2
	label := fmt.Sprintf("%s (%d)", group, count)
	widget.DrawText(screen, col, y, w-(col-x), label, nameStyle)
}

// spinnerFrames are argus's Nerd Font progress-spinner frames
// (U+EE06..U+EE0B, argus internal/spinner StyleProgress), rendered for an
// actively running in_progress task. The SDK does not export argus's spinner
// package, so the frames are mirrored here verbatim.
var spinnerFrames = []rune{'\uEE06', '\uEE07', '\uEE08', '\uEE09', '\uEE0A', '\uEE0B'}

// spinnerInterval is argus's progress-spinner cadence (150 ms/frame). Hera
// animates by wall clock — the frame derives from the current time — so every
// repaint shows the frame argus itself would show at that instant.
const spinnerInterval = 150 * time.Millisecond

// animFrame is the wall-clock spinner frame index, derived from the rail's
// (test-overridable) now source so draw tests are deterministic.
func (rl *railList) animFrame() int {
	now := time.Now
	if rl.now != nil {
		now = rl.now
	}
	return int(now().UnixMilli() / spinnerInterval.Milliseconds())
}

// statusIcon picks the status icon from argus-reported task state, shared by
// worker rows (roleIcon) and coordinator headers (orchIcon) so both mirror
// argus's task-panel glyph table identically. The GLYPH always reflects the
// task's actual argus state when that state is known — needs-input, complete,
// in-review, in-progress idle/running, pending — REGARDLESS of the row's
// archived/dead bucketing, which modulates only the STYLE (dimmed). An icon
// that mutated on archive read as "archiving killed/reset my agent" (live QA
// R1/R2); the glyph must never lie about argus reality. The dimmed circle is
// the fallback ONLY for an archived/dead row with UNKNOWN state; active rows
// without state fall back on binding presence.
func statusIcon(archived, hasState, needsInput bool, status string, idle, live bool, frame int) (rune, tcell.Style) {
	if hasState {
		if glyph, style, ok := stateGlyph(needsInput, status, idle, frame); ok {
			if archived {
				return glyph, theme.StyleDimmed
			}
			return glyph, style
		}
	}
	if archived {
		return '○', theme.StyleDimmed
	}
	if live {
		return theme.IconMoonStars, theme.StyleInReview
	}
	return theme.IconMoonOutline, tcell.StyleDefault.Foreground(theme.ColorInReview)
}

// stateGlyph maps a KNOWN argus task state to its status glyph and active
// style, mirroring argus's task-panel table EXACTLY (argus theme.go:29-34 +
// tasklist.go:1095-1132): pending → ○ gray; complete → ✓ green; in_review →
// 󰖔 moon-with-stars blue (NEVER a check — only complete renders ✓);
// in_progress → needs-input ? (#faa378) outranking idle moon (blue)
// outranking the running spinner (orange, animated by frame). needs-input is
// scoped to in_progress, mirroring argus's switch nesting (the API only
// serves needs_input for in_progress). Argus's TUI-only idleUnvisited
// variant (moon-stars for unvisited-idle) is not in the API and is not
// mirrored. ok is false for an unrecognized status so statusIcon can fall
// back to the binding-presence icons instead of guessing.
func stateGlyph(needsInput bool, status string, idle bool, frame int) (rune, tcell.Style, bool) {
	switch status {
	case "pending":
		return '○', theme.StylePending, true
	case "complete":
		return '✓', theme.StyleComplete, true
	case "in_review":
		return theme.IconReview, theme.StyleInReview, true
	case "in_progress":
		switch {
		case needsInput:
			return theme.IconNeedsInput, theme.StyleNeedsInput, true
		case idle:
			return theme.IconMoonOutline, theme.StyleInReview, true
		default:
			return spinnerFrames[((frame%len(spinnerFrames))+len(spinnerFrames))%len(spinnerFrames)], theme.StyleInProgress, true
		}
	}
	return 0, tcell.Style{}, false
}

// prGlyph maps an argus pr_state string to its indicator glyph and style,
// reporting whether the state is actionable (ok=true). Only the three
// actionable states produce a visible glyph; every other value (none, draft,
// merged-closed, unknown, or empty) returns ok=false so the caller skips the
// cell entirely and lets the name column reclaim the space. The hera-SDK
// deliberately does not export PRGlyph (argus's model.PRState is an int
// type not in the SDK); we map strings directly to avoid importing argus.
func prGlyph(state string) (rune, tcell.Style, bool) {
	switch state {
	case "awaiting-review":
		return theme.IconPRAwaiting, theme.StylePRAwaiting, true
	case "changes-requested":
		return theme.IconPRChanges, theme.StylePRChanges, true
	case "approved":
		return theme.IconPRApproved, theme.StylePRApproved, true
	default:
		return 0, tcell.Style{}, false
	}
}

// roleIcon picks the status icon for a role based on its argus-reported (or,
// as a fallback, binding) state. Archived or dead (binding open in DB but
// argus task is gone) dims the style; the glyph stays truthful.
func (rl *railList) roleIcon(r *roleEntry) (rune, tcell.Style) {
	return statusIcon(r.Archived || r.Dead || r.ArgusArchived, r.HasState, r.NeedsInput, r.Status, r.ArgusIdle, r.Live, rl.animFrame())
}

// orchIcon picks the status icon for a coordinator header from the coord task's
// argus state, so a coordinator row carries the same glyph vocabulary as a
// worker row (an archived root coordinator → dimmed). live falls back to
// "has a live coord binding" (CoordTaskID set) when argus state is unknown.
//
// The MIXED-COORD state outranks the status glyph: an orchestrator displayed
// ACTIVE whose coord task is argus-archived renders the ⊘ repair cue in error
// red — the operator must SEE "this coord is broken/archived" at a glance.
// An orchestrator that is itself archived is NOT mixed (both sides agree) and
// keeps the normal dimmed-archived treatment.
//
// Live coordinators are never rendered as ✓/complete: argus auto-completes
// coordinator tasks when the session goes idle, but the coordinator remains
// live. For non-archived coordinators, "complete" is masked to in_progress+idle
// so the glyph shows ☾ rather than ✓.
func (rl *railList) orchIcon(o *orchEntry) (rune, tcell.Style) {
	if o.CoordArgusArchived && !o.Archived {
		return iconCoordBroken, theme.StyleError
	}
	status, idle := liveCoordDisplayStatus(o.Archived, o.CoordStatus, o.CoordIdle)
	return statusIcon(o.Archived, o.CoordHasState, o.CoordNeedsInput, status, idle, o.CoordTaskID != "", rl.animFrame())
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

// drawPinnedBreadcrumbRow renders line 1 of a two-line pinned-sub-item entry
// (BUG-025): the role's status icon (dimmed) followed by the dimmed ancestry
// trail. If the trail is too wide for the available space it is left-truncated
// with a leading "…" so the NEAREST parent (rightmost text) stays visible.
func (rl *railList) drawPinnedBreadcrumbRow(screen tcell.Screen, x, y, w int, r *roleEntry, ancestry string) {
	if r == nil {
		return
	}
	col := x

	// Status icon, always dimmed — context only, not independently actionable.
	icon, _ := rl.roleIcon(r)
	screen.SetContent(col, y, icon, nil, theme.StyleDimmed)
	col += 2

	// Ancestry trail, dimmed. Left-truncate if too wide so the nearest
	// parent (rightmost text) is always visible.
	availW := w - (col - x)
	if availW < 0 {
		availW = 0
	}
	trail := truncRunesLeft(ancestry, availW)
	widget.DrawText(screen, col, y, availW, trail, theme.StyleDimmed)
}

// drawBreadcrumbNameRow renders line 2 of a two-line pinned-sub-item entry
// (BUG-025): the item's name (indented under line 1) and right-aligned age.
// cursor is true when the preceding breadcrumb row is selected (so the name
// renders in StyleSelected to mirror normal selection highlighting).
func (rl *railList) drawBreadcrumbNameRow(screen tcell.Screen, x, y, w int, r *roleEntry, depth int, cursor bool) {
	if r == nil {
		return
	}
	// A sub-coordinator continuation renders its name without the chevron/count
	// that drawSubCoordRow would add — those belong on the breadcrumb line.
	col := x + depth*indentStep

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

// truncRunesLeft left-truncates s to at most max runes, prepending "…" when
// truncation occurs. This keeps the rightmost (nearest-ancestor) text visible
// in a breadcrumb trail that overflows the available width (BUG-025).
func truncRunesLeft(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return "…" + string(runes[len(runes)-(max-1):])
}
