package view

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/anutron/hera/internal/db"
)

// DefaultRailSelectDebounce is the window over which rail j/k cursor
// movements coalesce into a single pane rebind. Without this, a rapid
// hold of j burns one /api/tasks/{id}/resize roundtrip per row.
const DefaultRailSelectDebounce = 120 * time.Millisecond

// App is the hera-view tview application plus its bound layout primitives,
// the open per-pane proxy subscriptions, and the terminalpanes consuming
// them. Stage F builds the layout and a one-shot rail snapshot; Stage G
// wires focus and key routing on top; Stage I hooks up dynamic rail
// refresh.
type App struct {
	app    *tview.Application
	pieces layoutPieces

	// src is the proxy substrate fan-out source. nil-PaneSource is used
	// when no proxy is wired (tests; daemon startup with no bindings).
	src PaneSource

	// mu guards the binding bookkeeping (currently-bound task IDs, the
	// proxy unsubscribe handles, and the bridges feeding each pane).
	mu          sync.Mutex
	coordTask   string
	agentTask   string
	coordUnsub  func()
	agentUnsub  func()
	coordBridge *paneBridge
	agentBridge *paneBridge

	// closed is set after Close runs so subsequent Close calls are no-ops.
	closed bool

	// database is retained for RepopulateRail so the bridge can ask
	// for a refresh without round-tripping back through the daemon.
	// Set by BuildApp; nil-safe at the use sites.
	database *db.DB

	// showArchived is flipped by the `l` rail key. When true,
	// populateRail walks ListInclusive variants so archived
	// orchestrators and roles render in the rail; when false,
	// archived rows are filtered out (the default at session start
	// per design.md D5).
	showArchived bool

	// selectDebounce is the rail j/k selection-change debounce
	// window. Defaults to DefaultRailSelectDebounce; tests can swap
	// in a smaller window (or zero, which means "fire synchronously
	// on the same goroutine") via the test-only setter.
	selectDebounce time.Duration

	// selectMu guards the selection-change timer / pending ref.
	selectMu      sync.Mutex
	selectTimer   *time.Timer
	selectPending any
	selectHasRef  bool
}

// BuildApp constructs the hera-view tview Application. It reads the
// orchestrator / role / binding state from db once at build time to
// populate the rail (Stage I wires live updates). If src is nil, a no-op
// PaneSource is used and panes render placeholder text.
//
// The returned *App owns the tview.Application, the terminalpane
// consumer goroutines, the bridge pump goroutines, and the proxy
// subscription handles. Callers MUST invoke Close to release them when
// the WebSocket session ends.
func BuildApp(database *db.DB, src PaneSource) (*App, error) {
	if database == nil {
		return nil, fmt.Errorf("view.BuildApp: nil db")
	}
	if src == nil {
		src = nilPaneSource{}
	}

	coordTask, agentTask := findInitialSelection(database, src)

	coordPane, coordBridge, coordUnsub := newBoundPane("Coord", "(no coord selected)", coordTask, src)
	agentPane, agentBridge, agentUnsub := newBoundPane("Agent", "(no agent selected)", agentTask, src)

	pieces := buildLayout(coordPane, agentPane)

	tApp := tview.NewApplication()
	tApp.SetRoot(pieces.pages, true)
	tApp.EnableMouse(false)

	a := &App{
		app:            tApp,
		pieces:         pieces,
		src:            src,
		coordTask:      coordTask,
		agentTask:      agentTask,
		coordUnsub:     coordUnsub,
		agentUnsub:     agentUnsub,
		coordBridge:    coordBridge,
		agentBridge:    agentBridge,
		database:       database,
		selectDebounce: DefaultRailSelectDebounce,
	}

	if err := a.populateRail(database); err != nil {
		return nil, fmt.Errorf("view.BuildApp: populate rail: %w", err)
	}

	// Align the rail cursor with the pane selection findInitialSelection
	// just picked, so the operator's mental model (cursor row → pane
	// content) is consistent from the first frame. Done BEFORE the
	// selection-changed callback wires up so this positional sync does
	// not trigger a spurious rebind.
	if agentTask != "" {
		for _, o := range a.pieces.rail.orchestrators {
			for _, r := range o.Roles {
				if r.ArgusTaskID == agentTask {
					a.pieces.rail.SelectByRoleID(r.RoleID)
				}
			}
		}
	}

	// Wire the rail's selection-change callback so subsequent j/k cursor
	// movement (and DAO-driven repopulates that land on a new row)
	// triggers a debounced rebind of the COORD / AGENT panes.
	a.pieces.rail.SetOnSelectionChanged(a.onRailSelectionChanged)

	return a, nil
}

// Application returns the wrapped tview Application. Stage E's WebSocket
// server attaches its custom screen and runs the event loop on this
// handle.
func (a *App) Application() *tview.Application {
	return a.app
}

// Close stops the terminalpane consumer goroutines, cancels each pane's
// bridge pump, and releases every open proxy subscription. Idempotent.
func (a *App) Close() {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	coordUnsub := a.coordUnsub
	agentUnsub := a.agentUnsub
	coordBridge := a.coordBridge
	agentBridge := a.agentBridge
	a.coordUnsub = nil
	a.agentUnsub = nil
	a.coordBridge = nil
	a.agentBridge = nil
	a.mu.Unlock()

	a.selectMu.Lock()
	if a.selectTimer != nil {
		a.selectTimer.Stop()
		a.selectTimer = nil
	}
	a.selectPending = nil
	a.selectHasRef = false
	a.selectMu.Unlock()

	if coordUnsub != nil {
		coordUnsub()
	}
	if agentUnsub != nil {
		agentUnsub()
	}
	if coordBridge != nil {
		coordBridge.stop()
	}
	if agentBridge != nil {
		agentBridge.stop()
	}
	a.pieces.coord.Close()
	a.pieces.agent.Close()
}

// populateRail walks the orchestrators / roles / bindings tables and
// hands the result to the rail widget. Coord roles are NOT added to the
// orchestrator's Roles slice; instead each orchestrator's CoordTaskID
// captures the live coord binding so the COORD pane can rebind
// implicitly when an agent (or the header) is selected.
//
// Live bindings (ended_at IS NULL) drive the moon-stars icon on the
// role row; idle roles get the moon-outline icon. Bindings whose argus
// task has gone away (per the optional TaskAliveChecker on a.src) are
// marked Dead so the rail can hide or dim them.
//
// When a.showArchived is true, archived orchestrators and roles render
// below the Archive separator and dead bindings are kept (dimmed);
// otherwise both are filtered out (default per design.md D5).
func (a *App) populateRail(database *db.DB) error {
	ctx := context.Background()

	checker, _ := a.src.(TaskAliveChecker)

	var (
		orchs []*db.Orchestrator
		err   error
	)
	if a.showArchived {
		orchs, err = database.Orchestrators.ListInclusive(ctx)
	} else {
		orchs, err = database.Orchestrators.List(ctx)
	}
	if err != nil {
		return fmt.Errorf("list orchestrators: %w", err)
	}

	entries := make([]*orchEntry, 0, len(orchs))
	for _, orch := range orchs {
		entry := &orchEntry{
			ID:       orch.ID,
			Name:     orch.Name,
			Archived: orch.ArchivedAt != nil,
		}

		var roles []*db.Role
		if a.showArchived {
			roles, err = database.Roles.ListByOrchestratorInclusive(ctx, orch.ID)
		} else {
			roles, err = database.Roles.ListByOrchestrator(ctx, orch.ID)
		}
		if err != nil {
			return fmt.Errorf("list roles for orch %d: %w", orch.ID, err)
		}
		// Hold the coord role aside so we can fall back to rendering it as
		// the orchestrator's sole row when no agents exist (operator still
		// needs a way to reach the coord pane in agent-less projects).
		var coordRole *db.Role
		var coordLive bool
		var coordDead bool
		var coordTaskID string
		var coordStartedAt time.Time
		for _, role := range roles {
			bnd, _ := database.Bindings.GetLiveByRole(ctx, role.ID)
			live := bnd != nil
			dead := false
			var argusTaskID string
			startedAt := role.CreatedAt
			if live {
				argusTaskID = bnd.ArgusTaskID
				startedAt = bnd.StartedAt
				if checker != nil && !checker.IsTaskAlive(argusTaskID) {
					dead = true
				}
			}

			// Coord roles do not render as their own rail row in the common
			// case. The first live + alive + non-archived coord binding
			// feeds the orchestrator's CoordTaskID so the COORD pane can
			// rebind implicitly when an agent / header is selected.
			// Archived or dead coord bindings are skipped so the COORD
			// pane doesn't get bound to a tombstone. We hold a copy of the
			// first eligible coord role aside in case the orchestrator
			// ends up with zero agent rows — see below.
			if role.Kind == db.KindCoordinator {
				archived := role.ArchivedAt != nil
				if live && !dead && !archived && entry.CoordTaskID == "" {
					entry.CoordTaskID = argusTaskID
					coordRole = role
					coordLive = live
					coordDead = dead
					coordTaskID = argusTaskID
					coordStartedAt = startedAt
				}
				continue
			}

			r := &roleEntry{
				OrchestratorID: orch.ID,
				RoleID:         role.ID,
				RoleKind:       string(role.Kind),
				Name:           role.Name,
				Live:           live,
				Dead:           dead,
				ArgusTaskID:    argusTaskID,
				Archived:       role.ArchivedAt != nil,
				StartedAt:      startedAt,
			}
			entry.Roles = append(entry.Roles, r)
		}

		// Agent-less fallback: when the orchestrator would otherwise have
		// zero visible rows, surface its coord as the sole row so the
		// operator has something selectable. The rail's selection-change
		// rebind still routes COORD-to-coord-task as usual; this row's
		// role-row is just the operator-facing affordance.
		if len(entry.Roles) == 0 && coordRole != nil {
			entry.Roles = append(entry.Roles, &roleEntry{
				OrchestratorID: orch.ID,
				RoleID:         coordRole.ID,
				RoleKind:       string(coordRole.Kind),
				Name:           coordRole.Name,
				Live:           coordLive,
				Dead:           coordDead,
				ArgusTaskID:    coordTaskID,
				Archived:       coordRole.ArchivedAt != nil,
				StartedAt:      coordStartedAt,
			})
		}

		entries = append(entries, entry)
	}

	a.pieces.rail.SetShowArchived(a.showArchived)
	a.pieces.rail.SetOrchestrators(entries)
	return nil
}

// RepopulateRail re-renders the rail from the current DB state. Safe
// to call from any goroutine — it bounces through QueueUpdateDraw so
// the actual node-tree mutation happens on the tview event loop.
//
// Satisfies the repopulator contract used by the mutation bridge.
func (a *App) RepopulateRail() {
	if a.database == nil {
		return
	}
	body := func() {
		_ = a.populateRail(a.database)
	}
	if a.app == nil {
		body()
		return
	}
	a.app.QueueUpdateDraw(body)
}

// CurrentRailSelection returns the bridge's view of the currently-
// highlighted rail row. Returns a zero-value railSelection when the
// cursor is not on an addressable row (archive separator, empty).
//
// Satisfies the railSelector contract used by the mutation bridge.
func (a *App) CurrentRailSelection() railSelection {
	if a.pieces.rail == nil {
		return railSelection{}
	}
	switch ref := a.pieces.rail.CurrentRef().(type) {
	case *orchEntry:
		return railSelection{
			Kind:           selOrchestrator,
			OrchestratorID: ref.ID,
			Name:           ref.Name,
			Archived:       ref.Archived,
		}
	case *roleEntry:
		return railSelection{
			Kind:           selRole,
			OrchestratorID: ref.OrchestratorID,
			RoleID:         ref.RoleID,
			Name:           ref.Name,
			RoleKind:       ref.RoleKind,
			Archived:       ref.Archived,
		}
	}
	return railSelection{}
}

// findInitialSelection picks the argus task IDs to bind to the coord and
// agent panes at build time. Selection rules, in order of preference:
//
//  1. The first orchestrator with a worker/freelance role whose live
//     binding's argus task is still alive (per the optional
//     TaskAliveChecker). Its coord task (if any) feeds the COORD pane;
//     the live worker task feeds the AGENT pane.
//  2. If no orchestrator has a live worker, the first orchestrator with
//     an ALIVE coord. The coord task feeds BOTH panes so the operator
//     sees coord output in the agent pane until they pick a worker.
//  3. Both return values are empty — every pane falls back to its
//     placeholder text.
//
// The TaskAliveChecker is optional: when src does not satisfy it (most
// tests pass a fake source without aliveness data, and the daemon's
// nilPaneSource is also unchecked), selection treats every live DB
// binding as alive, matching the pre-stage-K behavior.
func findInitialSelection(database *db.DB, src PaneSource) (coordTask, agentTask string) {
	checker, _ := src.(TaskAliveChecker)
	ctx := context.Background()
	orchs, err := database.Orchestrators.List(ctx)
	if err != nil {
		return "", ""
	}

	type orchSnap struct {
		coord  string
		worker string
	}
	snaps := make([]orchSnap, 0, len(orchs))
	for _, orch := range orchs {
		var snap orchSnap
		roles, err := database.Roles.ListByOrchestrator(ctx, orch.ID)
		if err != nil {
			snaps = append(snaps, snap)
			continue
		}
		for _, role := range roles {
			bnd, err := database.Bindings.GetLiveByRole(ctx, role.ID)
			if err != nil || bnd == nil {
				continue
			}
			switch role.Kind {
			case db.KindCoordinator:
				if snap.coord == "" {
					snap.coord = bnd.ArgusTaskID
				}
			default:
				if snap.worker == "" && (checker == nil || checker.IsTaskAlive(bnd.ArgusTaskID)) {
					snap.worker = bnd.ArgusTaskID
				}
			}
		}
		snaps = append(snaps, snap)
	}

	for _, s := range snaps {
		if s.worker != "" {
			return s.coord, s.worker
		}
	}
	for _, s := range snaps {
		if s.coord != "" && (checker == nil || checker.IsTaskAlive(s.coord)) {
			return s.coord, s.coord
		}
	}
	return "", ""
}

// CoordTaskID returns the argus task id currently bound to the COORD
// pane, or "" if none. Satisfies the KeyRouter.PaneTargets contract so
// pane-focus keystrokes route to the right task.
func (a *App) CoordTaskID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.coordTask
}

// AgentTaskID returns the argus task id currently bound to the AGENT
// pane, or "" if none.
func (a *App) AgentTaskID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.agentTask
}

// OnFocusChanged repaints the colored focus border so the operator sees
// which of the three elements is active and routes tview's primitive
// focus to the matching widget so propagated keys (the rail's j/k/↑/↓)
// land where they should. Satisfies the KeyRouter.BorderUpdater contract.
// Called from the tview input pump (so direct tview state mutation is
// safe here — single-threaded by contract).
//
// The terminalpane widget paints its own border via the SDK's theme
// styles based on its HasFocus() state, so SetBorderColor on the coord
// and agent panes is mostly cosmetic on top of that. The rail (a plain
// TreeView) still relies on Box.SetBorderColor for focus feedback.
func (a *App) OnFocusChanged(state FocusState) {
	const focused = tcell.ColorYellow
	const unfocused = tcell.ColorWhite

	// Reflect the focus state in the bottom bar — the operator's primary cue
	// for "which element am I driving right now". Without this the bar stays
	// a static [RAIL] string and pane focus looks like a frozen rail.
	if a.pieces.bottom != nil {
		a.pieces.bottom.SetText(bottomBarText(state))
	}

	a.pieces.rail.SetBorderColor(unfocused)
	a.pieces.coord.SetBorderColor(unfocused)
	a.pieces.agent.SetBorderColor(unfocused)

	switch state {
	case FocusRAIL:
		a.pieces.rail.SetBorderColor(focused)
		if a.app != nil {
			a.app.SetFocus(a.pieces.rail)
		}
	case FocusCOORD:
		a.pieces.coord.SetBorderColor(focused)
		if a.app != nil {
			a.app.SetFocus(a.pieces.coord)
		}
	case FocusAGENT:
		a.pieces.agent.SetBorderColor(focused)
		if a.app != nil {
			a.app.SetFocus(a.pieces.agent)
		}
	}
}

// OnRailSelectEnter handles Enter pressed while RAIL has focus. It reads
// the rail's currently-highlighted node, and if that node references a
// live role binding it rebinds the matching pane (COORD for a
// coordinator row, AGENT for a worker/freelance row) to the row's argus
// task and returns the focus target the operator should land in.
// Non-bindable rows (orchestrator headers, role rows without a live
// binding) return FocusRAIL so the KeyRouter propagates Enter to the
// tree for its native fold/unfold behavior.
//
// Satisfies the KeyRouter.RailSelectHandler contract. Runs on the tview
// input pump goroutine — direct mutation of pieces / Flex contents is
// safe here.
func (a *App) OnRailSelectEnter() FocusState {
	if a.pieces.rail == nil {
		return FocusRAIL
	}
	ref, ok := a.pieces.rail.CurrentRef().(*roleEntry)
	if !ok || ref == nil || ref.ArgusTaskID == "" {
		return FocusRAIL
	}
	// Browsing flow: Enter rebinds the appropriate pane but KEEPS focus
	// on RAIL so subsequent j/k continues to navigate the tree and
	// subsequent Enter rebinds again. To start typing into a pane, the
	// operator uses the Ctrl-arrow ladder to move focus explicitly. (The
	// original D4 design said Enter jumps to AGENT; live testing showed
	// that broke the browse-many-roles flow because the second Enter was
	// consumed by the focused pane instead of triggering another rebind.)
	if ref.RoleKind == string(db.KindCoordinator) {
		a.rebindCoord(ref.ArgusTaskID)
	} else {
		a.rebindAgent(ref.ArgusTaskID)
	}
	return FocusRAIL
}

// rebindCoord swaps the COORD pane's underlying paneBridge / subscription
// over to the given argus task ID. No-op when the pane is already bound
// to that task or the App has been closed.
func (a *App) rebindCoord(taskID string) {
	a.mu.Lock()
	if a.closed || a.coordTask == taskID {
		a.mu.Unlock()
		return
	}
	oldUnsub := a.coordUnsub
	oldBridge := a.coordBridge
	oldPane := a.pieces.coord
	pane, bridge, unsub := newBoundPane("Coord", "(no coord selected)", taskID, a.src)
	a.coordTask = taskID
	a.coordBridge = bridge
	a.coordUnsub = unsub
	a.pieces.coord = pane
	a.mu.Unlock()

	if oldUnsub != nil {
		oldUnsub()
	}
	if oldBridge != nil {
		oldBridge.stop()
	}
	if oldPane != nil {
		oldPane.Close()
	}
	a.refreshBody()
}

// rebindAgent swaps the AGENT pane's underlying paneBridge / subscription
// over to the given argus task ID. No-op when the pane is already bound
// to that task or the App has been closed.
func (a *App) rebindAgent(taskID string) {
	a.mu.Lock()
	if a.closed || a.agentTask == taskID {
		a.mu.Unlock()
		return
	}
	oldUnsub := a.agentUnsub
	oldBridge := a.agentBridge
	oldPane := a.pieces.agent
	pane, bridge, unsub := newBoundPane("Agent", "(no agent selected)", taskID, a.src)
	a.agentTask = taskID
	a.agentBridge = bridge
	a.agentUnsub = unsub
	a.pieces.agent = pane
	a.mu.Unlock()

	if oldUnsub != nil {
		oldUnsub()
	}
	if oldBridge != nil {
		oldBridge.stop()
	}
	if oldPane != nil {
		oldPane.Close()
	}
	a.refreshBody()
}

// onRailSelectionChanged is invoked by the rail widget when the cursor
// lands on a new selectable row. It schedules a debounced rebind so
// rapid j/k traversal coalesces into a single pane swap on the row the
// cursor finally rests on, rather than POSTing /api/tasks/{id}/resize
// for every intermediate stop.
//
// Selection-rebind semantics:
//   - role row (agent): rebind COORD to the orchestrator's coord task
//     and AGENT to the role's argus task.
//   - orchestrator header: rebind COORD to the orchestrator's coord
//     task; leave AGENT bound to whatever it was previously so the
//     operator's last agent stays visible while they re-anchor the
//     coord pane.
//
// The handler runs on the goroutine that triggered the cursor move
// (the tview input pump for j/k); the deferred work runs on a timer
// goroutine and bounces back onto the event loop via QueueUpdateDraw
// before mutating any pane primitives.
func (a *App) onRailSelectionChanged(ref any) {
	a.selectMu.Lock()
	if a.closed {
		a.selectMu.Unlock()
		return
	}
	a.selectPending = ref
	a.selectHasRef = true
	delay := a.selectDebounce
	if delay <= 0 {
		// Fire-synchronously path: drop the lock before the rebind so the
		// rebind itself can acquire a.mu without lock-order issues.
		a.selectPending = nil
		a.selectHasRef = false
		a.selectMu.Unlock()
		a.applyRailSelection(ref)
		return
	}
	if a.selectTimer == nil {
		a.selectTimer = time.AfterFunc(delay, a.fireRailSelection)
	} else {
		a.selectTimer.Reset(delay)
	}
	a.selectMu.Unlock()
}

// fireRailSelection runs from the debounce timer's goroutine. It reads
// the latest pending ref and bounces the actual pane rebind onto the
// tview event loop so primitive mutation stays single-threaded.
func (a *App) fireRailSelection() {
	a.selectMu.Lock()
	if !a.selectHasRef || a.closed {
		a.selectMu.Unlock()
		return
	}
	ref := a.selectPending
	a.selectPending = nil
	a.selectHasRef = false
	a.selectMu.Unlock()

	if a.app != nil {
		a.app.QueueUpdateDraw(func() { a.applyRailSelection(ref) })
		return
	}
	a.applyRailSelection(ref)
}

// applyRailSelection rebinds the COORD / AGENT panes per the
// selection-rebind semantics documented on onRailSelectionChanged. Must
// run on the tview event loop when a.app != nil (the input pump or a
// QueueUpdateDraw callback).
func (a *App) applyRailSelection(ref any) {
	switch r := ref.(type) {
	case *orchEntry:
		if r == nil {
			return
		}
		if r.CoordTaskID != "" {
			a.rebindCoord(r.CoordTaskID)
		}
		// Header selection leaves the agent pane alone so the last-
		// picked agent stays visible while the operator changes coord
		// targets. Selecting an agent row (below) rebinds both panes.
	case *roleEntry:
		if r == nil {
			return
		}
		// Locate the orchestrator so we can also rebind COORD to its
		// coord task. orchestrators is the rail's source of truth so a
		// linear walk is cheap.
		var coordTask string
		for _, o := range a.pieces.rail.orchestrators {
			if o.ID == r.OrchestratorID {
				coordTask = o.CoordTaskID
				break
			}
		}
		if coordTask != "" {
			a.rebindCoord(coordTask)
		}
		if r.ArgusTaskID != "" {
			a.rebindAgent(r.ArgusTaskID)
		}
	}
}

// refreshBody re-composes the body Flex with the current rail + coord +
// agent panes. Called after a pane rebind so the new pane primitive is
// drawn in place of the old one.
func (a *App) refreshBody() {
	body := a.pieces.body
	if body == nil {
		return
	}
	body.Clear()
	body.AddItem(a.pieces.rail, RailWidth, 0, false)
	body.AddItem(a.pieces.coord, 0, 1, false)
	body.AddItem(a.pieces.agent, 0, 1, false)
}
