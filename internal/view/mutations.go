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

// NewCoordFormInput carries the validated field values from the new-coordinator
// form modal.
type NewCoordFormInput struct {
	Name    string
	Project string
	Branch  string
	Backend string
	Prompt  string
}

// mutationService is the subset of *ops.Service the mutation bridge
// uses. Defining an interface rather than depending on *ops.Service
// directly lets tests inject a fake without standing up ops's full
// dependency tree.
type mutationService interface {
	NewOrchestrator(ctx context.Context, in ops.NewOrchestratorInput) (*ops.NewOrchestratorResult, error)
	ListProjects(ctx context.Context) ([]string, error)
	ListBackends(ctx context.Context) ([]string, error)
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

	// Pin verbs (the `P` key). Pin sets pinned_at and clears archived_at
	// (mutually exclusive); the view dispatches the direction from the
	// selection's rendered pinned state.
	PinOrchestrator(ctx context.Context, id int64) error
	UnpinOrchestrator(ctx context.Context, id int64) error
	PinRole(ctx context.Context, id int64) error
	UnpinRole(ctx context.Context, id int64) error

	// Adopt verbs (the `J` key). ListActiveOrchestrators feeds the target
	// picker; AdoptTaskIntoOrchestrator creates the operator-side worker
	// binding for the selected freelancer (mirroring hera_join attach-mode).
	ListActiveOrchestrators(ctx context.Context) ([]*ops.Orchestrator, error)
	AdoptTaskIntoOrchestrator(ctx context.Context, in ops.AdoptInput) (*ops.AdoptResult, error)

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

	// ReattachAgent restarts the dead agent session for the given argus task.
	// Backs Enter-on-dead-session-agent (BUG-033): when the user presses Enter
	// on a row whose PTY session has ended, hera asks argus to re-spawn the
	// agent backend (e.g. claude --resume <last-session-id>). The proxy
	// subscription picks up the new output automatically via its reconnect loop.
	// Returns ops.ErrRestartNotSupported when the daemon lacks the endpoint.
	ReattachAgent(ctx context.Context, argusTaskID string) error

	// SpawnWorker handles the `w` rail key: creates an argus task in the
	// coordinator's argus_project, inserts a worker role and binding
	// programmatically, and returns the created role + task ids so the
	// bridge can auto-select the new row. Returns ErrValidation on empty
	// prompt; other errors surface as an error modal.
	SpawnWorker(ctx context.Context, in ops.SpawnWorkerInput) (*ops.SpawnWorkerResult, error)

	// Task-direct verbs for freelance rows (unmanaged argus tasks with no hera
	// role or binding): both bypass the hera-binding lookup and address the
	// argus task by id. ToggleArchiveTask backs `a`; archived is the task's
	// current argus archived state per the rail's state cache. StepTaskStatus
	// backs `s`/`S` (advance=true steps toward complete).
	ToggleArchiveTask(ctx context.Context, taskID string, archived bool) error
	StepTaskStatus(ctx context.Context, taskID string, advance bool) (string, error)

	// MarkRoleDone sets the hera role's thread_status to "done" and mirrors
	// it to argus task_meta (best-effort), without advancing the argus workflow
	// status. Backs the `s`→done→confirm-no path in hera-view: the operator
	// chose to mark the role done in hera without flipping the argus task to
	// :checked: (complete).
	MarkRoleDone(ctx context.Context, roleID int64) error
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
	// Used by the new-project flow (name required, prompt optional). Either
	// callback may be nil.
	ShowForm2(title, label1, initial1, label2, initial2 string, onSubmit func(v1, v2 string), onCancel func())
	// ShowNewCoordForm opens the five-field new-coordinator form modal.
	// projects and backends are loaded before the modal opens (from argus).
	// onSubmit fires with the form values when the operator confirms with a
	// non-empty name; onCancel fires on Esc or empty-name submit.
	// Either callback may be nil.
	ShowNewCoordForm(title string, projects, backends []string, onSubmit func(NewCoordFormInput), onCancel func())
	// ShowConfirm opens a y/N confirmation modal. onYes runs when the
	// operator picks Yes; onNo runs on No or cancel. Either may be nil.
	ShowConfirm(title, message string, onYes func(), onNo func())
	// ShowSelect opens a single-choice picker listing items. onSelect is
	// invoked with the chosen 0-based index when the operator confirms a
	// selection (Enter); onCancel runs on dismiss (Esc). Either callback may
	// be nil. Backs the `J` adopt orchestrator picker.
	ShowSelect(title, label string, items []string, onSelect func(idx int), onCancel func())
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

	// Pinned is the row's rendered pinned state. Carried on orchestrator and
	// role selections so `P` can dispatch the direction — unpin a pinned row,
	// pin an unpinned one. For managed rows this reflects hera's pinned_at DB
	// column; for freelance rows it reflects the rail's pinnedFreelance map
	// (persisted via railViewState — no DB role row exists for freelancers).
	Pinned bool

	// CoordRoleID is the coord role id of the orchestrator the selection
	// belongs to. Carried on:
	//   - orchestrator (header) rows — so resurrect-on-Enter can target the
	//     coord role when Enter lands on an archived root coordinator;
	//   - agent/worker role rows — so `w` (OnNewWorker) can resolve the
	//     coordinator to spawn the new worker under (delta scenario "w resolves
	//     an agent selection to its coordinator"); without this an agent-row
	//     `w` resolves CoordRoleID==0 and dies as not-applicable.
	// Zero when the orchestrator has no coord role, or for freelance rows.
	CoordRoleID int64

	// ChildOrchestratorID is set ONLY on a promoted sub-coordinator role row (a
	// worker whose own task coordinates a separate child orchestrator — a
	// multi-binding). It is the CHILD orchestrator's id. `w` (OnNewWorker) uses
	// it to spawn the new worker under the sub-coordinator itself (the child
	// orchestrator), with the sub-coord's OWN role as the coord — per delta D2
	// "a coordinator row (root OR a sub-coordinator role row) targets that
	// coordinator." Zero for root coordinators and plain leaf workers.
	ChildOrchestratorID int64

	// CoordTaskID / CoordArgusArchived carry the orchestrator header's
	// coord-pane binding and its argus-side archived bit. A displayed-active
	// header (Archived false) whose coord task is argus-archived is the
	// MIXED-COORD state: `a` then REPAIRS — unarchives the coord task directly
	// by id — instead of cascade-archiving the orchestrator the operator sees
	// as active. Empty/false for non-orchestrator selections.
	CoordTaskID        string
	CoordArgusArchived bool

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

	// HasDeadSession reports that the argus task record EXISTS but the PTY
	// session has ended (the task status is terminal). This is distinct from
	// Dead (record gone). A dead-session row is the reattach target (BUG-033):
	// Enter asks argus to restart the agent backend so the conversation can
	// resume from where it left off.
	HasDeadSession bool

	// WorktreePath is the argus task's worktree path, carried on freelance
	// rows (RoleKind "freelance", RoleID 0) so `^p` can open a PR straight
	// from the freelancer's worktree — hera has no binding to resolve it
	// from. Empty for managed roles (their worktree is resolved via the
	// live binding by the ops layer) and orchestrator rows.
	WorktreePath string

	// Project is the freelancer's argus repo, carried on freelance rows so
	// `J` adoption can record it as the new worker role's argus_project
	// (write-once, consistent with managed roles). Empty for managed roles
	// and orchestrator rows.
	Project string

	// ChildCount is the number of child agents the selection has (live
	// roles under an orchestrator). Drives the `^d` destructive-delete
	// warning when the target has children. Zero for leaf rows.
	ChildCount int

	// Status is the argus status of the selection's relevant task, carried
	// so stepStatus can compute the optimistic predicted status (BUG-032)
	// without a separate cache lookup. For role rows this is roleEntry.Status;
	// for orchestrator headers it is orchEntry.CoordStatus. Empty when the
	// task's state has not yet been observed (cold cache, no live binding).
	Status string
}

// railSelector reads the rail's current selection. Tests substitute a
// fake that returns a fixed value; production wires *App.
type railSelector interface {
	CurrentRailSelection() railSelection
}

// statusOptimizer applies and clears optimistic status overrides on the argus
// state cache. The bridge calls SetOptimistic at the start of a status-step
// goroutine so the rail reflects the predicted new status before the argus
// round-trip completes; ClearOptimistic reverts the override on write failure.
// nil is a safe no-op (tests that don't wire an optimizer work unchanged).
type statusOptimizer interface {
	SetOptimistic(taskID, status string)
	ClearOptimistic(taskID string)
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

// freelancePinner toggles the pinned state of a freelance task in the rail.
// Implemented by *railList (ToggleFreelancePin). Wired by the App so the
// mutation bridge can pin freelancers without needing direct access to the
// rail widget.
type freelancePinner interface {
	ToggleFreelancePin(argusTaskID string)
}

// rowSelector stashes a role id to auto-select on the NEXT broadcaster-driven
// rail repopulate. Because role/binding inserts trigger an async (~100ms) rail
// refresh, the new row does not exist at the instant SpawnWorker returns — an
// immediate select would silently no-op. QueueSelectRole defers the select to
// when the row is actually present (the App applies it at the end of its
// populateRail). Production wires this to *App; tests inject a fakeRowSelector.
// nil makes auto-select a no-op (the row is still visible; the operator can
// navigate to it manually).
type rowSelector interface {
	QueueSelectRole(id int64)
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
	ctx       context.Context
	modals    modalAPI
	sel       railSelector
	svc       mutationService
	listAll   listAllState
	repop     repopulator
	help      helpFrameSender
	rowSel    rowSelector
	fPinner   freelancePinner
	optimizer statusOptimizer // optional: nil is safe (no optimistic render)
	log       *slog.Logger

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

// OnNew opens the five-field new-coordinator form. The project and backend
// lists are fetched from argus before opening the modal; either failure
// surfaces an error modal and aborts the open (the form is never shown).
// An empty-name submission no-ops silently. The modal opens via goUI (the
// open bounces through QueueUpdateDraw); the submit callback runs back on
// the event loop, so the spawn itself goes through mutate.
func (b *mutationBridge) OnNew() {
	b.goUI(func() {
		projects, err := b.svc.ListProjects(b.ctx)
		if err != nil {
			b.modals.ShowError("new coordinator: could not load projects: " + err.Error())
			return
		}
		backends, err := b.svc.ListBackends(b.ctx)
		if err != nil {
			b.modals.ShowError("new coordinator: could not load backends: " + err.Error())
			return
		}
		if len(backends) == 0 {
			backends = []string{"claude"}
		}

		b.modals.ShowNewCoordForm("New coordinator", projects, backends, func(in NewCoordFormInput) {
			if strings.TrimSpace(in.Name) == "" {
				return
			}
			b.mutate("new coordinator", true, func() error {
				res, err := b.svc.NewOrchestrator(b.ctx, ops.NewOrchestratorInput{
					Name:    in.Name,
					Project: in.Project,
					Branch:  in.Branch,
					Backend: in.Backend,
					Prompt:  in.Prompt,
				})
				if err != nil {
					return err
				}
				// Auto-select the new coordinator row after the rail repopulates.
				// The binding exists now so the Coord pane binds immediately.
				if b.rowSel != nil && res != nil {
					b.rowSel.QueueSelectRole(res.RoleID)
				}
				return nil
			})
		}, nil)
	})
}

// OnNewWorker handles the `w` RAIL-focus-only key (D1). It:
//  1. Resolves the target coordinator from the current rail selection.
//  2. Opens a single-field input modal prompting for the worker's prompt.
//  3. On confirm with a non-empty prompt, runs SpawnWorker off the event loop.
//  4. On success, auto-selects the new worker row while keeping focus RAIL.
//
// Selection resolution (D2):
//   - Orchestrator header → that orchestrator's coord role (CoordRoleID).
//   - Sub-coordinator role row (a promoted worker whose own task coordinates a
//     child orchestrator) → that CHILD orchestrator, with this row's own role
//     as coord (D2: a sub-coordinator row targets itself).
//   - Leaf agent/worker role row → that agent's orchestrator (OrchestratorID),
//     using CoordRoleID carried on the selection.
//   - Freelance row, selNone, or any selection without a coordinator →
//     a dismissible "not applicable" notice, no spawn.
func (b *mutationBridge) OnNewWorker() {
	sel := b.sel.CurrentRailSelection()

	// Resolve target orchestrator ID and coordinator role id. The coordinator
	// NAME for the orientation prefix is NOT resolved here — the ops layer
	// sources it from the coord role it loads, so an agent-row selection still
	// yields a prefix naming the coordinator (not the agent).
	var orchID, coordRoleID int64

	switch sel.Kind {
	case selOrchestrator:
		// Root coordinator header: use the header's orchestrator + its coord role.
		if sel.CoordRoleID == 0 {
			b.notApplicable("w: selected coordinator has no coord role — cannot spawn a worker")
			return
		}
		orchID = sel.OrchestratorID
		coordRoleID = sel.CoordRoleID
	case selRole:
		if sel.RoleKind == string(db.KindFreelance) {
			b.notApplicable("w: a freelancer is an unmanaged argus task — select a coordinator to spawn a worker under it")
			return
		}
		if sel.RoleKind == string(db.KindCoordinator) {
			// A promoted SUB-COORDINATOR row targets ITSELF (D2): spawn the new
			// worker under its CHILD orchestrator, with this row's own role as
			// coordinator. Its own task coordinates that child orchestrator.
			if sel.ChildOrchestratorID == 0 {
				b.notApplicable("w: this coordinator row has no child orchestrator to spawn under")
				return
			}
			orchID = sel.ChildOrchestratorID
			coordRoleID = sel.RoleID
			break
		}
		// Leaf agent/worker row → that agent's coordinator.
		if sel.OrchestratorID == 0 || sel.CoordRoleID == 0 {
			b.notApplicable("w: selected row is not attached to a coordinator")
			return
		}
		orchID = sel.OrchestratorID
		coordRoleID = sel.CoordRoleID
	default:
		b.notApplicable("w: not applicable to this row")
		return
	}

	capturedOrchID := orchID
	capturedCoordRoleID := coordRoleID

	b.goUI(func() {
		b.modals.ShowInput("New worker", "Prompt", "", func(prompt string) {
			if strings.TrimSpace(prompt) == "" {
				// Empty/whitespace confirm: surface a dismissible notice rather
				// than closing silently (D1). No argus/DB call on this path.
				b.notApplicable("w: prompt is required")
				return
			}
			b.mutate("spawn worker", true, func() error {
				res, err := b.svc.SpawnWorker(b.ctx, ops.SpawnWorkerInput{
					TargetOrchestratorID: capturedOrchID,
					CoordRoleID:          capturedCoordRoleID,
					Prompt:               prompt,
				})
				if err != nil {
					return err
				}
				// Auto-select the new worker row (D3/D7). The rail repopulate is
				// broadcaster-driven (~100ms) and has NOT run yet, so the row does
				// not exist in the rail at this instant — an immediate select would
				// silently no-op. Instead we STASH the role id; the App applies it
				// on the next repopulate, when the row is present. Focus stays RAIL.
				if b.rowSel != nil && res != nil {
					b.rowSel.QueueSelectRole(res.RoleID)
				}
				return nil
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
		// REPAIR-FIRST: a displayed-active header whose coord task is
		// argus-archived (the mixed-coord state — only external argus-side
		// archiving produces it) gets its coord task UNARCHIVED, aligning
		// argus reality to the displayed active orchestrator. Cascade-
		// archiving here would tear down the whole live orchestrator the
		// operator sees as active. ToggleArchiveTask(…, archived=true)
		// unarchives and tolerates a 404 as a skip. Once repaired (or when
		// not mixed), `a` behaves exactly as below.
		if !sel.Archived && sel.CoordArgusArchived && sel.CoordTaskID != "" {
			taskID := sel.CoordTaskID
			b.mutate("archive", true, func() error {
				return b.svc.ToggleArchiveTask(b.ctx, taskID, true)
			})
			return
		}
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

// OnPin toggles the pinned state of the selected coordinator, agent, or
// freelancer (`P`). No modal — pin is non-destructive and instantly reversible
// (press `P` again). The direction follows the selection's rendered pinned
// state: a pinned row unpins, an unpinned row pins.
//
// For managed coordinators and agents the pin is hera-side (pinned_at on the
// DB row); the DB round-trip runs off-loop via mutate.
//
// For freelancers the pin state is stored in the rail's in-memory
// pinnedFreelance map (persisted via railViewState to the config table). No
// DB role row exists and argus exposes no pin endpoint, so the toggle runs
// synchronously on-loop via fPinner.ToggleFreelancePin — no mutate wrapper.
// A pinned freelancer floats to the root level of the Pinned block (BUG-024).
func (b *mutationBridge) OnPin() {
	sel := b.sel.CurrentRailSelection()
	switch sel.Kind {
	case selOrchestrator:
		if sel.Pinned {
			b.mutate("unpin", true, func() error {
				return b.svc.UnpinOrchestrator(b.ctx, sel.OrchestratorID)
			})
		} else {
			b.mutate("pin", true, func() error {
				return b.svc.PinOrchestrator(b.ctx, sel.OrchestratorID)
			})
		}
	case selRole:
		if sel.RoleKind == string(db.KindFreelance) {
			if b.fPinner == nil {
				b.notApplicable("P: freelance pin not available in this context")
				return
			}
			// Toggle in-memory pin state. Runs on-loop (ToggleFreelancePin
			// rebuilds the rail and persists via onStateChanged — no DB role row
			// exists and argus has no pin endpoint for unmanaged tasks).
			b.fPinner.ToggleFreelancePin(sel.ArgusTaskID)
			return
		}
		if sel.Pinned {
			b.mutate("unpin", true, func() error {
				return b.svc.UnpinRole(b.ctx, sel.RoleID)
			})
		} else {
			b.mutate("pin", true, func() error {
				return b.svc.PinRole(b.ctx, sel.RoleID)
			})
		}
	default:
		b.notApplicable("P: not applicable to this row")
	}
}

// OnAdopt adopts the selected FREELANCER into a chosen coordinator (`J`). It
// is freelancer-only: any other selection (coordinator, managed agent,
// orchestrator header, section row) gets visible feedback, never a silent
// no-op. On a freelancer it lists the active orchestrators and opens a target
// picker; selecting one creates an operator-side worker role + binding via
// AdoptTaskIntoOrchestrator (the same DAO path hera_join attach-mode uses), so
// the agent need not act for the binding to exist.
//
// The selection is read synchronously (UI state, on-loop); the orchestrator
// listing + picker open run off-loop via goUI (each modal open bounces through
// QueueUpdateDraw), and the adopt itself runs through mutate from the picker's
// on-loop select callback — so the event loop is never blocked.
func (b *mutationBridge) OnAdopt() {
	sel := b.sel.CurrentRailSelection()
	if sel.Kind != selRole || sel.RoleKind != string(db.KindFreelance) {
		b.notApplicable("J: only freelancers can be adopted into a coordinator")
		return
	}
	if sel.ArgusTaskID == "" {
		b.notApplicable("J: this freelancer has no argus task id to adopt")
		return
	}
	taskID := sel.ArgusTaskID
	name := sel.Name
	project := sel.Project
	worktree := sel.WorktreePath

	b.goUI(func() {
		orchs, err := b.svc.ListActiveOrchestrators(b.ctx)
		if err != nil {
			b.modals.ShowError(err.Error())
			return
		}
		if len(orchs) == 0 {
			b.modals.ShowError("J: no active coordinators to adopt into — create one with `n` first")
			return
		}
		labels := make([]string, len(orchs))
		for i, o := range orchs {
			labels[i] = o.Name
		}
		b.modals.ShowSelect(
			fmt.Sprintf("Adopt %q into…", name),
			"Coordinator",
			labels,
			func(idx int) {
				if idx < 0 || idx >= len(orchs) {
					return
				}
				orchID := orchs[idx].ID
				b.mutate("adopt", true, func() error {
					_, err := b.svc.AdoptTaskIntoOrchestrator(b.ctx, ops.AdoptInput{
						ArgusTaskID:    taskID,
						OrchestratorID: orchID,
						RoleName:       name,
						ArgusProject:   project,
						WorktreePath:   worktree,
					})
					return err
				})
			},
			nil,
		)
	})
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

// OnReattach handles Enter on a dead-session (not permanently Dead) worker or
// freelance row (BUG-033). It calls argus's restart endpoint so the agent
// backend re-spawns with the previous session's ID — the proxy subscription's
// normal reconnect backoff loop then picks up the new session's output
// automatically. Returns true when it owns the Enter (reattach was initiated
// or a clear "not supported" message was shown), so the router must NOT fall
// through to pane-entry. Returns false when the selection is not a reattachable
// dead-session row (the router then runs the normal OnRailSelectEnter path).
//
// Guard: the selected row must have a non-empty ArgusTaskID, must NOT be
// permanently Dead (task record gone — nothing to restart), and must be in a
// terminal status (roleInputDead). A live row, an archived-coord row (handled
// by OnResurrect), or a row with no task id is not a reattach target.
func (b *mutationBridge) OnReattach() bool {
	sel := b.sel.CurrentRailSelection()
	if sel.Kind != selRole {
		return false
	}
	// Only dead-session (not permanently dead, not archived) workers and
	// freelancers are reattachable. OnResurrect handles archived coordinators.
	if sel.ArgusTaskID == "" || sel.Dead {
		return false
	}
	// A live row (no terminal status) should enter the pane normally.
	if !sel.HasDeadSession {
		return false
	}

	taskID := sel.ArgusTaskID
	name := sel.Name
	b.mutate("reattach", false, func() error {
		err := b.svc.ReattachAgent(b.ctx, taskID)
		if err != nil {
			// Surface a human-readable message: distinguish "not supported"
			// (update argus) from other failures (e.g. network error).
			return fmt.Errorf("re-attach %q: %s", name, err.Error())
		}
		// Success: argus is restarting the session. The proxy subscription's
		// reconnect loop picks up the new output automatically. No explicit
		// refresh needed — argus will fire task.status_changed / session.started
		// events that drive the argus state cache + rail repopulate. Trigger a
		// courtesy refresh so the status icon updates sooner.
		b.refresh()
		return nil
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

// OnStatusAdvance steps the selected row's argus status forward (`s`). When
// the advance would reach "complete" (the final argus rung), a y/n modal fires
// first: y advances the argus status to complete (:checked:); n updates only
// the hera role status to "done" without touching the argus workflow status.
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
// BUG-048: when advancing to "complete" (the final argus status rung), a y/n
// confirmation modal fires first. Yes advances the argus task status to
// :checked: (existing behavior). No updates only the hera role status to
// "done" without touching the argus workflow status. The modal fires only
// from this interactive key path — `hera_status(status=done)` via MCP and
// `S` (backward step) are unaffected.
//
// OPTIMISTIC RENDER (BUG-032): the predicted new status is applied to the
// argus state cache at the START of the mutation goroutine — after the
// inFlight guard is claimed — and an immediate repopulate fires so the rail
// icon updates before the argus round-trip (~0.5s) completes. On write
// failure the override is cleared and the rail reverts. On success the poll
// auto-clears the entry once it confirms the new value.
//
// The argus round-trip runs off-loop via mutate.
func (b *mutationBridge) stepStatus(advance bool) {
	sel := b.sel.CurrentRailSelection()
	verb := "s"
	if !advance {
		verb = "S"
	}

	// Resolve the argus task ID for the optimistic overlay. For managed roles
	// sel.ArgusTaskID is the bound task's ID; for orchestrator headers
	// sel.CoordTaskID is the coord task's ID. Either may be empty (no live
	// binding / cold cache) — the optimistic is skipped when either is absent.
	optID := sel.ArgusTaskID
	if optID == "" {
		optID = sel.CoordTaskID
	}

	// Compute the predicted next/prev status from the currently displayed one.
	// Empty sel.Status (cold cache, no binding) means we cannot predict the
	// next rung — skip the optimistic entirely rather than showing a fabricated
	// icon. Clamped transitions (already at complete/pending) are also skipped.
	var optStatus string
	if b.optimizer != nil && optID != "" && sel.Status != "" {
		if advance {
			optStatus = ops.NextStatus(sel.Status)
		} else {
			optStatus = ops.PrevStatus(sel.Status)
		}
		if optStatus == sel.Status {
			optStatus = "" // clamped: no change expected
		}
	}

	// applyOptimistic sets the predicted status in the cache and triggers an
	// immediate repopulate so the new icon renders before the argus round-trip.
	// Called at the START of each mutation goroutine (after the inFlight guard),
	// so a mutation that was dropped (inFlight busy) never touches the cache.
	applyOptimistic := func() {
		if b.optimizer != nil && optID != "" && optStatus != "" {
			b.optimizer.SetOptimistic(optID, optStatus)
			b.refresh()
		}
	}

	// revertOptimistic clears the override and repopulates on write failure so
	// the rail reverts to the true argus state without waiting for the next poll.
	revertOptimistic := func() {
		if b.optimizer != nil && optID != "" && optStatus != "" {
			b.optimizer.ClearOptimistic(optID)
			b.refresh()
		}
	}

	// Compute the argus-step goroutine body and the managed role id (for the
	// MarkRoleDone no-path). Resolved before the modal so closures capture the
	// right values regardless of when the callbacks fire.
	var argusStep func() error
	var markDoneRoleID int64 // 0 = no managed hera role (freelancer)

	switch sel.Kind {
	case selRole:
		if sel.RoleKind == string(db.KindFreelance) {
			if sel.ArgusTaskID == "" {
				b.notApplicable(verb + ": this freelancer has no argus task id to step")
				return
			}
			taskID := sel.ArgusTaskID
			argusStep = func() error {
				applyOptimistic()
				_, err := b.svc.StepTaskStatus(b.ctx, taskID, advance)
				if err != nil {
					revertOptimistic()
				}
				return err
			}
			// markDoneRoleID stays 0: freelancers have no hera role
		} else {
			roleID := sel.RoleID
			argusStep = func() error {
				applyOptimistic()
				var err error
				if advance {
					_, err = b.svc.AdvanceStatus(b.ctx, roleID)
				} else {
					_, err = b.svc.RevertStatus(b.ctx, roleID)
				}
				if err != nil {
					revertOptimistic()
				}
				return err
			}
			markDoneRoleID = roleID
		}
	case selOrchestrator:
		if sel.CoordRoleID == 0 {
			b.notApplicable(verb + ": this project has no coordinator role to step")
			return
		}
		roleID := sel.CoordRoleID
		argusStep = func() error {
			applyOptimistic()
			var err error
			if advance {
				_, err = b.svc.AdvanceStatus(b.ctx, roleID)
			} else {
				_, err = b.svc.RevertStatus(b.ctx, roleID)
			}
			if err != nil {
				revertOptimistic()
			}
			return err
		}
		markDoneRoleID = roleID
	default:
		b.notApplicable(verb + ": not applicable to this row")
		return
	}

	// BUG-048: gate on a y/n confirmation when the advance would reach
	// "complete". optStatus is only "complete" when we could predict the
	// next rung (warm cache, non-empty ArgusTaskID/CoordTaskID) — cold-cache
	// rows fall through to the direct path unchanged.
	if advance && optStatus == "complete" {
		doneRoleID := markDoneRoleID
		b.goUI(func() {
			b.modals.ShowConfirm(
				"Mark done?",
				"Also mark :checked: in argus? (y/n)",
				func() {
					// y: advance the argus task status to complete (:checked:)
					b.mutate("status step", true, argusStep)
				},
				func() {
					// n: update hera role status to "done" only; no argus touch.
					// Freelancers (doneRoleID==0) have no hera role — no-op.
					if doneRoleID == 0 {
						return
					}
					b.mutate("mark done", true, func() error {
						return b.svc.MarkRoleDone(b.ctx, doneRoleID)
					})
				},
			)
		})
		return
	}

	b.mutate("status step", true, argusStep)
}

func (b *mutationBridge) refresh() {
	if b.repop != nil {
		b.repop.RepopulateRail()
	}
}
