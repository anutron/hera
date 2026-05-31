package view

import (
	"context"
	"fmt"
	"log/slog"

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

// OnNew prompts for an orchestrator name and spawns the bootstrap
// argus task via ops.NewOrchestrator. Empty submissions no-op.
func (b *mutationBridge) OnNew() {
	b.modals.ShowInput("New project", "Name", "", func(name string) {
		if name == "" {
			return
		}
		if _, err := b.svc.NewOrchestrator(b.ctx, ops.NewOrchestratorInput{Name: name}); err != nil {
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
			"This will end every binding under %q, archive every role, and remove every worktree. Continue? (y/N)",
			sel.Name,
		)
		do = func() error { return b.svc.DeleteOrchestrator(b.ctx, sel.OrchestratorID) }
	case selRole:
		title = fmt.Sprintf("Delete role %q?", sel.Name)
		message = fmt.Sprintf(
			"This will end %q's binding, archive the role, and remove its worktree. Continue? (y/N)",
			sel.Name,
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

func (b *mutationBridge) refresh() {
	if b.repop != nil {
		b.repop.RepopulateRail()
	}
}
