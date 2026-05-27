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

	coordTask, agentTask := findInitialSelection(database)

	coordPane, coordBridge, coordUnsub := newBoundPane("Coord", "(no coord selected)", coordTask, src)
	agentPane, agentBridge, agentUnsub := newBoundPane("Agent", "(no agent selected)", agentTask, src)

	pieces := buildLayout(coordPane, agentPane)

	tApp := tview.NewApplication()
	tApp.SetRoot(pieces.root, true)
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

// populateRail walks the orchestrators / roles / bindings tables once at
// build time and inserts a node per orchestrator with each role nested
// underneath. Live bindings (ended_at IS NULL) are marked with a "*"
// prefix on the role row; archived rows are omitted (Stage A's
// archived_at filter is not yet merged on this branch — caller-side
// filtering on bindings' ended_at suffices for the initial render).
func (a *App) populateRail(database *db.DB) error {
	ctx := context.Background()
	root := a.pieces.rootRoot
	root.ClearChildren()

	orchs, err := database.Orchestrators.List(ctx)
	if err != nil {
		return fmt.Errorf("list orchestrators: %w", err)
	}

	for _, orch := range orchs {
		node := tview.NewTreeNode(orch.Name)
		node.SetReference(orchReference{ID: orch.ID})

		roles, err := database.Roles.ListByOrchestrator(ctx, orch.ID)
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
			roleNode := tview.NewTreeNode(label)
			ref := roleReference{
				OrchestratorID: orch.ID,
				RoleID:         role.ID,
				RoleKind:       string(role.Kind),
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

// orchReference is attached to an orchestrator tree node so Stage G/H/I
// operations can resolve the row from the selected node.
type orchReference struct {
	ID int64
}

// roleReference is attached to a role tree node so pane bindings + later
// mutation operations can be routed.
type roleReference struct {
	OrchestratorID int64
	RoleID         int64
	RoleKind       string
	ArgusTaskID    string // empty when no live binding
}

// findInitialSelection picks the first live agent (worker role) on the
// first non-archived orchestrator that has one and returns the argus
// task IDs to bind to the coord and agent panes. Both return values are
// empty when no live agent is found anywhere.
func findInitialSelection(database *db.DB) (coordTask, agentTask string) {
	ctx := context.Background()
	orchs, err := database.Orchestrators.List(ctx)
	if err != nil {
		return "", ""
	}
	for _, orch := range orchs {
		roles, err := database.Roles.ListByOrchestrator(ctx, orch.ID)
		if err != nil {
			continue
		}
		var coord string
		var firstAgent string
		for _, role := range roles {
			bnd, err := database.Bindings.GetLiveByRole(ctx, role.ID)
			if err != nil || bnd == nil {
				continue
			}
			if role.Kind == db.KindCoordinator {
				coord = bnd.ArgusTaskID
			} else if firstAgent == "" {
				firstAgent = bnd.ArgusTaskID
			}
		}
		if firstAgent == "" {
			continue
		}
		return coord, firstAgent
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
// which of the three elements is active. Satisfies the
// KeyRouter.BorderUpdater contract. Called from the tview input pump.
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
	case FocusCOORD:
		a.pieces.coord.SetBorderColor(focused)
	case FocusAGENT:
		a.pieces.agent.SetBorderColor(focused)
	}
}
