package view

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"

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

	// Explicit archive verbs. The VIEW decides the direction from the
	// selection's EFFECTIVE rendered archived state (what the operator
	// sees) and calls the matching verb — never a flag-inspecting toggle,
	// which on a mixed-flag row (hera-active + argus-archived) would move
	// the row OPPOSITE to what the operator sees.
	ArchiveOrchestrator(ctx context.Context, id int64) error
	UnarchiveOrchestrator(ctx context.Context, id int64) error
	ArchiveRole(ctx context.Context, id int64) error
	UnarchiveRole(ctx context.Context, id int64) error

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

	// Task-direct verbs for freelance rows (unmanaged argus tasks with no hera
	// role or binding): both bypass the hera-binding lookup and address the
	// argus task by id. ToggleArchiveTask backs `a`; archived is the task's
	// current argus archived state per the rail's state cache. StepTaskStatus
	// backs `s`/`S` (advance=true steps toward complete).
	ToggleArchiveTask(ctx context.Context, taskID string, archived bool) error
	StepTaskStatus(ctx context.Context, taskID string, advance bool) (string, error)
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
// rail row. Empty Kind means "nothing addressable selected" — mutation
// keys then surface a short "not applicable" notice (never a silent
// no-op).
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

	// ArgusArchived is the argus-side archived state of the selection's task
	// (per the rail's argus state cache). Carried on every role row so `a`
	// can compute the EFFECTIVE archived state the rail displays — on a
	// freelance row it is the whole signal (no hera role row, so Archived
	// stays false), and on a managed row it catches the mixed-flag state
	// (hera-active + argus-archived) the hera flag alone would miss.
	ArgusArchived bool

	// Dead reports that the argus task RECORD no longer exists (404 /
	// pruned); status alone never sets it. A dead row DISPLAYS as archived
	// (roleArchived), so `a` must treat it as an unarchive target, never
	// stamp a fresh archive.
	Dead bool

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

// archiveVisibilitySetter is optionally implemented by the repopulator
// (production *App) so the `l` toggle can sync the rail's archive
// visibility before the refresh repopulates. Without this sync the toggle
// only flipped ops.ListAllState — which populateRail never reads — so the
// Archive section never actually revealed.
type archiveVisibilitySetter interface {
	SetShowArchived(bool)
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
//
// CONCURRENCY CONTRACT: every handler is invoked synchronously on the
// tview event loop and MUST return without blocking. Selection reads
// happen synchronously (UI state, on-loop); everything that blocks —
// svc calls AND every modal/refresh helper (each bounces through
// tview's QueueUpdateDraw, which deadlocks the loop when called from
// it) — is handed to a goroutine via goUI / mutate.
type mutationBridge struct {
	ctx     context.Context
	modals  modalAPI
	sel     railSelector
	svc     mutationService
	listAll listAllState
	repop   repopulator
	help    helpFrameSender
	log     *slog.Logger

	// inFlight guards the blocking phase of a mutation: while a svc call is
	// executing on a background goroutine, a second mutation key no-ops with
	// visible feedback instead of firing a concurrent conflicting operation
	// on the same row.
	inFlight atomic.Bool

	// wg tracks every goroutine the bridge spawns (modal opens, mutation
	// execution) so tests can deterministically wait for the async work to
	// settle (waitIdle). Production never waits on it.
	wg sync.WaitGroup
}

// waitIdle blocks until every goroutine the bridge has spawned so far has
// finished. Test synchronization only — the async hand-off is the point of
// the bridge (handlers MUST return to the event loop before their blocking
// work runs), so tests use this instead of sleeping.
func (b *mutationBridge) waitIdle() { b.wg.Wait() }

// goUI runs fn on its own goroutine so the on-loop caller never blocks.
//
// WHY THIS EXISTS: the bridge's handlers run synchronously on the tview
// event loop (KeyRouter.HandleKey), and tview v0.42's QueueUpdate BLOCKS
// until the queued func has executed on that same loop. Every UI helper the
// bridge touches — the modal openers and RepopulateRail — bounces through
// QueueUpdateDraw, so calling one directly from a handler deadlocks the loop
// on itself (the live-repro freeze: `s`/`a` on a rail row bricked the
// session). From a goroutine the bounce merely blocks the goroutine until
// the loop services it, which is the safe direction.
func (b *mutationBridge) goUI(fn func()) {
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		fn()
	}()
}

// mutate executes op — a blocking svc call (argus HTTP, DB cascade, worktree
// removal) — off the event loop, then bounces the UI follow-up (error modal
// on failure, rail refresh on success when refreshAfter) from that goroutine.
//
// The in-flight flag is claimed on the CALLER's goroutine, so by the time
// the handler returns a second mutation key deterministically sees "busy"
// and no-ops with visible feedback instead of firing a concurrent
// conflicting op on the same row. The flag spans only the blocking phase;
// modal opens (goUI) don't hold it — while a modal is up the ModalGate
// already keeps mutation keys from reaching the bridge.
func (b *mutationBridge) mutate(name string, refreshAfter bool, op func() error) {
	if !b.inFlight.CompareAndSwap(false, true) {
		b.log.Warn("view.mutations: mutation already in flight; dropping", "op", name)
		b.goUI(func() {
			b.modals.ShowError(fmt.Sprintf("%s: another operation is in flight — try again in a moment", name))
		})
		return
	}
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		defer b.inFlight.Store(false)
		if err := op(); err != nil {
			b.modals.ShowError(err.Error())
			return
		}
		if refreshAfter {
			b.refresh()
		}
	}()
}

// notApplicable surfaces short feedback for a mutation key pressed on a
// selection it cannot act on. NEVER a silent no-op: silence on a live
// surface reads as a freeze and sends the operator hunting (the reported
// symptom behind the addressability gaps).
func (b *mutationBridge) notApplicable(msg string) {
	b.goUI(func() { b.modals.ShowError(msg) })
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
// name submissions no-op. The modal opens via goUI (the open bounces through
// QueueUpdateDraw); the submit callback runs back on the event loop, so the
// spawn itself goes through mutate.
func (b *mutationBridge) OnNew() {
	b.goUI(func() {
		b.modals.ShowForm2("New project", "Name", "", "Mission (optional)", "", func(name, mission string) {
			if name == "" {
				return
			}
			b.mutate("new project", true, func() error {
				_, err := b.svc.NewOrchestrator(b.ctx, ops.NewOrchestratorInput{Name: name, Mission: mission})
				return err
			})
		}, nil)
	})
}

// OnRename prompts for a new name on the currently-selected row and
// dispatches to RenameOrchestrator or RenameRole based on kind. The
// selection is captured synchronously (it reads UI state, which must stay
// on-loop); the modal open and the rename itself run off-loop. Freelancers
// and non-addressable rows get visible feedback, never a silent no-op.
func (b *mutationBridge) OnRename() {
	sel := b.sel.CurrentRailSelection()
	switch sel.Kind {
	case selOrchestrator:
		b.goUI(func() {
			b.modals.ShowInput(
				fmt.Sprintf("Rename project %q", sel.Name),
				"New name",
				sel.Name,
				func(name string) {
					if name == "" || name == sel.Name {
						return
					}
					b.mutate("rename", true, func() error {
						return b.svc.RenameOrchestrator(b.ctx, sel.OrchestratorID, name)
					})
				},
				nil,
			)
		})
	case selRole:
		if sel.RoleKind == string(db.KindFreelance) {
			b.notApplicable("r: a freelancer is an unmanaged argus task — there is no hera role to rename")
			return
		}
		b.goUI(func() {
			b.modals.ShowInput(
				fmt.Sprintf("Rename role %q", sel.Name),
				"New name",
				sel.Name,
				func(name string) {
					if name == "" || name == sel.Name {
						return
					}
					b.mutate("rename", true, func() error {
						return b.svc.RenameRole(b.ctx, sel.RoleID, name)
					})
				},
				nil,
			)
		})
	default:
		b.notApplicable("r: not applicable to this row")
	}
}

// OnDelete confirms then dispatches to DeleteOrchestrator or DeleteRole.
// The confirm opens off-loop; the destructive cascade runs through mutate
// (the modal's Yes callback fires back on the event loop). Freelancers and
// non-addressable rows get visible feedback.
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
		if sel.RoleKind == string(db.KindFreelance) {
			b.notApplicable("^d: a freelancer is an unmanaged argus task — delete it from argus instead")
			return
		}
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
		b.notApplicable("^d: not applicable to this row")
		return
	}
	b.goUI(func() {
		b.modals.ShowConfirm(title, message, func() {
			b.mutate("delete", true, do)
		}, nil)
	})
}

// OnArchive toggles the archived state of the selected row. No modal
// — archive is non-destructive (worktree survives), so a single key
// is enough. A freelance row (unmanaged argus task, no hera role) is
// addressed directly by its argus task id; non-addressable rows get
// visible feedback. The argus call runs off-loop via mutate.
//
// The toggle DIRECTION follows the row's EFFECTIVE rendered archived
// state — the same predicate the rail uses to bucket the row into an
// Archive expando — and dispatches an explicit verb. Deriving the
// direction from a single backing flag (the old ToggleArchiveRole read
// role.Archived alone) moved a mixed-flag row (hera-active +
// argus-archived) OPPOSITE to what the operator sees: an Archive-expando
// row got a fresh archived_at instead of clearing both sides.
func (b *mutationBridge) OnArchive() {
	sel := b.sel.CurrentRailSelection()
	switch sel.Kind {
	case selOrchestrator:
		// An orchestrator header displays archived from its own flag.
		if sel.Archived {
			b.mutate("archive", true, func() error {
				return b.svc.UnarchiveOrchestrator(b.ctx, sel.OrchestratorID)
			})
		} else {
			b.mutate("archive", true, func() error {
				return b.svc.ArchiveOrchestrator(b.ctx, sel.OrchestratorID)
			})
		}
	case selRole:
		if sel.RoleKind == string(db.KindFreelance) {
			if sel.ArgusTaskID == "" {
				b.notApplicable("a: this freelancer has no argus task id to archive")
				return
			}
			b.mutate("archive", true, func() error {
				return b.svc.ToggleArchiveTask(b.ctx, sel.ArgusTaskID, sel.ArgusArchived)
			})
			return
		}
		// Effective rendered state, mirroring rail_list.roleArchived: the
		// row sits in an Archive expando when EITHER side is archived or
		// the binding is dead.
		if sel.Archived || sel.ArgusArchived || sel.Dead {
			b.mutate("archive", true, func() error {
				return b.svc.UnarchiveRole(b.ctx, sel.RoleID)
			})
		} else {
			b.mutate("archive", true, func() error {
				return b.svc.ArchiveRole(b.ctx, sel.RoleID)
			})
		}
	default:
		b.notApplicable("a: not applicable to this row")
	}
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

	b.goUI(func() {
		b.modals.ShowConfirm(
			fmt.Sprintf("Resurrect %q?", sel.Name),
			fmt.Sprintf("Resurrect %q — unarchive its coordinator and spawn a fresh argus task that rebinds via hera_join? (y/N)", sel.Name),
			func() {
				b.mutate("resurrect", true, func() error {
					_, err := b.svc.ResurrectOrchestrator(b.ctx, coordRoleID)
					return err
				})
			},
			nil,
		)
	})
	return true
}

// OnListAll toggles archive-section visibility. No DB write, no argus
// call — pure view state per design.md D5. The toggle itself is cheap
// in-memory state and runs synchronously; the new visibility is synced
// into the repopulator (production *App reads it in populateRail) and the
// refresh bounces off-loop.
func (b *mutationBridge) OnListAll() {
	visible := false
	if b.listAll != nil {
		visible = b.listAll.Toggle()
	}
	if s, ok := b.repop.(archiveVisibilitySetter); ok {
		s.SetShowArchived(visible)
	}
	b.goUI(b.refresh)
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
// The listing is itself a blocking argus sweep, so the WHOLE pre-confirm
// phase runs off-loop; the destructive prune then goes through mutate from
// the modal's Yes callback.
func (b *mutationBridge) OnPrune() {
	b.goUI(func() {
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
			b.mutate("prune", true, func() error {
				_, err := b.svc.PruneCompleted(b.ctx, agents)
				return err
			})
		}, nil)
	})
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
			b.notApplicable("^p: this freelancer has no worktree to open a PR from")
			return
		}
		wt := sel.WorktreePath
		b.goUI(func() {
			b.modals.ShowConfirm(
				fmt.Sprintf("Open PR for %q?", sel.Name),
				fmt.Sprintf("Open a pull request from %q's argus-task worktree via the host git flow? (y/N)", sel.Name),
				func() {
					// No refresh: opening a PR mutates nothing the rail renders.
					b.mutate("open PR", false, func() error {
						_, err := b.svc.OpenPRFromWorktree(b.ctx, wt)
						return err
					})
				},
				nil,
			)
		})
		return
	}

	roleID := b.openPRRoleID(sel)
	if roleID == 0 {
		b.notApplicable("^p: no worktree resolvable from this row")
		return
	}
	b.goUI(func() {
		b.modals.ShowConfirm(
			fmt.Sprintf("Open PR for %q?", sel.Name),
			fmt.Sprintf("Open a pull request from %q's worktree via the host git flow? (y/N)", sel.Name),
			func() {
				b.mutate("open PR", false, func() error {
					_, err := b.svc.OpenPR(b.ctx, roleID)
					return err
				})
			},
			nil,
		)
	})
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

// OnStatusAdvance steps the selected row's argus status forward (`s`).
// No modal — status stepping is reversible (`S`).
func (b *mutationBridge) OnStatusAdvance() {
	b.stepStatus(true)
}

// OnStatusRevert steps the selected row's argus status backward (`S`).
func (b *mutationBridge) OnStatusRevert() {
	b.stepStatus(false)
}

// stepStatus routes `s`/`S` by selection kind, mirroring how `a` resolves
// its target:
//   - worker / sub-coordinator role → the role's live binding's task
//     (AdvanceStatus / RevertStatus);
//   - freelance row → the argus task directly by id (StepTaskStatus — a
//     freelancer has no hera binding to resolve);
//   - orchestrator header → the orchestrator's coord role's task;
//   - anything else → visible feedback, never silence.
//
// The argus round-trip runs off-loop via mutate.
func (b *mutationBridge) stepStatus(advance bool) {
	sel := b.sel.CurrentRailSelection()
	verb := "s"
	if !advance {
		verb = "S"
	}
	stepRole := func(roleID int64) {
		b.mutate("status step", true, func() error {
			var err error
			if advance {
				_, err = b.svc.AdvanceStatus(b.ctx, roleID)
			} else {
				_, err = b.svc.RevertStatus(b.ctx, roleID)
			}
			return err
		})
	}
	switch sel.Kind {
	case selRole:
		if sel.RoleKind == string(db.KindFreelance) {
			if sel.ArgusTaskID == "" {
				b.notApplicable(verb + ": this freelancer has no argus task id to step")
				return
			}
			taskID := sel.ArgusTaskID
			b.mutate("status step", true, func() error {
				_, err := b.svc.StepTaskStatus(b.ctx, taskID, advance)
				return err
			})
			return
		}
		stepRole(sel.RoleID)
	case selOrchestrator:
		if sel.CoordRoleID == 0 {
			b.notApplicable(verb + ": this project has no coordinator role to step")
			return
		}
		stepRole(sel.CoordRoleID)
	default:
		b.notApplicable(verb + ": not applicable to this row")
	}
}

func (b *mutationBridge) refresh() {
	if b.repop != nil {
		b.repop.RepopulateRail()
	}
}
