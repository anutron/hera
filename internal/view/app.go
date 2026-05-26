package view

import (
	"context"
	"fmt"
	"sync"

	"github.com/rivo/tview"

	"github.com/anutron/hera/internal/db"
)

// App is the hera-view tview application plus its bound layout primitives,
// the open per-pane proxy subscriptions, and the StreamPanes consuming
// them. Stage F builds the layout and a one-shot rail snapshot; Stage G
// wires focus and key routing on top; Stage I hooks up dynamic rail
// refresh.
type App struct {
	app    *tview.Application
	pieces layoutPieces

	// src is the proxy substrate fan-out source. nil-PaneSource is used
	// when no proxy is wired (tests; daemon startup with no bindings).
	src PaneSource

	// coordSrc, agentSrc and coordChan, agentChan track the channels
	// currently bound to the coord and agent StreamPanes so they can be
	// swapped on rail navigation in later stages.
	mu         sync.Mutex
	coordTask  string
	agentTask  string
	coordUnsub func()
	agentUnsub func()

	// closed is set after Close runs so subsequent Close calls are no-ops.
	closed bool
}

// BuildApp constructs the hera-view tview Application. It reads the
// orchestrator / role / binding state from db once at build time to
// populate the rail (Stage I wires live updates). If src is nil, a no-op
// PaneSource is used and panes render placeholder text.
//
// The returned *App owns the tview.Application, the StreamPane
// goroutines, and the proxy subscription handles. Callers MUST invoke
// Close to release them when the WebSocket session ends.
func BuildApp(database *db.DB, src PaneSource) (*App, error) {
	if database == nil {
		return nil, fmt.Errorf("view.BuildApp: nil db")
	}
	if src == nil {
		src = nilPaneSource{}
	}

	// Stage F panes start with no source channel; they'll be wired on
	// the first rail selection (live or initial). Until then they render
	// a placeholder.
	coordPane := NewStreamPane(nil)
	coordPane.SetPlaceholder("(no coord selected)")
	agentPane := NewStreamPane(nil)
	agentPane.SetPlaceholder("(no agent selected)")

	pieces := buildLayout(coordPane, agentPane)

	tApp := tview.NewApplication()
	tApp.SetRoot(pieces.root, true)
	tApp.EnableMouse(false)

	a := &App{
		app:    tApp,
		pieces: pieces,
		src:    src,
	}

	if err := a.populateRail(database); err != nil {
		return nil, fmt.Errorf("view.BuildApp: populate rail: %w", err)
	}

	// On a fresh open with at least one live binding, bind the panes to
	// the first agent's project's coord + the first agent. If no live
	// bindings exist, the panes keep their placeholders.
	a.bindInitialSelection(database)

	return a, nil
}

// Application returns the wrapped tview Application. Stage E's WebSocket
// server attaches its custom screen and runs the event loop on this
// handle.
func (a *App) Application() *tview.Application {
	return a.app
}

// Close stops the StreamPane consumer goroutines and cancels every open
// proxy subscription. Idempotent.
func (a *App) Close() {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	coordUnsub := a.coordUnsub
	agentUnsub := a.agentUnsub
	a.coordUnsub = nil
	a.agentUnsub = nil
	a.mu.Unlock()

	if coordUnsub != nil {
		coordUnsub()
	}
	if agentUnsub != nil {
		agentUnsub()
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

// bindInitialSelection picks the first live agent (worker role) on the
// first non-archived orchestrator that has one, and binds the coord and
// agent panes to the appropriate ring buffers. If no live agent exists,
// the panes keep their placeholders.
func (a *App) bindInitialSelection(database *db.DB) {
	ctx := context.Background()
	orchs, err := database.Orchestrators.List(ctx)
	if err != nil {
		return
	}
	for _, orch := range orchs {
		roles, err := database.Roles.ListByOrchestrator(ctx, orch.ID)
		if err != nil {
			continue
		}
		var coordTask string
		var firstAgentTask string
		for _, role := range roles {
			bnd, err := database.Bindings.GetLiveByRole(ctx, role.ID)
			if err != nil || bnd == nil {
				continue
			}
			if role.Kind == db.KindCoordinator {
				coordTask = bnd.ArgusTaskID
			} else if firstAgentTask == "" {
				firstAgentTask = bnd.ArgusTaskID
			}
		}
		if firstAgentTask == "" {
			continue
		}
		a.bindPanes(coordTask, firstAgentTask)
		return
	}
}

// bindPanes attaches the coord and agent panes to the proxy
// subscriptions for the given argus task IDs. An empty taskID detaches
// the corresponding pane (placeholder rendered). Old subscriptions are
// released.
func (a *App) bindPanes(coordTask, agentTask string) {
	a.mu.Lock()
	prevCoordUnsub := a.coordUnsub
	prevAgentUnsub := a.agentUnsub
	a.coordUnsub = nil
	a.agentUnsub = nil
	a.coordTask = coordTask
	a.agentTask = agentTask
	a.mu.Unlock()

	if prevCoordUnsub != nil {
		prevCoordUnsub()
	}
	if prevAgentUnsub != nil {
		prevAgentUnsub()
	}

	if coordTask != "" {
		snap, ch, unsub := a.src.SubscribeTask(coordTask)
		a.pieces.coord.replaceSource(snap, ch)
		a.mu.Lock()
		a.coordUnsub = unsub
		a.mu.Unlock()
	}
	if agentTask != "" {
		snap, ch, unsub := a.src.SubscribeTask(agentTask)
		a.pieces.agent.replaceSource(snap, ch)
		a.mu.Lock()
		a.agentUnsub = unsub
		a.mu.Unlock()
	}
}
