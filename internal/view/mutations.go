package view

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/anutron/hera/internal/db"
	"github.com/anutron/hera/internal/view/ops"
)

// mutationService is the subset of *ops.Service the mutation bridge
// uses. Defining an interface rather than depending on *ops.Service
// directly lets tests inject a fake without standing up ops's full
// dependency tree.
type mutationService interface {
	NewOrchestrator(ctx context.Context, in ops.NewOrchestratorInput) (*ops.CreatedTask, error)
	RenameOrchestrator(ctx context.Context, id int64, newName string) error
	RenameRole(ctx context.Context, id int64, newName string) error
	DeleteOrchestrator(ctx context.Context, id int64) error
	DeleteRole(ctx context.Context, id int64) error
	ToggleArchiveOrchestrator(ctx context.Context, id int64) error
	ToggleArchiveRole(ctx context.Context, id int64) error

	// Stage P extended keyset.
	ListCompletedAgents(ctx context.Context) ([]ops.CompletedAgent, error)
	PruneCompleted(ctx context.Context, agents []ops.CompletedAgent) (int, error)
	AdvanceStatus(ctx context.Context, roleID int64) (string, error)
	RevertStatus(ctx context.Context, roleID int64) (string, error)
	OpenPR(ctx context.Context, roleID int64) (string, error)

	// OpenPRFromWorktree opens a PR straight from a worktree path, with no
	// hera role/binding to resolve. Backs `^p` on a freelancer (an unmanaged
	// argus task, RoleID 0), whose worktree is the argus task's own.
	OpenPRFromWorktree(ctx context.Context, worktreePath string) (string, error)

	// ResurrectOrchestrator unarchives a dormant orchestrator + coord role and
	// spawns a fresh argus task that rebinds via hera_join. Backs the
	// resurrect-on-Enter flow (Enter against an archived coord with the Archive
	// section visible).
	ResurrectOrchestrator(ctx context.Context, coordRoleID int64) (*ops.CreatedTask, error)
}

// listAllState is the subset of *ops.ListAllState the bridge uses to
// drive the `l` toggle. Visible reports the current state; Toggle
// flips it and returns the new state.
type listAllState interface {
	Visible() bool
	Toggle() bool
}

// modalAPI is the surface the bridge needs to drive input / confirm /
// help / error modals against the surrounding tview Application.
// Production wires modalAPI to the App's tview.Pages; tests substitute
// a fake that fires callbacks synchronously.
type modalAPI interface {
	// ShowInput opens a single-line input modal. onSubmit is invoked
	// with the entered text when the operator confirms; onCancel runs
	// on cancel. Either callback may be nil.
	ShowInput(title, label, initial string, onSubmit func(value string), onCancel func())
	// ShowForm2 opens a two-field input modal. onSubmit is invoked with both
	// trimmed values when the operator confirms; onCancel runs on cancel.
	// Used by the new-project flow (name required, mission optional). Either
	// callback may be nil.
	ShowForm2(title, label1, initial1, label2, initial2 string, onSubmit func(v1, v2 string), onCancel func())
	// ShowConfirm opens a y/N confirmation modal. onYes runs when the
	// operator picks Yes; onNo runs on No or cancel. Either may be nil.
	ShowConfirm(title, message string, onYes func(), onNo func())
	// ShowError surfaces a string in an error modal. Dismissed by any
	// key.
	ShowError(message string)
}

// selectionKind tells the bridge whether the highlighted rail row is
// an orchestrator header or a role.
type selectionKind int

const (
	selNone selectionKind = iota
	selOrchestrator
	selRole
)

// railSelection is the bridge's view of the currently-highlighted
// rail row. Empty Kind means "nothing addressable selected" —
// rename/delete/archive silently no-op in that case.
type railSelection struct {
	Kind           selectionKind
	OrchestratorID int64
	RoleID         int64
	Name           string
	RoleKind       string
	Archived       bool

	// CoordRoleID is the orchestrator's coord role id, carried on orchestrator
	// rows so the resurrect-on-Enter flow can target the coord role when the
	// operator presses Enter on an archived root coordinator (whose selection
	// is the orchestrator header, not a role row). Zero for non-orchestrator
	// selections or when the orchestrator has no coord role.
	CoordRoleID int64

	// ArgusTaskID is the selected role's bound argus task id (empty for
	// orchestrator rows or roles with no live binding). Carried so the
	// extended keyset can act on the selection's task.
	ArgusTaskID string

	// WorktreePath is the argus task's worktree path, carried on freelance
	// rows (RoleKind "freelance", RoleID 0) so `^p` can open a PR straight
	// from the freelancer's worktree — hera has no binding to resolve it
	// from. Empty for managed roles (their worktree is resolved via the
	// live binding by the ops layer) and orchestrator rows.
	WorktreePath string

	// ChildCount is the number of child agents the selection has (live
	// roles under an orchestrator). Drives the `^d` destructive-delete
	// warning when the target has children. Zero for leaf rows.
	ChildCount int
}

// railSelector reads the rail's current selection. Tests substitute a
// fake that returns a fixed value; production wires *App.
type railSelector interface {
	CurrentRailSelection() railSelection
}

// repopulator triggers a rail repopulate after a mutation completes
// or `l` flips the archive visibility. The DAO broadcaster already
// refreshes the rail on writes, but `l` does not write the DB and
// some `n` writes happen on a background task that hasn't completed
// yet, so the bridge pokes a refresh directly.
type repopulator interface {
	RepopulateRail()
}

// helpFrameSender sends argus's help control frame so argus pops its help
// overlay rendered from hera's pushed hotkey dictionary (D12). The bridge's
// OnHelp routes here instead of opening an in-surface modal. nil makes OnHelp
// a no-op (tests / daemon startup without a session conn).
type helpFrameSender interface {
	SendHelp() error
}

// mutationBridge implements MutationHandler by routing each rail
// mutation key to modals + ops.Service. Construction wires it to the
// surrounding App via the small interfaces above; tests inject
// fakes for each.
type mutationBridge struct {
	ctx     context.Context
	modals  modalAPI
	sel     railSelector
	svc     mutationService
	listAll listAllState
	repop   repopulator
	help    helpFrameSender
	log     *slog.Logger
}

// newMutationBridge constructs the bridge. log defaults to
// slog.Default() when nil. help may be nil (OnHelp then no-ops).
func newMutationBridge(
	ctx context.Context,
	modals modalAPI,
	sel railSelector,
	svc mutationService,
	listAll listAllState,
	repop repopulator,
	help helpFrameSender,
	log *slog.Logger,
) *mutationBridge {
	if log == nil {
		log = slog.Default()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return &mutationBridge{
		ctx:     ctx,
		modals:  modals,
		sel:     sel,
		svc:     svc,
		listAll: listAll,
		repop:   repop,
		help:    help,
		log:     log,
	}
}

// OnNew prompts for an orchestrator name (required) and an optional coord
// mission, then spawns the bootstrap argus task via ops.NewOrchestrator. Both
// fields are passed through so the spawned prompt carries the mission. Empty-
// name submissions no-op.
func (b *mutationBridge) OnNew() {
	b.modals.ShowForm2("New project", "Name", "", "Mission (optional)", "", func(name, mission string) {
		if name == "" {
			return
		}
		if _, err := b.svc.NewOrchestrator(b.ctx, ops.NewOrchestratorInput{Name: name, Mission: mission}); err != nil {
			b.modals.ShowError(err.Error())
			return
		}
		b.refresh()
	}, nil)
}

// OnRename prompts for a new name on the currently-selected row and
// dispatches to RenameOrchestrator or RenameRole based on kind. No-op
// when nothing addressable is selected.
func (b *mutationBridge) OnRename() {
	sel := b.sel.CurrentRailSelection()
	switch sel.Kind {
	case selOrchestrator:
		b.modals.ShowInput(
			fmt.Sprintf("Rename project %q", sel.Name),
			"New name",
			sel.Name,
			func(name string) {
				if name == "" || name == sel.Name {
					return
				}
				if err := b.svc.RenameOrchestrator(b.ctx, sel.OrchestratorID, name); err != nil {
					b.modals.ShowError(err.Error())
					return
				}
				b.refresh()
			},
			nil,
		)
	case selRole:
		b.modals.ShowInput(
			fmt.Sprintf("Rename role %q", sel.Name),
			"New name",
			sel.Name,
			func(name string) {
				if name == "" || name == sel.Name {
					return
				}
				if err := b.svc.RenameRole(b.ctx, sel.RoleID, name); err != nil {
					b.modals.ShowError(err.Error())
					return
				}
				b.refresh()
			},
			nil,
		)
	}
}

// OnDelete confirms then dispatches to DeleteOrchestrator or DeleteRole.
// No-op when nothing addressable is selected.
func (b *mutationBridge) OnDelete() {
	sel := b.sel.CurrentRailSelection()
	var (
		title   string
		message string
		do      func() error
	)
	switch sel.Kind {
	case selOrchestrator:
		title = fmt.Sprintf("Delete project %q?", sel.Name)
		message = fmt.Sprintf(
			"DESTRUCTIVE: this destroys %q and all %d of its child agents — "+
				"each agent's argus task, git worktree, and branch are removed. "+
				"This cannot be undone. Continue? (y/N)",
			sel.Name, sel.ChildCount,
		)
		do = func() error { return b.svc.DeleteOrchestrator(b.ctx, sel.OrchestratorID) }
	case selRole:
		warn := ""
		if sel.ChildCount > 0 {
			warn = fmt.Sprintf(" WARNING: %q has %d child agent(s) that will also be destroyed.", sel.Name, sel.ChildCount)
		}
		title = fmt.Sprintf("Delete %q?", sel.Name)
		message = fmt.Sprintf(
			"DESTRUCTIVE: this destroys %q — its argus task, git worktree, and branch are removed. "+
				"This cannot be undone.%s Continue? (y/N)",
			sel.Name, warn,
		)
		do = func() error { return b.svc.DeleteRole(b.ctx, sel.RoleID) }
	default:
		return
	}
	b.modals.ShowConfirm(title, message, func() {
		if err := do(); err != nil {
			b.modals.ShowError(err.Error())
			return
		}
		b.refresh()
	}, nil)
}

// OnArchive toggles the archived state of the selected row. No modal
// — archive is non-destructive (worktree survives), so a single key
// is enough.
func (b *mutationBridge) OnArchive() {
	sel := b.sel.CurrentRailSelection()
	var err error
	switch sel.Kind {
	case selOrchestrator:
		err = b.svc.ToggleArchiveOrchestrator(b.ctx, sel.OrchestratorID)
	case selRole:
		err = b.svc.ToggleArchiveRole(b.ctx, sel.RoleID)
	default:
		return
	}
	if err != nil {
		b.modals.ShowError(err.Error())
		return
	}
	b.refresh()
}

// OnResurrect handles Enter against an archived coord row when the Archive
// section is visible: it confirms ("Resurrect <project>?") and, on confirm,
// calls the resurrect op (unarchive orchestrator + coord role, spawn a fresh
// argus task that rebinds via hera_join). It returns true when it owns the
// Enter (an archived coord with Archive visible — modal shown), so the router
// must NOT fall through to pane-entry; false otherwise (the router then runs
// the normal OnRailSelectEnter pane-entry path).
//
// Gate: the Archive section must be visible (listAll.Visible()) — archived
// root coordinators live in the top-level Archive section, which `l` reveals.
// A live (non-archived) coord, an archived worker, or any non-coord selection
// is not a resurrect target.
func (b *mutationBridge) OnResurrect() bool {
	if b.listAll == nil || !b.listAll.Visible() {
		return false
	}
	sel := b.sel.CurrentRailSelection()
	if !sel.Archived {
		return false
	}
	var coordRoleID int64
	switch sel.Kind {
	case selOrchestrator:
		// Archived root coordinator: target its coord role.
		if sel.CoordRoleID == 0 {
			return false
		}
		coordRoleID = sel.CoordRoleID
	case selRole:
		// Only a coordinator role resurrects; archived workers do not.
		if sel.RoleKind != string(db.KindCoordinator) {
			return false
		}
		coordRoleID = sel.RoleID
	default:
		return false
	}

	b.modals.ShowConfirm(
		fmt.Sprintf("Resurrect %q?", sel.Name),
		fmt.Sprintf("Resurrect %q — unarchive its coordinator and spawn a fresh argus task that rebinds via hera_join? (y/N)", sel.Name),
		func() {
			if _, err := b.svc.ResurrectOrchestrator(b.ctx, coordRoleID); err != nil {
				b.modals.ShowError(err.Error())
				return
			}
			b.refresh()
		},
		nil,
	)
	return true
}

// OnListAll toggles archive-section visibility. No DB write, no argus
// call — pure view state per design.md D5.
func (b *mutationBridge) OnListAll() {
	if b.listAll != nil {
		b.listAll.Toggle()
	}
	b.refresh()
}

// OnHelp sends argus's help control frame so argus pops its help overlay
// rendered from hera's pushed hotkey dictionary (D12). Hera renders no
// in-surface help modal. No DB read or write. No-op when no help sender is
// wired (tests / daemon startup without a session conn).
func (b *mutationBridge) OnHelp() {
	if b.help != nil {
		_ = b.help.SendHelp()
	}
}

// OnPrune confirms then prunes all completed agents fleet-wide (D15 `^r`).
// It first lists the completed agents so the confirmation names exactly what
// disappears; no destruction occurs unless the operator confirms. With no
// completed agents it surfaces an informational modal and does nothing.
func (b *mutationBridge) OnPrune() {
	agents, err := b.svc.ListCompletedAgents(b.ctx)
	if err != nil {
		b.modals.ShowError(err.Error())
		return
	}
	if len(agents) == 0 {
		b.modals.ShowError("No completed agents to prune.")
		return
	}
	var names []string
	for _, a := range agents {
		names = append(names, a.Name)
	}
	message := fmt.Sprintf(
		"DESTRUCTIVE: prune %d completed agent(s) — %s — removing each task, worktree, and branch. "+
			"This cannot be undone. Continue? (y/N)",
		len(agents), strings.Join(names, ", "),
	)
	b.modals.ShowConfirm("Prune completed?", message, func() {
		if _, err := b.svc.PruneCompleted(b.ctx, agents); err != nil {
			b.modals.ShowError(err.Error())
			return
		}
		b.refresh()
	}, nil)
}

// OnOpenPR opens a pull request for the selected agent's, coordinator's, OR
// freelancer's task (D15 `^p`). It resolves the worktree the PR is opened from:
//   - an agent/worker row → that role's live binding's worktree (via OpenPR);
//   - a coordinator selection (root orchestrator header or a sub-coordinator
//     role) → the coordinator's bound argus task's worktree (via OpenPR);
//   - a freelancer (a freelance row: no hera role/RoleID, but carrying the
//     argus task's worktree path) → that worktree directly (via
//     OpenPRFromWorktree) — the same way argus opens a PR for the task.
//
// Combined with the header-only rendering of worker-less orchestrators, `^p`
// on a coord-only project opens a PR on the coord. Confirmed before the
// external action fires. No-op when nothing addressable resolves.
func (b *mutationBridge) OnOpenPR() {
	sel := b.sel.CurrentRailSelection()

	// Freelancer: no hera binding, but argus knows the task's worktree. Open
	// the PR straight from that path.
	if sel.Kind == selRole && sel.RoleKind == string(db.KindFreelance) {
		if sel.WorktreePath == "" {
			return
		}
		wt := sel.WorktreePath
		b.modals.ShowConfirm(
			fmt.Sprintf("Open PR for %q?", sel.Name),
			fmt.Sprintf("Open a pull request from %q's argus-task worktree via the host git flow? (y/N)", sel.Name),
			func() {
				if _, err := b.svc.OpenPRFromWorktree(b.ctx, wt); err != nil {
					b.modals.ShowError(err.Error())
					return
				}
			},
			nil,
		)
		return
	}

	roleID := b.openPRRoleID(sel)
	if roleID == 0 {
		return
	}
	b.modals.ShowConfirm(
		fmt.Sprintf("Open PR for %q?", sel.Name),
		fmt.Sprintf("Open a pull request from %q's worktree via the host git flow? (y/N)", sel.Name),
		func() {
			if _, err := b.svc.OpenPR(b.ctx, roleID); err != nil {
				b.modals.ShowError(err.Error())
				return
			}
		},
		nil,
	)
}

// openPRRoleID resolves which role's binding the `^p` PR opens from:
//   - an orchestrator header (root coordinator) → its coord role (CoordRoleID)
//   - a role row → that role (a worker, or a sub-coordinator — both have a
//     live binding whose worktree the PR opens from)
//
// Returns 0 when nothing addressable is selected (the bridge then no-ops). A
// freelancer carries RoleID 0 and no hera binding, so it resolves to 0 too.
func (b *mutationBridge) openPRRoleID(sel railSelection) int64 {
	switch sel.Kind {
	case selOrchestrator:
		return sel.CoordRoleID
	case selRole:
		return sel.RoleID
	}
	return 0
}

// OnStatusAdvance steps the selected agent's argus status forward (`s`).
// No modal — status stepping is reversible (`S`). No-op for non-role rows.
func (b *mutationBridge) OnStatusAdvance() {
	b.stepStatus(true)
}

// OnStatusRevert steps the selected agent's argus status backward (`S`).
func (b *mutationBridge) OnStatusRevert() {
	b.stepStatus(false)
}

func (b *mutationBridge) stepStatus(advance bool) {
	sel := b.sel.CurrentRailSelection()
	if sel.Kind != selRole {
		return
	}
	var err error
	if advance {
		_, err = b.svc.AdvanceStatus(b.ctx, sel.RoleID)
	} else {
		_, err = b.svc.RevertStatus(b.ctx, sel.RoleID)
	}
	if err != nil {
		b.modals.ShowError(err.Error())
		return
	}
	b.refresh()
}

func (b *mutationBridge) refresh() {
	if b.repop != nil {
		b.repop.RepopulateRail()
	}
}
