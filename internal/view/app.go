package view

import (
	"context"
	"fmt"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/anutron/hera/internal/db"
)

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
		app:         tApp,
		pieces:      pieces,
		src:         src,
		coordTask:   coordTask,
		agentTask:   agentTask,
		coordUnsub:  coordUnsub,
		agentUnsub:  agentUnsub,
		coordBridge: coordBridge,
		agentBridge: agentBridge,
		database:    database,
	}

	if err := a.populateRail(database); err != nil {
		return nil, fmt.Errorf("view.BuildApp: populate rail: %w", err)
	}

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
// inserts a node per orchestrator with each role nested underneath.
// Live bindings (ended_at IS NULL) are marked with a "*" prefix on
// the role row. When a.showArchived is true, archived orchestrators
// and roles are also included (suffixed with " [archived]"); when
// false (the default at session start per design.md D5) archived rows
// are filtered out.
func (a *App) populateRail(database *db.DB) error {
	ctx := context.Background()
	root := a.pieces.rootRoot
	root.ClearChildren()

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

	for _, orch := range orchs {
		label := orch.Name
		if orch.ArchivedAt != nil {
			label += " [archived]"
		}
		node := tview.NewTreeNode(label)
		node.SetReference(orchReference{
			ID:       orch.ID,
			Name:     orch.Name,
			Archived: orch.ArchivedAt != nil,
		})

		var roles []*db.Role
		if a.showArchived {
			roles, err = database.Roles.ListByOrchestratorInclusive(ctx, orch.ID)
		} else {
			roles, err = database.Roles.ListByOrchestrator(ctx, orch.ID)
		}
		if err != nil {
			return fmt.Errorf("list roles for orch %d: %w", orch.ID, err)
		}
		for _, role := range roles {
			bnd, err := database.Bindings.GetLiveByRole(ctx, role.ID)
			live := err == nil && bnd != nil
			label := role.Name
			if live {
				label = "* " + label
			} else {
				label = "  " + label
			}
			if role.ArchivedAt != nil {
				label += " [archived]"
			}
			roleNode := tview.NewTreeNode(label)
			ref := roleReference{
				OrchestratorID: orch.ID,
				RoleID:         role.ID,
				RoleKind:       string(role.Kind),
				Name:           role.Name,
				Archived:       role.ArchivedAt != nil,
			}
			if live {
				ref.ArgusTaskID = bnd.ArgusTaskID
			}
			roleNode.SetReference(ref)
			node.AddChild(roleNode)
		}

		root.AddChild(node)
	}

	if len(orchs) == 0 {
		empty := tview.NewTreeNode("(no projects)").SetSelectable(false)
		root.AddChild(empty)
	}

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
// node is not addressable (rail root, empty placeholder).
//
// Satisfies the railSelector contract used by the mutation bridge.
func (a *App) CurrentRailSelection() railSelection {
	if a.pieces.rail == nil {
		return railSelection{}
	}
	node := a.pieces.rail.GetCurrentNode()
	if node == nil {
		return railSelection{}
	}
	switch ref := node.GetReference().(type) {
	case orchReference:
		return railSelection{
			Kind:           selOrchestrator,
			OrchestratorID: ref.ID,
			Name:           ref.Name,
			Archived:       ref.Archived,
		}
	case roleReference:
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

// orchReference is attached to an orchestrator tree node so Stage G/H/I
// operations can resolve the row from the selected node.
type orchReference struct {
	ID       int64
	Name     string
	Archived bool
}

// roleReference is attached to a role tree node so pane bindings + later
// mutation operations can be routed.
type roleReference struct {
	OrchestratorID int64
	RoleID         int64
	RoleKind       string
	ArgusTaskID    string // empty when no live binding
	Name           string
	Archived       bool
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
	node := a.pieces.rail.GetCurrentNode()
	if node == nil {
		return FocusRAIL
	}
	ref, ok := node.GetReference().(roleReference)
	if !ok || ref.ArgusTaskID == "" {
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
