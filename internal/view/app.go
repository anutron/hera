package view

import (
	"context"
	"fmt"
	"sort"
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

	// coordPresent / agentPresent track which panes the body currently
	// composes — the three-mode layout (D13), driven by the rail selection:
	//
	//   - coordinator selected: coordPresent=true,  agentPresent=false
	//     (rail + full-width HERA pane, no agent)
	//   - agent selected:       coordPresent=true,  agentPresent=true
	//     (rail + HERA + AGENT split)
	//   - freelancer selected:  coordPresent=false, agentPresent=true
	//     (rail + full-width AGENT pane, no HERA)
	//
	// The absent pane is removed from the body Flex (not hidden) and its proxy
	// subscription is torn down. Defaults match the initial agent-mode split.
	coordPresent bool
	agentPresent bool

	// focus is the session's focus machine, injected via SetFocusMachine so
	// the App can flip the present-pane flags when the body mode changes.
	// nil in tests that build the App without a router.
	focus *FocusMachine

	// control sends the argus key-surrender control frames (D12). On every
	// OnFocusChanged the App pushes a focus-aware hotkeys frame so argus's
	// plugin-mode bottom bar + help overlay reflect the current focus. nil
	// (no session conn wired — tests, daemon startup) makes the push a
	// no-op. Injected via SetControl during session wiring.
	control *viewControl

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

	coordPane, coordBridge, coordUnsub := newBoundPane("HERA", "(no coord selected)", coordTask, src)
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
		coordPresent:   true,
		agentPresent:   true,
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

	// Compose the opening frame to match the initial selection's mode. The
	// coordPresent/agentPresent defaults above assume the agent split; without
	// this an initial coordinator (or freelancer) selection would render as a
	// split until the first keypress. applyRailSelection is idempotent on the
	// panes (rebind is a no-op when already bound) so this only fixes the mode.
	a.applyRailSelection(a.pieces.rail.CurrentRef())

	return a, nil
}

// Application returns the wrapped tview Application. Stage E's WebSocket
// server attaches its custom screen and runs the event loop on this
// handle.
func (a *App) Application() *tview.Application {
	return a.app
}

// SetFocusMachine injects the session's focus machine so the App can toggle
// its coordPresent flag when switching to/from freelance (full-width) mode.
// Called once during session wiring, after the machine is constructed.
func (a *App) SetFocusMachine(f *FocusMachine) {
	a.mu.Lock()
	a.focus = f
	a.mu.Unlock()
}

// SetControl injects the session's view-control sender so the App can push the
// focus-aware hotkey dictionary to argus on connect and on every focus change
// (D12). Called once during session wiring, after the conn is accepted. A nil
// sender (no session conn) makes every push a safe no-op.
func (a *App) SetControl(c *viewControl) {
	a.mu.Lock()
	a.control = c
	a.mu.Unlock()
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

// applyArgusState fills a rail row's argus-reported state (status, idle,
// needs-input, archived) from the optional TaskStateProvider, keyed by the
// row's bound argus task id. No-op when no provider is wired (tests) or argus
// has no entry for the task — the row then renders from binding state.
func applyArgusState(r *roleEntry, prov TaskStateProvider) {
	if prov == nil || r == nil || r.ArgusTaskID == "" {
		return
	}
	if st, ok := prov.TaskState(r.ArgusTaskID); ok {
		r.HasState = true
		r.Status = st.Status
		r.ArgusIdle = st.Idle
		r.NeedsInput = st.NeedsInput
		r.ArgusArchived = st.Archived
		return
	}
	// Provider present but argus has no such task: it's gone (deleted /
	// pruned worktree). Argus doesn't show deleted tasks at all, so neither
	// should hera's active rail — mark it dead (hidden unless `l listall`).
	// Gated on cache readiness so a cold cache doesn't transiently hide
	// live rows on first render.
	if rp, ok := prov.(interface{ StatesReady() bool }); !ok || rp.StatesReady() {
		r.Dead = true
	}
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
	stateProv, _ := a.src.(TaskStateProvider)

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
			} else if hist, _ := database.Bindings.ListByRole(ctx, role.ID); len(hist) > 0 {
				// No live binding (the agent finished / its task completed).
				// Fall back to the most-recent binding's argus task so the
				// row stays selectable and its pane can show the agent's last
				// output read-only — argus serves /api/tasks/{id}/output for
				// completed tasks. ListByRole is started_at DESC, so hist[0]
				// is the latest binding. Without this, done agents have no
				// task id and the pane never rebinds off them (stuck panes).
				argusTaskID = hist[0].ArgusTaskID
				startedAt = hist[0].StartedAt
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
				// Capture the coord role id (first coord seen) regardless of its
				// archived/live state so the resurrect-on-Enter flow can target it
				// when the operator presses Enter on an archived root coordinator.
				if entry.CoordRoleID == 0 {
					entry.CoordRoleID = role.ID
				}
				if !dead && !archived && entry.CoordTaskID == "" && argusTaskID != "" {
					// Prefer a live coord; fall back to its most-recent binding
					// (argusTaskID set above) so the coord pane can still show
					// the coordinator's last output after its task finished —
					// fixes the "coord pane doesn't follow selection" case.
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
			applyArgusState(r, stateProv)
			entry.Roles = append(entry.Roles, r)
		}

		// Agent-less fallback: when the orchestrator would otherwise have
		// zero visible rows, surface its coord as the sole row so the
		// operator has something selectable. The rail's selection-change
		// rebind still routes COORD-to-coord-task as usual; this row's
		// role-row is just the operator-facing affordance.
		if len(entry.Roles) == 0 && coordRole != nil {
			cr := &roleEntry{
				OrchestratorID: orch.ID,
				RoleID:         coordRole.ID,
				RoleKind:       string(coordRole.Kind),
				Name:           coordRole.Name,
				Live:           coordLive,
				Dead:           coordDead,
				ArgusTaskID:    coordTaskID,
				Archived:       coordRole.ArchivedAt != nil,
				StartedAt:      coordStartedAt,
			}
			applyArgusState(cr, stateProv)
			entry.Roles = append(entry.Roles, cr)
		}

		entries = append(entries, entry)
	}

	a.pieces.rail.SetShowArchived(a.showArchived)
	a.pieces.rail.SetFreelance(a.buildFreelance(ctx, database))
	a.pieces.rail.SetOrchestrators(entries)
	return nil
}

// buildFreelance partitions the live argus task list into the Freelance
// section: every non-archived argus task that hera has never bound (a
// "freelancer") grouped by argus project/repo, "the same way Argus shows
// them". Returns nil when no FreelanceProvider is wired (tests) or no
// freelancers exist. Archived freelancers are included only when
// showArchived is set, so the active rail mirrors argus's non-archived set.
func (a *App) buildFreelance(ctx context.Context, database *db.DB) []*freelanceProject {
	prov, ok := a.src.(FreelanceProvider)
	if !ok {
		return nil
	}
	tasks := prov.LiveTasks()
	if len(tasks) == 0 {
		return nil
	}
	bound, err := database.Bindings.AllArgusTaskIDs(ctx)
	if err != nil {
		// On a query failure, fail safe to "everything looks managed" so we
		// never mislabel a hera-managed task as a freelancer.
		return nil
	}

	byProject := map[string]*freelanceProject{}
	var order []string
	for _, t := range tasks {
		if _, managed := bound[t.ID]; managed {
			continue
		}
		if t.State.Archived && !a.showArchived {
			continue
		}
		fp, seen := byProject[t.Project]
		if !seen {
			fp = &freelanceProject{Project: t.Project}
			byProject[t.Project] = fp
			order = append(order, t.Project)
		}
		fp.Tasks = append(fp.Tasks, &roleEntry{
			RoleKind:        string(db.KindFreelance),
			Name:            t.Name,
			ArgusTaskID:     t.ID,
			Live:            t.State.Status == "in_progress" && !t.State.Idle,
			ElapsedOverride: t.Elapsed,
			HasState:        true,
			Status:          t.State.Status,
			ArgusIdle:       t.State.Idle,
			NeedsInput:      t.State.NeedsInput,
			ArgusArchived:   t.State.Archived,
		})
	}
	if len(order) == 0 {
		return nil
	}
	sort.Strings(order)
	out := make([]*freelanceProject, 0, len(order))
	for _, p := range order {
		out = append(out, byProject[p])
	}
	return out
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
			CoordRoleID:    ref.CoordRoleID,
			Name:           ref.Name,
			Archived:       ref.Archived,
			// Child agents that `^d` will also destroy: the orchestrator's
			// live (non-archived) child roles.
			ChildCount: countLiveRoles(ref.Roles),
		}
	case *roleEntry:
		return railSelection{
			Kind:           selRole,
			OrchestratorID: ref.OrchestratorID,
			RoleID:         ref.RoleID,
			Name:           ref.Name,
			RoleKind:       ref.RoleKind,
			Archived:       ref.Archived,
			ArgusTaskID:    ref.ArgusTaskID,
		}
	}
	return railSelection{}
}

// countLiveRoles returns the number of non-archived roles in the slice —
// the child-agent count used by the `^d` destructive-delete warning.
func countLiveRoles(roles []*roleEntry) int {
	n := 0
	for _, r := range roles {
		if r != nil && !r.Archived {
			n++
		}
	}
	return n
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

	// Advertise the focus-aware hotkey dictionary to argus so its plugin-mode
	// bottom bar + help overlay reflect the current focus (D12). Hera renders
	// no bottom bar of its own; argus owns that chrome. nil control (no
	// session conn) makes this a no-op.
	a.mu.Lock()
	coordPresent := a.coordPresent
	control := a.control
	a.mu.Unlock()
	if control != nil {
		_ = control.SendHotkeys(hotkeyItems(state, coordPresent))
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

// OnRailSelectEnter handles Enter pressed while RAIL has focus. It enters the
// selection's PRIMARY pane (D13 "Enter enters the selection's primary pane"):
//
//   - a coordinator row (orchestrator header or sub-coordinator) → its HERA
//     (COORD) pane, returning FocusCOORD;
//   - an agent row → its AGENT pane, returning FocusAGENT;
//   - a freelancer row → its AGENT pane, returning FocusAGENT;
//   - a header / expando row (Freelance repo group or Archive) → toggle the
//     section fold and stay in RAIL (returns FocusRAIL). Enter NEVER enters a
//     pane on these rows; `space` and Enter both fold them.
//
// In each pane case the row is bound first (composing the right body mode and
// tearing down the absent pane's subscription) so the operator lands IN the
// PTY ready to type. This is the reliable way into a pane when hosted inside
// argus, which eats the Cmd/Ctrl-arrow focus ladder; browsing without
// entering is j/k, which rebinds the panes live via the selection callback.
//
// Satisfies the KeyRouter.RailSelectHandler contract. Runs on the tview
// input pump goroutine — direct mutation of pieces / Flex contents is safe.
func (a *App) OnRailSelectEnter() FocusState {
	if a.pieces.rail == nil {
		return FocusRAIL
	}
	switch ref := a.pieces.rail.CurrentRef().(type) {
	case *orchEntry:
		// Orchestrator header = coordinator selection → full-width HERA pane.
		if ref == nil {
			return FocusRAIL
		}
		a.applyRailSelection(ref)
		if ref.CoordTaskID == "" {
			// No coord task to enter (agent-less / dead coord): stay in RAIL.
			return FocusRAIL
		}
		return FocusCOORD
	case *roleEntry:
		if ref == nil || ref.ArgusTaskID == "" {
			return FocusRAIL
		}
		a.applyRailSelection(ref)
		if ref.RoleKind == string(db.KindCoordinator) {
			// A sub-coordinator is a coordinator selection → HERA pane.
			return FocusCOORD
		}
		// Worker or freelancer → AGENT pane.
		return FocusAGENT
	case *freelanceProject:
		// Freelance repo-group header: Enter folds the group, never enters a
		// pane. Toggle the fold and remain in RAIL.
		a.pieces.rail.ToggleCollapse()
		return FocusRAIL
	}
	// nil ref → Freelance separator or Archive expando. Fold the section
	// (ToggleCollapse handles the Archive expando under the cursor) and stay
	// in RAIL.
	a.pieces.rail.ToggleCollapse()
	return FocusRAIL
}

// ScrollFocusedPane scrolls the pane that currently has focus by delta lines
// (positive = up into scrollback, negative = down toward the live screen),
// driving the ⇧↑/⇧↓ keys (D15). It MUST NOT move the rail selection. No-op when
// focus is RAIL (no pane is focused) or the targeted pane is absent.
//
// Satisfies the KeyRouter.PaneScroller contract. Runs on the tview input pump.
//
// LIMITATION: the SDK terminalpane paints only the live screen (see
// pinnedTerminalPane.scrollOffset), so the offset is recorded but the visible
// surface does not yet scroll. The key is intercepted end-to-end so it never
// leaks to the PTY or the rail.
func (a *App) ScrollFocusedPane(state FocusState, delta int) {
	a.mu.Lock()
	var pane *pinnedTerminalPane
	switch state {
	case FocusCOORD:
		pane = a.pieces.coord
	case FocusAGENT:
		pane = a.pieces.agent
	}
	a.mu.Unlock()
	if pane == nil {
		return
	}
	pane.ScrollBy(delta)
}

// InPaneNavigate moves the rail selection to the next (dir>0) or previous
// (dir<0) pane-bindable agent row and re-enters that selection's primary pane,
// keeping focus INSIDE a pane (D15's ⌘↑/⌘↓ in-pane navigation). It reuses the
// Stage-O selection+enter-pane logic (applyRailSelection via OnRailSelectEnter)
// so the body re-composes and the focus machine lands on the new selection's
// primary pane — never RAIL. Returns the focus state the operator should land
// in (FocusCOORD for a coordinator selection, FocusAGENT for a worker /
// freelancer). When there is no further bindable row in the requested
// direction the selection (and focus) stay put.
//
// A coord-less orchestrator header (CoordTaskID == "") is pane-bindable for
// normal rail navigation (j/k still land on it) but has no pane to enter, so
// OnRailSelectEnter yields FocusRAIL. In-pane flipping must never strand the
// operator with the selection on such a header while focus silently lingers in
// the previous pane, so we keep stepping in the same direction until we reach a
// row that actually enters a pane (COORD/AGENT). If none exists that way, the
// original selection + focus are restored so the operator stays put.
//
// Satisfies the KeyRouter.InPaneNavigator contract. Runs on the tview input
// pump goroutine — direct mutation of the rail cursor and panes is safe.
func (a *App) InPaneNavigate(dir int) FocusState {
	if a.pieces.rail == nil {
		return FocusRAIL
	}
	startCursor := a.pieces.rail.cursor
	for a.pieces.rail.StepToBindable(dir) {
		if target := a.OnRailSelectEnter(); target != FocusRAIL {
			return target
		}
		// Landed on a coord-less header (no pane to enter): keep flipping in the
		// same direction. StepToBindable clamps at the ends, so this terminates.
	}
	// No further pane-bindable row that way: restore the original selection so
	// the operator's selection + focus stay where they were.
	a.pieces.rail.cursor = startCursor
	a.pieces.rail.clampOffset()
	a.applyRailSelection(a.pieces.rail.CurrentRef())
	return a.focusForCurrentSelection()
}

// focusForCurrentSelection returns the pane-focus state the current rail
// selection's primary pane corresponds to, WITHOUT moving the cursor or
// rebinding. Used when in-pane nav has nowhere to step so the operator stays in
// the pane they're already in.
func (a *App) focusForCurrentSelection() FocusState {
	switch ref := a.pieces.rail.CurrentRef().(type) {
	case *orchEntry:
		if ref != nil && ref.CoordTaskID != "" {
			return FocusCOORD
		}
	case *roleEntry:
		if ref == nil || ref.ArgusTaskID == "" {
			return FocusRAIL
		}
		if ref.RoleKind == string(db.KindCoordinator) {
			return FocusCOORD
		}
		return FocusAGENT
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
	pane, bridge, unsub := newBoundPane("HERA", "(no coord selected)", taskID, a.src)
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

// applyRailSelection re-composes the body into the mode the highlighted row
// demands (D13's three modes) and rebinds the present panes' subscriptions.
// Must run on the tview event loop when a.app != nil (the input pump or a
// QueueUpdateDraw callback).
//
//   - coordinator (orchestrator header or sub-coordinator role) → coordinator
//     mode: full-width HERA pane bound to the coord task, no AGENT pane;
//   - worker/agent role → agent mode: HERA bound to its coord + AGENT bound to
//     the agent (split);
//   - freelancer role → freelance mode: full-width AGENT bound to the
//     freelancer, coord released (no HERA pane);
//   - Freelance repo-group header → not pane-bindable; leave the mode and
//     panes untouched (Enter/space fold it instead).
func (a *App) applyRailSelection(ref any) {
	switch r := ref.(type) {
	case *orchEntry:
		if r == nil {
			return
		}
		// Coordinator mode: bind HERA to the coord, drop the agent pane.
		if r.CoordTaskID != "" {
			a.rebindCoord(r.CoordTaskID)
		}
		a.setBodyMode(true, false)
	case *freelanceProject:
		// A Freelance repo-group header is not pane-bindable; leave the panes
		// and the current mode untouched (selecting a task row under it
		// switches to freelance mode).
		return
	case *roleEntry:
		if r == nil {
			return
		}
		if r.RoleKind == string(db.KindFreelance) {
			// Freelancer: full-width AGENT, no coord (release the coord sub).
			a.rebindCoord("")
			if r.ArgusTaskID != "" {
				a.rebindAgent(r.ArgusTaskID)
			}
			a.setBodyMode(false, true)
			return
		}
		// Locate the orchestrator so we can rebind COORD to its coord task.
		// orchestrators is the rail's source of truth so a linear walk is cheap.
		var coordTask string
		for _, o := range a.pieces.rail.orchestrators {
			if o.ID == r.OrchestratorID {
				coordTask = o.CoordTaskID
				break
			}
		}
		// Always rebind COORD to the selected project's coord task — including
		// "" when the project has no coord. Otherwise the HERA pane would keep
		// showing the PREVIOUS project's coordinator (the split stays, but bound
		// to a foreign coord). rebindCoord("") clears it to its placeholder.
		a.rebindCoord(coordTask)
		if r.RoleKind == string(db.KindCoordinator) {
			// A sub-coordinator selection is coordinator mode: full-width HERA.
			a.setBodyMode(true, false)
			return
		}
		// Worker/agent: HERA + AGENT split.
		if r.ArgusTaskID != "" {
			a.rebindAgent(r.ArgusTaskID)
		}
		a.setBodyMode(true, true)
	}
}

// setBodyMode records which panes the body composes, re-lays out the body
// Flex, and tells the focus machine which positions exist so traversal steps
// only through present panes and never rests on a torn-down one. Idempotent —
// a no-op when the mode is unchanged. Runs on the tview event loop.
func (a *App) setBodyMode(coordPresent, agentPresent bool) {
	a.mu.Lock()
	if a.coordPresent == coordPresent && a.agentPresent == agentPresent {
		a.mu.Unlock()
		// Still refresh the body so a pane rebind that didn't change the mode
		// redraws the swapped-in primitive.
		a.refreshBody()
		return
	}
	a.coordPresent = coordPresent
	a.agentPresent = agentPresent
	focus := a.focus
	a.mu.Unlock()

	a.refreshBody()

	if focus != nil {
		// Update both present flags; rebalance bumps focus off any now-absent
		// pane. OnFocusChanged repaints borders and the bottom bar.
		focus.SetCoordPresent(coordPresent)
		focus.SetAgentPresent(agentPresent)
		a.OnFocusChanged(focus.State())
	}
}

// refreshBody re-composes the body Flex with the rail plus whichever panes the
// current mode (coordPresent / agentPresent) includes. The absent pane is
// removed from the Flex entirely (not hidden) so the present pane(s) take the
// full canvas. Called after a pane rebind or a mode switch.
func (a *App) refreshBody() {
	body := a.pieces.body
	if body == nil {
		return
	}
	a.mu.Lock()
	coordPresent := a.coordPresent
	agentPresent := a.agentPresent
	a.mu.Unlock()

	body.Clear()
	body.AddItem(a.pieces.rail, RailWidth, 0, false)
	if coordPresent {
		body.AddItem(a.pieces.coord, 0, 1, false)
	}
	if agentPresent {
		body.AddItem(a.pieces.agent, 0, 1, false)
	}
}
