package view

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anutron/argus-sdk/theme"
	"github.com/rivo/tview"

	"github.com/anutron/hera/internal/db"
)

// DefaultRailSelectDebounce is the window over which rail j/k cursor
// movements coalesce into a single pane rebind. Without this, a rapid
// hold of j burns one /api/tasks/{id}/resize roundtrip per row.
const DefaultRailSelectDebounce = 120 * time.Millisecond

// maxPendingSelectMisses bounds how many rail repopulates a queued auto-select
// (QueueSelectRole) survives without its row appearing before it is abandoned.
// A spawned worker's row lands on the very next broadcaster repopulate in the
// common case; the slack tolerates a repopulate that raced just ahead of the
// binding insert. Beyond this the row is presumed never-arriving.
const maxPendingSelectMisses = 5

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
	mu                sync.Mutex
	coordTask         string
	agentTask         string
	agentIsFreelancer bool // true when agentTask is bound to a freelancer row
	coordUnsub        func()
	agentUnsub        func()
	coordBridge       *paneBridge
	agentBridge       *paneBridge

	// closed is set after Close runs so subsequent Close calls are no-ops.
	closed bool

	// redraw coalesces pane repaints. Each pane's terminalpane.OnNeedRedraw
	// hook calls redraw.Schedule (marking the surface dirty); a single ticker
	// goroutine flushes at most one QueueUpdateDraw per frame so a chatty PTY
	// burst paints settled frames instead of one partial frame per chunk.
	// Started in BuildApp, stopped in Close. Shared by both panes.
	redraw *redrawCoalescer

	// spinnerStop halts the spinner driver goroutine: a spinnerInterval
	// ticker that schedules a coalesced repaint only while the rail has a
	// running row, so the running spinner animates by wall clock (argus's
	// spinnerLoop cadence: tick only when there's live work). Closed in
	// Close, after the closed guard, so it closes exactly once.
	spinnerStop chan struct{}

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

	// fullscreenActive and fullscreenPane track whether pane fullscreen is
	// active (BUG-027). When active, refreshBody composes only the fullscreen
	// pane (hiding the rail and the other pane). Guarded by mu.
	fullscreenActive bool
	fullscreenPane   FocusState

	// focus is the session's focus machine, injected via SetFocusMachine so
	// the App can flip the present-pane flags when the body mode changes.
	// nil in tests that build the App without a router.
	focus *FocusMachine

	// focusState is a thread-safe mirror of the focus machine's current state,
	// updated in OnFocusChanged (the single chokepoint for focus changes). The
	// raw-input transport layer (rawInputConn) reads it from pluginview's read
	// goroutine to route inbound bytes, where touching the single-threaded
	// FocusMachine directly would race. Zero value = FocusRAIL (the start
	// state), so it reads correctly even before the first OnFocusChanged.
	focusState atomic.Int32

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

	// showArchived is flipped by the `l` rail key (the mutation bridge
	// syncs the ops.ListAllState toggle here via SetShowArchived, from its
	// background goroutine — hence guarded by mu). When true, populateRail
	// walks ListInclusive variants so archived orchestrators and roles
	// render in the rail; when false, archived rows are filtered out (the
	// default at session start per design.md D5).
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

	// pendingSelectRoleID is a role id queued by the mutation bridge
	// (QueueSelectRole) to auto-select on the NEXT rail repopulate. Role/binding
	// inserts trigger a broadcaster-driven (~100ms) refresh, so the new row does
	// not exist when SpawnWorker returns — an immediate select would no-op. The
	// id is consumed (set back to 0) in populateRail once the row is present and
	// selection succeeds; an unresolvable id is logged and cleared so it cannot
	// hijack a later unrelated repopulate. Guarded by selectMu.
	pendingSelectRoleID int64

	// pendingSelectMisses counts repopulates that ran while pendingSelectRoleID
	// was set but the row was still absent. After maxPendingSelectMisses the
	// pending id is abandoned (logged) so a never-arriving row cannot steer a
	// much-later unrelated repopulate. Guarded by selectMu.
	pendingSelectMisses int

	// onDeadPaneReattach, when non-nil, is called by the App when a dead pane
	// receives focus (Ctrl+→ into a dead pane or session dying while in the
	// pane). The callback fires the background argus restart call from its own
	// goroutine. Wired in session.go (nil in tests without rendering).
	onDeadPaneReattach func(taskID string)

	// modalSync (tests only) makes queueModal run its body synchronously
	// instead of bouncing through QueueUpdateDraw — which blocks forever
	// when the tview event loop is not running, as in unit tests.
	modalSync bool

	// modalPrevFocus is the primitive that held tview focus when the
	// current modal opened; closeModal restores it (falling back to the
	// rail). Only touched on the tview event loop (queueModal bodies and
	// modal button callbacks), so it needs no lock.
	modalPrevFocus tview.Primitive
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

	// Make every tview primitive that hera does not explicitly style fall
	// through to the SAME black the pane interiors use (BUG-001). tview's stock
	// PrimitiveBackgroundColor is ColorBlack (the dark Color0 that rendered as
	// the grey-blue chrome/canvas); ContrastBackgroundColor is the lavder/blue
	// modal default. Repointing both at heraBackground (tcell.ColorDefault,
	// matching the emulator cells) means gaps, the pages canvas, and form
	// internals all paint the uniform terminal black with no per-widget setter.
	tview.Styles.PrimitiveBackgroundColor = heraBackground
	tview.Styles.ContrastBackgroundColor = heraBackground

	// tApp is constructed before the panes so each pane's OnNeedRedraw hook can
	// bounce a repaint through its event loop the moment PTY output arrives
	// (live repaint, independent of keystroke input).
	tApp := tview.NewApplication()

	// Route every pane's OnNeedRedraw through a coalescer instead of calling
	// QueueUpdateDraw per chunk. A burst of PTY chunks (the settled-snapshot
	// blob, a SIGWINCH whole-screen re-render, or fast autonomous output) then
	// drains into the emulator between flushes, so each painted frame is
	// settled — no scroll-through of history, no partial-frame garble — while
	// the latest output is still painted within one frame interval.
	redrawCoalescer := newRedrawCoalescer(func() { tApp.QueueUpdateDraw(func() {}) }, DefaultRedrawInterval)
	redraw := redrawCoalescer.Schedule

	coordPane, coordBridge, coordUnsub := newBoundPane("Coord", "(no coord selected)", coordTask, src, redraw)
	agentPane, agentBridge, agentUnsub := newBoundPane("Agent", "(no agent selected)", agentTask, src, redraw)

	pieces := buildLayout(coordPane, agentPane)

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
		redraw:         redrawCoalescer,
		spinnerStop:    make(chan struct{}),
	}

	// Wire reflow callbacks on the initial panes so dimension changes after
	// startup (e.g. Ctrl-Z fullscreen) replay the ring snapshot at the new
	// size, reflowing scrollback to the new width (BUG-038).
	coordPane.onReflow = a.makeCoordReflowCallback(coordTask)
	agentPane.onReflow = a.makeAgentReflowCallback(agentTask)

	// Start the coalescing flush loop. Pane bytes ingested during BuildApp
	// (the initial snapshot) have already armed the dirty flag via Schedule;
	// the first tick paints that settled frame once the event loop runs.
	redrawCoalescer.start()

	// Spinner driver: while the rail has a running row, schedule a coalesced
	// repaint once per spinner frame so the wall-clock-derived spinner
	// advances visibly. Quiet (no Schedule calls) when nothing is running —
	// the rail's hasRunning flag is read atomically, so this goroutine never
	// touches rail state owned by the event loop.
	go func(rail *railList, stop chan struct{}) {
		t := time.NewTicker(spinnerInterval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if rail.HasRunningRows() {
					redrawCoalescer.Schedule()
				}
			}
		}
	}(pieces.rail, a.spinnerStop)

	// Restore persisted fold state and last selection before the initial rail
	// populate so buildRows honours the operator's prior coordinator/archive
	// choices. Errors are non-fatal: a fresh DB returns ErrNotFound, a corrupt
	// entry resets silently, and neither prevents hera from starting.
	var savedLastSel railLastSelection
	if database.Config != nil {
		if s, err := loadRailStateFromDB(context.Background(), database.Config); err == nil {
			if !s.isEmpty() {
				a.pieces.rail.RestoreViewState(s)
			}
			savedLastSel = s.LastSelection // remember for post-populate cursor restore
		}
		cfg := database.Config
		a.pieces.rail.SetOnStateChanged(func() {
			// Capture state immediately (still on the tview event loop) then
			// write to the DB on a background goroutine so the event pump is
			// never blocked by a SQLite write.
			s := a.pieces.rail.ViewState()
			go func() {
				_ = saveRailStateToDB(context.Background(), cfg, s)
			}()
		})
	}

	if err := a.populateRail(database); err != nil {
		return nil, fmt.Errorf("view.BuildApp: populate rail: %w", err)
	}

	// Restore the cursor to the operator's last selected row (BUG-001). The
	// three populateRail Set* calls each attempt restoreCursor on their own, but
	// their ordering (SetFreelance → SetArchivedFreelance → SetOrchestrators)
	// lets the cursor trail a freelancer to the bottom when no prior identity is
	// anchored. One final authoritative restore — after all three sets have
	// settled the row list — places the cursor at the right position.
	//
	// Three cases:
	//  1. savedLastSel non-zero AND found in current rows → restore. Done.
	//  2. savedLastSel non-zero but row no longer visible (archived/deleted) →
	//     fall to the topmost live item (firstSelectableRow).
	//  3. savedLastSel zero (first ever open, no prior session) → fall back to
	//     the original agentTask alignment so the initial pane content and the
	//     cursor row agree, preserving the pre-BUG-001 first-open experience.
	if !a.tryRestoreLastSelection(savedLastSel) {
		if !savedLastSel.isZero() {
			// Case 2: had a saved row but it's gone — land at topmost live item.
			a.pieces.rail.cursor = a.pieces.rail.firstSelectableRow()
			a.pieces.rail.clampOffset()
		} else {
			// Case 3: no prior memory — align cursor with the initial pane
			// selection findInitialSelection picked, so the first frame is
			// consistent with the pane content. Mirrors the pre-BUG-001 behavior.
			if agentTask != "" {
				for _, o := range a.pieces.rail.orchestrators {
					for _, r := range o.Roles {
						if r.ArgusTaskID == agentTask {
							a.pieces.rail.SelectByRoleID(r.RoleID)
						}
					}
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

// SendHelp satisfies helpFrameSender. It pushes a comprehensive hotkey
// dictionary covering all three focus states (Rail, Coord pane, Agent pane),
// sends {"type":"help"} so argus pops its help overlay, then restores the
// current-focus hotkeys so argus's bar is correct when the overlay is
// dismissed. "?" is only reachable from Rail focus, so the restore is always
// Rail-keyed; the live focus state is read for safety. nil control (no session
// conn) makes this a no-op.
func (a *App) SendHelp() error {
	a.mu.Lock()
	coordPresent := a.coordPresent
	control := a.control
	a.mu.Unlock()
	if control == nil {
		return nil
	}
	// Push comprehensive dictionary (all Bar:false so the bar is not corrupted
	// during the brief window before the overlay appears).
	if err := control.SendHotkeys(helpHotkeyItems(coordPresent)); err != nil {
		return err
	}
	// Pop argus's help overlay.
	if err := control.SendHelp(); err != nil {
		return err
	}
	// Restore current-focus hotkeys so the bar is correct on overlay dismiss.
	focus := FocusState(a.focusState.Load())
	return control.SendHotkeys(hotkeyItems(focus, coordPresent))
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
	// Stop the spinner driver. Guarded by the closed flag above, so the
	// channel closes exactly once. spinnerStop is set once in BuildApp and
	// never reassigned.
	if a.spinnerStop != nil {
		close(a.spinnerStop)
	}
	// Stop the coalescer's ticker goroutine. redraw is set once in BuildApp and
	// never reassigned, so reading it outside a.mu is safe. Stop is idempotent
	// and nil-safe.
	a.redraw.Stop()
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
		r.PRState = st.PRState
		return
	}
	// Provider present but argus has no such task: the RECORD is gone
	// (deleted / pruned worktree). Argus doesn't show deleted tasks at all,
	// so neither should hera's active rail — mark it dead (hidden unless `l`
	// listall). taskGone gates on cache readiness so a cold cache doesn't
	// transiently hide live rows on first render. Status is NOT consulted:
	// a completed/failed task whose record still exists is never dead.
	if taskGone(prov, r.ArgusTaskID) {
		r.Dead = true
	}
}

// taskGone reports whether the argus task RECORD no longer exists: the warm
// state cache (which lists ALL tasks, archived and completed included) has no
// entry for the id. This — and ONLY this — is the Dead classification: task
// STATUS never buckets a row (a completed task whose record exists renders
// active with its ✓, mirroring argus's panel). A provider without a
// StatesReady method counts as warm (test fakes); a cold cache reports
// nothing gone so first render never transiently hides live rows. Reading
// the cache snapshot keeps rail rebuilds free of per-row argus HTTP calls —
// populateRail runs on the tview event loop, where a synchronous roundtrip
// per row serializes ahead of input handling.
func taskGone(prov TaskStateProvider, taskID string) bool {
	if prov == nil || taskID == "" {
		return false
	}
	if _, ok := prov.TaskState(taskID); ok {
		return false
	}
	if rp, ok := prov.(interface{ StatesReady() bool }); ok && !rp.StatesReady() {
		return false
	}
	return true
}

// populateRail walks the orchestrators / roles / bindings tables and
// hands the result to the rail widget. Coord roles are NOT added to the
// orchestrator's Roles slice; instead each orchestrator's CoordTaskID
// captures the live coord binding so the COORD pane can rebind
// implicitly when an agent (or the header) is selected.
//
// Live bindings (ended_at IS NULL) drive the moon-stars icon on the
// role row; idle roles get the moon-outline icon. Bindings whose argus
// task RECORD no longer exists (absent from the warm argus state cache —
// pruned / 404) are marked Dead so the rail can hide or dim them. Task
// STATUS never marks a row Dead: a completed task whose record exists
// renders active with its ✓ glyph, exactly like argus's own panel.
//
// Roles are ALWAYS loaded inclusively (active + archived): buildRows
// partitions each coordinator's archived/dead children into its Archive (N)
// expando (collapsed by default, design.md D14), so archiving a row moves
// it into the expando instead of dropping it from the rail. Archived
// orchestrators load only when a.showArchived is set (`l` listall), which
// also force-expands the expandos and reveals dimmed dead rows.
func (a *App) populateRail(database *db.DB) error {
	ctx := context.Background()

	stateProv, _ := a.src.(TaskStateProvider)
	// Snapshot the archive-visibility flag once: it is written by the
	// mutation bridge's background goroutine (SetShowArchived) while this
	// runs on the event loop.
	showArchived := a.archiveVisible()

	// Always load orchestrators INCLUSIVELY (active + archived + pinned):
	// buildRows partitions them into the Pinned section (pinned), the active
	// tree, and the bottom Archive section (archived). Archived root
	// coordinators must reach the rail data so they render in the bottom
	// Archive section WITHOUT `l` (collapsed by default); `l` (showArchived)
	// only force-expands the Archive expandos and reveals dead rows.
	orchs, err := database.Orchestrators.ListInclusive(ctx)
	if err != nil {
		return fmt.Errorf("list orchestrators: %w", err)
	}

	entries := make([]*orchEntry, 0, len(orchs))
	// rendered collects the argus task ids REACHABLE through the rendered
	// tree, so buildFreelance can avoid duplicating them: every task carried
	// by a role ROW (via live bindings or the latest-binding fallback), plus —
	// collected after the loop — every rendered header's coord-pane binding
	// (CoordTaskID). A coord task the header binds is findable by selecting
	// the header, so a freelance row for it would be a duplicate; only a
	// coord task NO rendered header carries (coord role archived, header not
	// loaded) still falls back to Freelance (rail-truthfulness).
	rendered := map[string]struct{}{}
	for _, orch := range orchs {
		entry := &orchEntry{
			ID:       orch.ID,
			Name:     orch.Name,
			Archived: orch.ArchivedAt != nil,
			Pinned:   orch.PinnedAt != nil,
		}

		// ALWAYS load roles inclusively: a hera-archived role (archived_at
		// set, the `a` key) must reach the rail data so buildRows can bucket
		// it into its coordinator's Archive (N) expando next to dead and
		// argus-archived children — the exclusive query made archived rows
		// vanish from the rail entirely instead of moving into the expando.
		// Downstream consumers stay archived-aware: visibleRoleCount and
		// countLiveRoles skip archived rows, and the coord-capture branch
		// below never feeds an archived coord binding to CoordTaskID.
		roles, err := database.Roles.ListByOrchestratorInclusive(ctx, orch.ID)
		if err != nil {
			return fmt.Errorf("list roles for orch %d: %w", orch.ID, err)
		}
		for _, role := range roles {
			bnd, _ := database.Bindings.GetLiveByRole(ctx, role.ID)
			live := bnd != nil
			var argusTaskID string
			startedAt := role.CreatedAt
			if live {
				argusTaskID = bnd.ArgusTaskID
				startedAt = bnd.StartedAt
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
			// Dead = the argus task RECORD no longer exists (warm-cache miss).
			// Status never feeds this: a completed task that still exists is
			// NOT dead — it renders active with its ✓ (status never buckets).
			// Reading the cache snapshot (instead of a per-row argus HTTP
			// aliveness probe) keeps this rebuild — which runs on the tview
			// event loop — free of synchronous network roundtrips.
			dead := taskGone(stateProv, argusTaskID)

			// Coord roles do not render as their own rail row. The first
			// existing + non-archived coord binding feeds the orchestrator's
			// CoordTaskID so the COORD pane (and `^p`/resurrect resolution) can
			// target the coordinator's task when the header is selected.
			// Archived or record-gone coord bindings are skipped so the COORD
			// pane doesn't get bound to a tombstone; a COMPLETED coord that
			// still exists binds normally (its pane shows the last output
			// read-only and the header renders ✓). A worker-less orchestrator
			// therefore renders header-only (the header IS the coordinator).
			if role.Kind == db.KindCoordinator {
				archived := role.ArchivedAt != nil
				// Capture the coord role id (first coord seen) regardless of its
				// archived/live state so the resurrect-on-Enter and `^p`-on-coord
				// flows can target it when the operator selects an (archived or
				// live) root coordinator header.
				if entry.CoordRoleID == 0 {
					entry.CoordRoleID = role.ID
				}
				if !dead && !archived && entry.CoordTaskID == "" && argusTaskID != "" {
					// Prefer a live coord; fall back to its most-recent binding
					// (argusTaskID set above) so the coord pane can still show
					// the coordinator's last output after its task finished —
					// fixes the "coord pane doesn't follow selection" case.
					entry.CoordTaskID = argusTaskID
					// Capture the coord task's argus state so the coordinator
					// header's status icon mirrors argus (☾/○/✓/?), the same way
					// applyArgusState drives a worker row's icon.
					if stateProv != nil {
						if st, ok := stateProv.TaskState(argusTaskID); ok {
							entry.CoordHasState = true
							entry.CoordStatus = st.Status
							entry.CoordIdle = st.Idle
							entry.CoordNeedsInput = st.NeedsInput
							// Argus-side archived bit: with the orchestrator
							// displayed active this is the MIXED-COORD state —
							// the header renders the ⊘ repair cue and `a`
							// repairs (unarchives the coord task) instead of
							// cascade-archiving.
							entry.CoordArgusArchived = st.Archived
						}
					}
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
				Pinned:         role.PinnedAt != nil,
				StartedAt:      startedAt,
				// Carry the owning orchestrator's coord role id (roles are loaded
				// coordinator-first, so entry.CoordRoleID is already set by the
				// time we build worker rows). `w` reads this to spawn the new
				// worker under this row's coordinator.
				CoordRoleID: entry.CoordRoleID,
			}
			applyArgusState(r, stateProv)
			entry.Roles = append(entry.Roles, r)
			if argusTaskID != "" {
				rendered[argusTaskID] = struct{}{}
			}
		}

		// Coord-only (worker-less) orchestrators render HEADER-ONLY: the
		// orchestrator header IS the coordinator. With no worker agents the
		// header has zero children, and selecting it composes the full-width
		// HERA pane (coordinator mode) bound to entry.CoordTaskID. We do NOT
		// synthesize a child coord role row.
		entries = append(entries, entry)
	}

	// Resolve sub-coordinators (multi-bindings): hera's data model is flat (a
	// Role has a single OrchestratorID; orchestrators have no parent link), so a
	// "sub-coordinator" is the SAME argus task bound twice — a worker role under
	// a parent orchestrator AND the coord of a separate child orchestrator. The
	// join key is a worker roleEntry's ArgusTaskID == some orchEntry's
	// CoordTaskID. Link them so the rail nests the child under the worker (which
	// is promoted to a coordinator row), and drop the child from the top level
	// so it isn't double-rendered.
	// Every rendered header's coord-pane binding is reachable via that header
	// (selecting it binds the coord pane), so it must not ALSO fall back into
	// the Freelance section. Collected from the flat pre-consumption list:
	// every entry here renders — either as a top-level header or, when
	// resolveSubCoordinators consumes it, nested under its promoted worker
	// row (same orchEntry value, same CoordTaskID).
	for _, e := range entries {
		if e.CoordTaskID != "" {
			rendered[e.CoordTaskID] = struct{}{}
		}
	}

	entries = resolveSubCoordinators(entries)

	a.pieces.rail.SetShowArchived(showArchived)
	active, archivedFree := a.buildFreelance(ctx, database, rendered)
	a.pieces.rail.SetFreelance(active)
	a.pieces.rail.SetArchivedFreelance(archivedFree)
	a.pieces.rail.SetOrchestrators(entries)

	// Apply any queued auto-select (e.g. the worker just spawned via `w`): the
	// row now exists in the freshly-populated rail, so move the cursor to it.
	// Focus is unchanged — the operator stays in RAIL.
	a.applyPendingSelect()

	// BUG-037: Details pane live-refresh. The rail was just rebuilt with fresh
	// orchEntry data, but if the cursor stayed on the same row, maybeFireSelectionChanged
	// is a no-op (cursor index unchanged) so onRailSelectionChanged never fires and
	// updateCoordDetails is never called. Re-derive the Details pane from the current
	// selection so a joining worker / status change shows up without leaving the view.
	a.refreshDetailsForCurrentSelection()
	return nil
}

// resolveSubCoordinators rewrites the flat orchestrator list into a tree by
// detecting multi-bindings: when a worker roleEntry's ArgusTaskID equals
// another orchEntry's CoordTaskID, that worker IS that orchestrator's
// coordinator. The worker is promoted to a coordinator row carrying the child
// orchestrator (childOrch), and the child is removed from the returned
// top-level list (it renders nested instead). The relinking is recursive — a
// sub-coordinator's child may itself contain a further sub-coordinator —
// because every orchEntry is linked in a single pass and the tree is then
// walked lazily at render time. A self-binding (a coord's own worker pointing
// back at the same orchestrator) is skipped so a node never becomes its own
// child. The buildRows cycle guard (seen set) protects against any remaining
// pathological cross-links at render time.
func resolveSubCoordinators(entries []*orchEntry) []*orchEntry {
	// Index orchestrators by their coord task so a worker can find the child
	// orchestrator it coordinates. Skip empty coord tasks (no binding) and only
	// keep the first orchestrator per coord task (a coord task is unique to one
	// orchestrator in practice; first-wins is deterministic).
	byCoordTask := make(map[string]*orchEntry, len(entries))
	for _, o := range entries {
		if o.CoordTaskID == "" {
			continue
		}
		if _, dup := byCoordTask[o.CoordTaskID]; !dup {
			byCoordTask[o.CoordTaskID] = o
		}
	}

	consumed := make(map[int64]bool, len(entries))
	for _, o := range entries {
		for _, role := range o.Roles {
			if role.ArgusTaskID == "" {
				continue
			}
			child, ok := byCoordTask[role.ArgusTaskID]
			if !ok || child.ID == o.ID {
				// No child orchestrator for this task, or it would link an
				// orchestrator to itself — leave the role as a plain worker.
				continue
			}
			// This worker is the child orchestrator's coordinator. Promote it to
			// a foldable coordinator row nesting the child's roles.
			role.childOrch = child
			role.RoleKind = string(db.KindCoordinator)
			consumed[child.ID] = true
		}
	}

	if len(consumed) == 0 {
		return entries
	}
	out := make([]*orchEntry, 0, len(entries))
	for _, o := range entries {
		if consumed[o.ID] {
			continue
		}
		out = append(out, o)
	}
	return out
}

// findOrchestratorByID returns the orchEntry with the given ID from the
// orchestrator tree, recursing into childOrch entries nested under role rows.
// resolveSubCoordinators removes child orchestrators from the top-level list
// and attaches them as roleEntry.childOrch, so a top-level-only scan misses
// any orchestrator that is the coordinator of a nested sub-orchestrator.
// Returns nil when no match is found.
func findOrchestratorByID(orchs []*orchEntry, id int64) *orchEntry {
	for _, o := range orchs {
		if o.ID == id {
			return o
		}
		for _, role := range o.Roles {
			if role.childOrch != nil {
				if found := findOrchestratorByID([]*orchEntry{role.childOrch}, id); found != nil {
					return found
				}
			}
		}
	}
	return nil
}

// buildFreelance partitions the live argus task list into the Freelance
// section, grouped by argus project/repo "the same way Argus shows them". A
// freelancer is a live argus task hera does not CURRENTLY manage: no LIVE
// binding claims it AND no role row already renders it (rendered — workers
// carry ended bindings' tasks via the latest-binding fallback). A task whose
// bindings have ALL ENDED and that no row carries (a coord binding reconciled
// away — the live-QA lost-session shape) falls back here so every non-archived
// argus task stays findable in the rail.
//
// Returns (active, archived): active freelancers grouped by repo, and a flat
// list of archived freelancers (the operator pressed `a` on them). Archived
// freelancers are split out so the rail renders them ONLY in the bottom
// Archive section — reachable WITHOUT `l`, never inline in their repo group
// (no double-render). Both are nil when no FreelanceProvider is wired (tests)
// or no freelancers exist.
func (a *App) buildFreelance(ctx context.Context, database *db.DB, rendered map[string]struct{}) ([]*freelanceProject, []*roleEntry) {
	prov, ok := a.src.(FreelanceProvider)
	if !ok {
		return nil, nil
	}
	tasks := prov.LiveTasks()
	if len(tasks) == 0 {
		return nil, nil
	}
	liveBindings, err := database.Bindings.ListLive(ctx)
	if err != nil {
		// On a query failure, fail safe to "everything looks managed" so we
		// never mislabel a hera-managed task as a freelancer.
		return nil, nil
	}
	liveBound := make(map[string]struct{}, len(liveBindings))
	for _, b := range liveBindings {
		liveBound[b.ArgusTaskID] = struct{}{}
	}

	mkRow := func(t ArgusTaskInfo) *roleEntry {
		return &roleEntry{
			RoleKind:        string(db.KindFreelance),
			Name:            t.Name,
			ArgusTaskID:     t.ID,
			WorktreePath:    t.WorktreePath,
			Project:         t.Project,
			Live:            t.State.Status == "in_progress" && !t.State.Idle,
			ElapsedOverride: t.Elapsed,
			HasState:        true,
			Status:          t.State.Status,
			ArgusIdle:       t.State.Idle,
			NeedsInput:      t.State.NeedsInput,
			ArgusArchived:   t.State.Archived,
		}
	}

	byProject := map[string]*freelanceProject{}
	var order []string
	var archivedFree []*roleEntry
	for _, t := range tasks {
		if _, managed := liveBound[t.ID]; managed {
			continue
		}
		if _, shown := rendered[t.ID]; shown {
			continue
		}
		// Archived freelancers go to the bottom Archive section (always
		// collected so they're reachable without `l`), never inline.
		if t.State.Archived {
			archivedFree = append(archivedFree, mkRow(t))
			continue
		}
		fp, seen := byProject[t.Project]
		if !seen {
			fp = &freelanceProject{Project: t.Project}
			byProject[t.Project] = fp
			order = append(order, t.Project)
		}
		fp.Tasks = append(fp.Tasks, mkRow(t))
	}
	if len(order) == 0 {
		return nil, archivedFree
	}
	sort.Strings(order)
	out := make([]*freelanceProject, 0, len(order))
	for _, p := range order {
		out = append(out, byProject[p])
	}
	return out, archivedFree
}

// RepopulateRail re-renders the rail from the current DB state. It bounces
// through QueueUpdateDraw so the actual node-tree mutation happens on the
// tview event loop.
//
// CONTRACT: callers MUST be off the event loop (tview v0.42's QueueUpdate
// blocks until the queued func runs — calling this FROM the loop deadlocks
// it). Production callers honor this: the RailRefresher and argus-state
// subscriber fire from their own goroutines, and the mutation bridge
// refreshes from its mutate/goUI goroutines.
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

// SetShowArchived records whether the rail should render archived rows
// (orchestrators, roles, freelancers — the Archive sections). Called by the
// mutation bridge from its background goroutine when `l` toggles the
// session's archive visibility, immediately before it triggers a
// RepopulateRail; populateRail snapshots the flag on the event loop.
//
// Satisfies the bridge's archiveVisibilitySetter contract.
func (a *App) SetShowArchived(v bool) {
	a.mu.Lock()
	a.showArchived = v
	a.mu.Unlock()
}

// archiveVisible reads the archive-visibility flag under the same lock.
func (a *App) archiveVisible() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.showArchived
}

// scheduleRedraw marks the pane surface dirty so the redraw coalescer flushes
// a single repaint on its next tick. It is wired into each pane's
// terminalpane.OnNeedRedraw (fired once per non-empty PTY chunk) so pane output
// paints live, independent of keystroke input. nil-coalescer-safe (tests).
//
// Unlike a per-chunk QueueUpdateDraw, Schedule is non-blocking: it does NOT
// back-pressure the consume goroutine on the event loop. That lets a burst of
// chunks drain fully into the emulator between coalesced flushes, so each
// painted frame is settled rather than a partial mid-stream frame.
func (a *App) scheduleRedraw() {
	a.redraw.Schedule()
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
			Pinned:         ref.Pinned,
			// Coord-pane binding + its argus archived bit: together with
			// Archived these let `a` detect the MIXED-COORD state (displayed-
			// active orchestrator, argus-archived coord task) and repair —
			// unarchive the coord task by id — instead of cascade-archiving.
			CoordTaskID:        ref.CoordTaskID,
			CoordArgusArchived: ref.CoordArgusArchived,
			// Child agents that `^d` will also destroy: the orchestrator's
			// live (non-archived) child roles.
			ChildCount: countLiveRoles(ref.Roles),
			// Status carries the coord task's argus status for the optimistic
			// render path (BUG-032): stepStatus computes the predicted next/prev
			// status from this value without a separate cache lookup.
			Status: ref.CoordStatus,
		}
	case *roleEntry:
		pinned := ref.Pinned
		if ref.RoleKind == string(db.KindFreelance) {
			// Freelancers have no DB pinned_at; their pin state lives in the
			// rail's pinnedFreelance map (persisted via railViewState).
			pinned = a.pieces.rail.IsFreelancePinned(ref.ArgusTaskID)
		}
		sel := railSelection{
			Kind:           selRole,
			OrchestratorID: ref.OrchestratorID,
			RoleID:         ref.RoleID,
			Name:           ref.Name,
			RoleKind:       ref.RoleKind,
			Archived:       ref.Archived,
			Pinned:         pinned,
			ArgusTaskID:    ref.ArgusTaskID,
			// CoordRoleID is the owning orchestrator's coord role; `w` spawns
			// the new worker under it for a leaf/agent row.
			CoordRoleID: ref.CoordRoleID,
			// Argus-side archived + binding-dead state: with the hera flag
			// these let `a` compute the EFFECTIVE archived state the rail
			// displays (roleArchived) and pick the explicit verb — a
			// mixed-flag row (hera-active + argus-archived) must unarchive,
			// and a freelance row (no hera role row, Archived always false)
			// toggles purely on the argus side.
			ArgusArchived: ref.ArgusArchived,
			Dead:          ref.Dead,
			// HasDeadSession is the reattach signal (BUG-033): the task record
			// exists but the PTY session has ended (terminal status). Distinct
			// from Dead (record gone). Enter on a dead-session row attempts a
			// restart rather than entering a pane that has no live PTY.
			HasDeadSession: !ref.Dead && ref.HasState && !taskStatusAlive(ref.Status),
			WorktreePath:   ref.WorktreePath,
			Project:        ref.Project,
			// Status carries the role's argus status for the optimistic render
			// path (BUG-032): stepStatus computes the predicted next/prev status
			// from this value without a separate cache lookup.
			Status: ref.Status,
		}
		// A promoted sub-coordinator row (its own task coordinates a child
		// orchestrator) is a coordinator TARGET in its own right: `w` spawns
		// under the CHILD orchestrator with this row's OWN role as coord. Carry
		// the child orchestrator id so OnNewWorker can resolve that (D2).
		if ref.childOrch != nil {
			sel.ChildOrchestratorID = ref.childOrch.ID
		}
		return sel
	}
	return railSelection{}
}

// ToggleFreelancePin toggles the pinned state of the freelance task identified
// by argusTaskID in the rail's pinnedFreelance map. Runs on the event loop
// (called synchronously from OnPin). Satisfies the freelancePinner contract.
func (a *App) ToggleFreelancePin(argusTaskID string) {
	if a.pieces.rail != nil {
		a.pieces.rail.ToggleFreelancePin(argusTaskID)
	}
}

// QueueSelectRole stashes a role id to auto-select on the NEXT rail
// repopulate. The mutation bridge calls this after SpawnWorker inserts the
// worker role + binding: those inserts trigger a broadcaster-driven (~100ms)
// rail refresh, so the new row does not exist yet at call time. populateRail
// consumes the id once the row is present (see applyPendingSelect). Focus is
// not changed — the operator stays in RAIL.
//
// Satisfies the rowSelector contract used by the mutation bridge.
func (a *App) QueueSelectRole(id int64) {
	a.selectMu.Lock()
	a.pendingSelectRoleID = id
	// Reset the miss budget so each newly-queued select gets the full
	// maxPendingSelectMisses allowance — otherwise a prior queue that burned
	// misses then succeeded would leave this one with a reduced budget.
	a.pendingSelectMisses = 0
	a.selectMu.Unlock()
}

// applyPendingSelect moves the rail cursor to the queued role id (if any) once
// the row is present. Called at the tail of populateRail, on the tview event
// loop. When the queued row exists, selection moves to it and the id is
// consumed. When it cannot be resolved on this repopulate the id is RETAINED
// (the row may simply not have landed yet — a later repopulate retries); to
// stop a permanently-unresolvable id from hijacking an unrelated future
// repopulate it is only logged-and-cleared after it has survived a bounded
// number of repopulate attempts.
func (a *App) applyPendingSelect() {
	a.selectMu.Lock()
	id := a.pendingSelectRoleID
	a.selectMu.Unlock()
	if id == 0 || a.pieces.rail == nil {
		return
	}
	if a.pieces.rail.SelectByRoleID(id) {
		// Selected — consume the pending id.
		a.selectMu.Lock()
		if a.pendingSelectRoleID == id {
			a.pendingSelectRoleID = 0
		}
		a.selectMu.Unlock()
		return
	}
	// Not yet present. Bump the miss counter; clear after a bounded number of
	// repopulates so a never-arriving row (e.g. insert raced an archive) does
	// not silently steer a much-later unrelated repopulate.
	a.selectMu.Lock()
	a.pendingSelectMisses++
	misses := a.pendingSelectMisses
	if misses >= maxPendingSelectMisses {
		a.pendingSelectRoleID = 0
		a.pendingSelectMisses = 0
	}
	a.selectMu.Unlock()
	if misses >= maxPendingSelectMisses {
		slog.Default().Warn("view: pending auto-select role never appeared in rail; giving up",
			"role_id", id, "repopulate_attempts", misses)
	}
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

// AgentIsFreelancer reports whether the agent pane is currently bound to a
// freelancer task. Used in tests to verify the BUG-009 guard is set correctly.
func (a *App) AgentIsFreelancer() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.agentIsFreelancer
}

// IsFiltering reports whether the rail is in search input mode, satisfying the
// KeyRouter.RailFilter gate so the router yields keys to the rail while the
// operator is typing a `/` query. Runs on the tview input pump (the rail's
// filtering flag is event-loop state). nil-rail-safe.
func (a *App) IsFiltering() bool {
	return a.pieces.rail != nil && a.pieces.rail.Filtering()
}

// CurrentFocus returns the current focus state from the thread-safe mirror.
// Satisfies focusReader so the raw-input transport layer can route inbound
// bytes by focus from pluginview's read goroutine. Reads RAIL until the first
// OnFocusChanged (atomic zero value), matching the focus machine's start state.
func (a *App) CurrentFocus() FocusState {
	return FocusState(a.focusState.Load())
}

// OnFocusChanged repaints the colored focus border so the operator sees
// which of the three elements is active and routes tview's primitive
// focus to the matching widget so propagated keys (the rail's j/k/↑/↓)
// land where they should. Satisfies the KeyRouter.BorderUpdater contract.
// Called from the tview input pump (so direct tview state mutation is
// safe here — single-threaded by contract).
//
// Focus feedback mirrors argus: the focused element's border is painted in
// argus's title/focus cyan (theme.ColorTitle), unfocused borders in argus's
// dim border gray (theme.ColorBorder), so Ctrl-H into hera feels like the same
// app. The SDK terminalpane hardcodes a white (StyleDefault) border on focus,
// so pinnedTerminalPane.Draw repaints the pane border in the same cyan; the
// rail (a plain Box) honors SetBorderColor directly here.
func (a *App) OnFocusChanged(state FocusState) {
	// Mirror the new state for the raw-input transport layer (read off the
	// tview goroutine). This is the single chokepoint for every focus change
	// (explicit transitions, the Enter/nav jumps, and present-pane rebalance),
	// so the mirror stays in lockstep with the focus machine.
	a.focusState.Store(int32(state))

	focused := theme.ColorTitle
	unfocused := theme.ColorBorder

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

	// While a modal overlay is up, the modal owns tview focus and NOTHING
	// may steal it: this path also fires from background rail repopulates
	// (argus state refresh → applyRailSelection → setBodyMode), and an
	// unconditional SetFocus here yanked focus back to the rail behind the
	// overlay — leaving the operator trapped with a modal that no longer
	// received Enter (live acceptance T3). Borders still repaint; only the
	// focus move is withheld. closeModal restores focus when the modal goes.
	moveFocus := a.app != nil && !a.IsModalActive()

	switch state {
	case FocusRAIL:
		a.pieces.rail.SetBorderColor(focused)
		if moveFocus {
			a.app.SetFocus(a.pieces.rail)
		}
	case FocusCOORD:
		a.pieces.coord.SetBorderColor(focused)
		if moveFocus {
			a.app.SetFocus(a.pieces.coord)
		}
	case FocusAGENT:
		a.pieces.agent.SetBorderColor(focused)
		if moveFocus {
			a.app.SetFocus(a.pieces.agent)
		}
	}

	// BUG-008 path 2: when focus lands on a pane (Ctrl+→ focus ladder), check
	// whether its bound task is dead-session and auto-trigger reattach if so.
	// Guard: only fires when entering a pane, and StartPaneReattach already sets
	// pane.reattaching=true before calling OnFocusChanged, so the check is
	// idempotent — no recursive trigger.
	if state == FocusCOORD || state == FocusAGENT {
		a.maybeAutoReattachPane(state)
	}

	// BUG-010 (browse-archived case): when entering any pane, force a resize
	// dispatch so the emulator clamps the cursor position from its snapshot to
	// the current pane bounds. Without this, an archived task's PTY snapshot may
	// position the cursor at coordinates that were valid in the old session but
	// exceed the current pane allocation — the cursor then appears below the
	// argus status line and keystrokes appear lost until the operator resizes.
	//
	// This mirrors the pattern App.OnTaskReattached uses for the reattach case
	// (BUG-053): clear the ProxyManager's "already applied" dedup flag so the
	// next ResizeTask dispatch reaches argus unconditionally, then queue a draw
	// that reads the current pane size and calls ResizeTask. For archived tasks
	// the resize 404s (no active session) but the QueueUpdateDraw still fires
	// pinnedTerminalPane.Draw, which calls tp.Resize(innerCols, innerRows) and
	// clamps the emulator cursor to within the current pane bounds.
	if a.app != nil && (state == FocusCOORD || state == FocusAGENT) {
		a.mu.Lock()
		var taskID string
		switch state {
		case FocusCOORD:
			taskID = a.coordTask
		case FocusAGENT:
			taskID = a.agentTask
		}
		a.mu.Unlock()
		if taskID != "" {
			if ri, ok := a.src.(paneResizeInvalidator); ok {
				ri.InvalidateResize(taskID)
			}
			go a.app.QueueUpdateDraw(func() {
				a.mu.Lock()
				var cols, rows int
				switch state {
				case FocusCOORD:
					if a.coordTask == taskID && a.pieces.coord != nil {
						cols, rows = a.pieces.coord.PinnedSize()
					}
				case FocusAGENT:
					if a.agentTask == taskID && a.pieces.agent != nil {
						cols, rows = a.pieces.agent.PinnedSize()
					}
				}
				a.mu.Unlock()
				if cols > 0 && rows > 0 && a.src != nil {
					a.src.ResizeTask(taskID, cols, rows)
				}
			})
		}
	}
}

// OnFullscreenChanged satisfies the KeyRouter.FullscreenUpdater contract
// (BUG-027). When active=true, the named pane fills the entire body area
// (rail and the other pane are hidden) and the pane's own title bracket-wraps
// its name to signal fullscreen mode. When active=false, the normal split is
// restored and both pane titles revert to their plain names. The indicator
// lives in the coord/agent pane-title slot rather than a separate top bar row,
// so the layout stays flush with zero internal margin (BUG-031).
// Runs on the tview event loop (called from the key router's input pump).
func (a *App) OnFullscreenChanged(pane FocusState, active bool) {
	a.mu.Lock()
	a.fullscreenActive = active
	a.fullscreenPane = pane
	a.mu.Unlock()

	if active {
		if pane == FocusCOORD {
			a.pieces.coord.SetTitle("[ Coord ]")
		} else {
			a.pieces.agent.SetTitle("[ Agent ]")
		}
	} else {
		a.pieces.coord.SetTitle("Coord")
		a.pieces.agent.SetTitle("Agent")
	}
	a.refreshBody()
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
		if roleInputDead(ref) {
			// Dead or dead-session row: the task's PTY returns HTTP 404 on /input.
			// applyRailSelection already cleared Dead panes to their placeholder and
			// forced focus to RAIL for any dead-session row. Stay in RAIL — never
			// enter a pane whose PTY would freeze the view (BUG-014 + BUG-018).
			return FocusRAIL
		}
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
// Since argus-sdk v0.0.3 the offset is a real, rendered offset: the SDK pane
// paints the scrollback window plus a [SCROLL] badge while scrolled and
// anchor-locks the view under new output. The keyboard path (1 line/press)
// and the wheel path (wheelStep lines/tick, see applyWheel) drive this same
// engine.
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

// wheelStep is the number of lines a pane scrolls (and rows the rail pans)
// per mouse-wheel tick — mirrors argus's task-terminal mouseScrollStep.
const wheelStep = 3

// RouteWheel satisfies the rawInputConn.WheelRouter contract: it receives a
// decoded SGR wheel tick on pluginview's read goroutine and bounces it onto
// the tview event loop, where applyWheel hit-tests the live layout rects.
func (a *App) RouteWheel(up bool, x, y int) {
	tApp := a.Application()
	if tApp == nil {
		return
	}
	tApp.QueueUpdateDraw(func() { a.applyWheel(up, x, y) })
}

// applyWheel routes one wheel tick by POSITION, not focus: the SGR 1-based
// viewport cell converts to the 0-based screen cell and is hit-tested against
// the rail and both panes' current rects. A pane hit scrolls that pane's
// scrollback wheelStep lines (up = into history); a rail hit pans the rail
// viewport without moving the selection (so pane bindings never churn from a
// casual scroll). Panes absent from the current body mode (freelance mode has
// no coord pane) are skipped — their stale rects must not swallow the tick.
// Anything else (top bar, gaps) is a dead zone. Runs on the tview event loop.
func (a *App) applyWheel(up bool, x, y int) {
	cx, cy := x-1, y-1
	a.mu.Lock()
	rail := a.pieces.rail
	coord, agent := a.pieces.coord, a.pieces.agent
	coordPresent, agentPresent := a.coordPresent, a.agentPresent
	a.mu.Unlock()

	delta := wheelStep
	if !up {
		delta = -wheelStep
	}
	switch {
	case rail != nil && rail.InRect(cx, cy):
		// Wheel-up reveals earlier rows (offset shrinks); wheel-down later ones.
		rail.PanBy(-delta)
	case coordPresent && coord != nil && coord.InRect(cx, cy):
		coord.ScrollBy(delta)
	case agentPresent && agent != nil && agent.InRect(cx, cy):
		agent.ScrollBy(delta)
	}
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
	pane, bridge, unsub := newBoundPane("Coord", "(no coord selected)", taskID, a.src, a.scheduleRedraw)
	pane.onReflow = a.makeCoordReflowCallback(taskID)
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
	pane, bridge, unsub := newBoundPane("Agent", "(no agent selected)", taskID, a.src, a.scheduleRedraw)
	pane.onReflow = a.makeAgentReflowCallback(taskID)
	a.agentTask = taskID
	a.agentIsFreelancer = false // reset; caller sets true for freelancer rows
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

// makeCoordReflowCallback returns a pinnedTerminalPane.onReflow callback for
// the COORD pane. When the pane's inner rect changes, the callback schedules
// a forceRebindCoord via QueueUpdateDraw so the ring buffer snapshot is
// replayed through a fresh emulator at the new dimensions, reflowing
// scrollback to the new width (BUG-038). Returns nil for empty taskIDs
// (placeholder panes never fire onReflow).
//
// The callback is debounced by the same defaultResizeDebounce window used for
// PTY resize dispatches: the initial frames of a session draw at the SDK
// default 80x24 surface (producing 20x21 pane inners) before the real
// resize envelope arrives. Without debounce that transient size triggers a
// useless reflow at 20x21 that is immediately superseded by the correct one;
// with debounce only the settled final size reaches forceRebindCoord.
//
// The dispatch is wrapped in a goroutine so the timer callback does not
// block the calling goroutine in tests where no event loop is running.
func (a *App) makeCoordReflowCallback(taskID string) func(int, int) {
	if taskID == "" {
		return nil
	}
	var mu sync.Mutex
	var timer *time.Timer
	var latestCols, latestRows int
	return func(cols, rows int) {
		mu.Lock()
		latestCols, latestRows = cols, rows
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(defaultResizeDebounce, func() {
			mu.Lock()
			c, r := latestCols, latestRows
			mu.Unlock()
			go a.app.QueueUpdateDraw(func() {
				a.forceRebindCoord(taskID, c, r)
			})
		})
		mu.Unlock()
	}
}

// makeAgentReflowCallback is the AGENT-pane counterpart of makeCoordReflowCallback.
func (a *App) makeAgentReflowCallback(taskID string) func(int, int) {
	if taskID == "" {
		return nil
	}
	var mu sync.Mutex
	var timer *time.Timer
	var latestCols, latestRows int
	return func(cols, rows int) {
		mu.Lock()
		latestCols, latestRows = cols, rows
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(defaultResizeDebounce, func() {
			mu.Lock()
			c, r := latestCols, latestRows
			mu.Unlock()
			go a.app.QueueUpdateDraw(func() {
				a.forceRebindAgent(taskID, c, r)
			})
		})
		mu.Unlock()
	}
}

// forceRebindCoord is like rebindCoord but bypasses the same-task guard.
// It replays the ring buffer snapshot through a fresh emulator pre-sized to
// (cols, rows), reflowing scrollback at the new dimensions (BUG-038). No-op
// when the App is closed or the coord task has changed since the callback
// was registered (stale closure).
func (a *App) forceRebindCoord(taskID string, cols, rows int) {
	a.mu.Lock()
	if a.closed || a.coordTask != taskID {
		a.mu.Unlock()
		return
	}
	oldUnsub := a.coordUnsub
	oldBridge := a.coordBridge
	oldPane := a.pieces.coord
	pane, bridge, unsub := newBoundPaneAt("Coord", "(no coord selected)", taskID, a.src, a.scheduleRedraw, cols, rows)
	pane.onReflow = a.makeCoordReflowCallback(taskID)
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

// forceRebindAgent is the AGENT-pane counterpart of forceRebindCoord.
func (a *App) forceRebindAgent(taskID string, cols, rows int) {
	a.mu.Lock()
	if a.closed || a.agentTask != taskID {
		a.mu.Unlock()
		return
	}
	oldUnsub := a.agentUnsub
	oldBridge := a.agentBridge
	oldPane := a.pieces.agent
	pane, bridge, unsub := newBoundPaneAt("Agent", "(no agent selected)", taskID, a.src, a.scheduleRedraw, cols, rows)
	pane.onReflow = a.makeAgentReflowCallback(taskID)
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
// tview event loop so primitive mutation stays single-threaded. After
// applying the selection it persists the new cursor identity to the DB
// so the next hera-view open can restore it (BUG-001).
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
		a.app.QueueUpdateDraw(func() {
			a.applyRailSelection(ref)
			a.saveSelectionState()
		})
		return
	}
	a.applyRailSelection(ref)
	a.saveSelectionState()
}

// fireSelectionNow cancels any pending debounce timer and immediately applies
// the buffered selection to the panes. Must run on the tview event loop (same
// requirement as applyRailSelection). Safe to call when no selection is pending.
//
// Used by StartPaneReattach (Enter on dead-session row before the 120ms
// debounce fires) and by the Ctrl+→ handler (BUG-012): in both cases the pane
// may still be bound to the previous task, so we need the selection applied
// before maybeAutoReattachPane checks the task ID.
func (a *App) fireSelectionNow() {
	a.selectMu.Lock()
	if !a.selectHasRef || a.closed {
		a.selectMu.Unlock()
		return
	}
	ref := a.selectPending
	a.selectPending = nil
	a.selectHasRef = false
	if a.selectTimer != nil {
		a.selectTimer.Stop()
		a.selectTimer = nil
	}
	a.selectMu.Unlock()
	a.applyRailSelection(ref)
	a.saveSelectionState()
}

// FireSelectionNow satisfies PendingSelectionFirer so the KeyRouter can flush
// a pending debounced selection before stepping focus from RAIL into a pane.
func (a *App) FireSelectionNow() { a.fireSelectionNow() }

// tryRestoreLastSelection moves the rail cursor to the row described by sel
// (BUG-001). Tries three stable identifiers in order:
//
//  1. RoleID — the DB primary key for managed (non-freelance) roles.
//  2. ArgusTaskID — stable for freelancers (RoleID==0) and as a fallback for
//     managed roles whose binding might have been updated since the save.
//  3. OrchID — for orchestrator header rows.
//
// Returns true when the cursor lands on the target row, false when sel is
// zero or none of the identifiers resolve to a visible row (row archived,
// deleted, or filtered out). On false the caller should fall back to
// firstSelectableRow to land at the topmost live item.
func (a *App) tryRestoreLastSelection(sel railLastSelection) bool {
	if sel.isZero() || a.pieces.rail == nil {
		return false
	}
	if sel.RoleID > 0 && a.pieces.rail.SelectByRoleID(sel.RoleID) {
		return true
	}
	if sel.ArgusTaskID != "" && a.pieces.rail.SelectByArgusTaskID(sel.ArgusTaskID) {
		return true
	}
	if sel.OrchID > 0 && a.pieces.rail.SelectByOrchID(sel.OrchID) {
		return true
	}
	return false
}

// saveSelectionState snapshots the current rail fold choices and cursor
// identity and writes them to the DB so the next hera-view open can restore
// the operator to the same row (BUG-001). No-op when no DB config is wired
// (tests that don't exercise persistence). Runs on the tview event loop (called
// from fireRailSelection via QueueUpdateDraw) or synchronously in the zero-
// debounce test path — both are safe since rails state is always accessed on a
// single goroutine at a time in each context.
func (a *App) saveSelectionState() {
	if a.database == nil || a.database.Config == nil || a.pieces.rail == nil {
		return
	}
	s := a.pieces.rail.ViewState()
	cfg := a.database.Config
	go func() {
		_ = saveRailStateToDB(context.Background(), cfg, s)
	}()
}

// roleInputDead reports whether the task bound to a rail row is unable to
// accept PTY input — i.e., PostTaskInput would return HTTP 404. This covers:
//   - Dead=true: the argus task RECORD is gone (the BUG-014 case);
//   - HasState=true with a terminal status: argus reports the task is complete/
//     failed/etc. but its record still exists (the BUG-018 "dead-session" case).
//
// When HasState=false the argus state cache has not yet observed this task; we
// assume alive so a cold cache doesn't prematurely lock operators out.
func roleInputDead(r *roleEntry) bool {
	if r.Dead {
		return true
	}
	return r.HasState && !taskStatusAlive(r.Status)
}

// applyDeadFocusGuard forces focus back to RAIL when r is a dead-session (or
// Dead) row and focus is currently in AGENT or COORD. Without this, navigating
// onto a row whose PTY returns 404 leaves focus stuck in a pane — the raw-input
// layer forwards every subsequent keystroke to the dead PTY and swallows it from
// tcell, making the view appear frozen (BUG-018). Must run on the tview event
// loop (same requirement as applyRailSelection).
func (a *App) applyDeadFocusGuard(r *roleEntry) {
	if !roleInputDead(r) {
		return
	}
	if a.focus == nil || a.focus.State() == FocusRAIL {
		return
	}
	a.focus.ToRAIL()
	a.OnFocusChanged(FocusRAIL)
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
		a.updateCoordDetails(r)
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
			if r.ArgusTaskID != "" && !r.Dead {
				a.rebindAgent(r.ArgusTaskID)
			} else if r.Dead {
				// Dead task: clear the agent pane to its placeholder so no
				// 404-ing PTY is ever bound and keystrokes have nowhere to go.
				a.rebindAgent("")
			}
			// Mark the agent pane as freelancer BEFORE setBodyMode calls
			// OnFocusChanged, which may trigger maybeAutoReattachPane. The flag
			// prevents auto-reattach on focus-bump (BUG-009): freelancers have no
			// hera binding and the auto-reattach path can hang on them. The
			// operator presses Enter to reattach a dead-session freelancer manually
			// via OnReattach (the mutation bridge's Enter path).
			a.mu.Lock()
			a.agentIsFreelancer = true
			a.mu.Unlock()
			a.setBodyMode(false, true)
			// BUG-018: if the freelancer's PTY session is gone (dead-session),
			// force focus back to RAIL so keystrokes drive rail navigation instead
			// of being forwarded to a dead PTY that returns HTTP 404.
			a.applyDeadFocusGuard(r)
			return
		}
		if r.childOrch != nil {
			// A sub-coordinator IS a coordinator: full-width HERA bound to the
			// sub-coord's OWN argus task (its own coordinator PTY), NOT the
			// parent orchestrator's coord. The child orchestrator's CoordTaskID
			// is, by construction, this role's ArgusTaskID (the multi-binding
			// join key); prefer it and fall back to the role's task.
			coordTask := r.childOrch.CoordTaskID
			if coordTask == "" {
				coordTask = r.ArgusTaskID
			}
			a.rebindCoord(coordTask)
			a.updateCoordDetails(r.childOrch)
			a.setBodyMode(true, false)
			return
		}
		// Locate the orchestrator that owns this role. resolveSubCoordinators
		// removes child orchestrators from the top-level list and nests them
		// under roleEntry.childOrch, so we must search the full tree rather
		// than just a.pieces.rail.orchestrators.
		orch := findOrchestratorByID(a.pieces.rail.orchestrators, r.OrchestratorID)
		var coordTask string
		if orch != nil {
			coordTask = orch.CoordTaskID
		}
		// Always rebind COORD to the selected project's coord task — including
		// "" when the project has no coord. Otherwise the HERA pane would keep
		// showing the PREVIOUS project's coordinator (the split stays, but bound
		// to a foreign coord). rebindCoord("") clears it to its placeholder.
		a.rebindCoord(coordTask)
		if r.RoleKind == string(db.KindCoordinator) {
			// A sub-coordinator selection is coordinator mode: full-width HERA.
			// Describe the orchestrator this coord role belongs to (childOrch
			// was nil here — a degenerate coord row without its own child
			// orchestrator).
			if orch != nil {
				a.updateCoordDetails(orch)
			}
			a.setBodyMode(true, false)
			return
		}
		// Worker/agent: HERA + AGENT split.
		if r.ArgusTaskID != "" && !r.Dead {
			a.rebindAgent(r.ArgusTaskID)
		} else if r.Dead {
			// Dead task: clear the agent pane to its placeholder so no
			// 404-ing PTY is ever bound and keystrokes have nowhere to go.
			a.rebindAgent("")
		}
		a.setBodyMode(true, true)
		// BUG-018: if the worker's PTY session is gone (dead-session), force
		// focus back to RAIL so keystrokes drive rail navigation instead of
		// being forwarded to a dead PTY that returns HTTP 404.
		a.applyDeadFocusGuard(r)
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
// full canvas. Called after a pane rebind, a mode switch, or a fullscreen change.
//
// When fullscreen is active (BUG-027), only the fullscreen pane is composed —
// the rail and the other pane are omitted so the selected pane fills the body.
func (a *App) refreshBody() {
	body := a.pieces.body
	if body == nil {
		return
	}
	a.mu.Lock()
	coordPresent := a.coordPresent
	agentPresent := a.agentPresent
	fullscreenActive := a.fullscreenActive
	fullscreenPane := a.fullscreenPane
	a.mu.Unlock()

	body.Clear()

	if fullscreenActive {
		switch fullscreenPane {
		case FocusCOORD:
			if a.pieces.coord != nil {
				body.AddItem(a.pieces.coord, 0, 1, false)
			}
		case FocusAGENT:
			if a.pieces.agent != nil {
				body.AddItem(a.pieces.agent, 0, 1, false)
			}
		}
		return
	}

	body.AddItem(a.pieces.rail, RailWidth, 0, false)
	switch {
	case coordPresent && !agentPresent && a.pieces.details != nil:
		// Coordinator mode: HERA (wider) + Details on the right. Flex-weighted
		// so the HERA pane keeps the majority of the width and is never starved.
		// Agent mode (both panes) and freelance mode (agent only) never compose
		// the Details pane, so those layouts are unchanged.
		body.AddItem(a.pieces.coord, 0, coordDetailsHERAFlex, false)
		body.AddItem(a.pieces.details, 0, coordDetailsPaneFlex, false)
	default:
		if coordPresent {
			body.AddItem(a.pieces.coord, 0, 1, false)
		}
		if agentPresent {
			body.AddItem(a.pieces.agent, 0, 1, false)
		}
	}
}

// updateCoordDetails rebuilds the Details pane's metadata for the selected
// coordinator (an orchestrator header's orchEntry, or a sub-coordinator's
// childOrch). Runs on the tview event loop (called from applyRailSelection);
// buildCoordDetails issues only local SQLite reads. No-op when the pane or DB
// is absent (tests that don't exercise rendering).
func (a *App) updateCoordDetails(orch *orchEntry) {
	if a.pieces.details == nil || orch == nil || a.database == nil {
		return
	}
	cd, err := buildCoordDetails(context.Background(), a.database, orch)
	if err != nil {
		return
	}
	a.pieces.details.SetDetails(cd)
}

// OnPaneDead is called by PaneForwarder when PostTaskInput returns
// ErrNoTaskInput (HTTP 404) — the task's PTY session ended while the operator
// was actively typing in the pane. Shows the REATTACHING splash and triggers a
// background session restart so the operator stays in the pane with progress
// feedback (BUG-008).
//
// Runs from the forwarder's sender goroutine; bounces to the tview event loop
// via QueueUpdateDraw. Satisfies paneDeadNotifier.
func (a *App) OnPaneDead(taskID string) {
	if taskID == "" {
		return
	}
	// Fast check outside event loop: is this task even bound to a pane?
	a.mu.Lock()
	boundToPane := a.coordTask == taskID || a.agentTask == taskID
	a.mu.Unlock()
	if !boundToPane || a.app == nil {
		return
	}
	go a.app.QueueUpdateDraw(func() { a.applyDeadPaneFocusGuard(taskID) })
}

// applyDeadPaneFocusGuard handles a pane's PTY session dying while the operator
// is actively focused on it (BUG-008). This is the event-loop half of
// OnPaneDead (extracted for testability).
//
// Shows the REATTACHING splash and auto-triggers a background restart when a
// trigger is wired (production). No-op when the pane is already reattaching
// (avoids re-triggering a concurrent restart) or when no trigger is wired
// (tests without a session). Does NOT snap to RAIL — the REATTACHING splash is
// the only dead-pane UX.
//
// Must run on the tview event loop.
func (a *App) applyDeadPaneFocusGuard(taskID string) {
	if a.focus == nil || a.focus.State() == FocusRAIL {
		return
	}
	// Re-check while on the event loop: the pane may have been rebound since
	// the 404 fired (j/k navigation away), in which case the dead task is no
	// longer the focused target and we must not disrupt focus.
	state := a.focus.State()
	a.mu.Lock()
	focusedTaskDead := (state == FocusCOORD && a.coordTask == taskID) ||
		(state == FocusAGENT && a.agentTask == taskID)
	var pane *pinnedTerminalPane
	switch state {
	case FocusCOORD:
		pane = a.pieces.coord
	case FocusAGENT:
		pane = a.pieces.agent
	}
	a.mu.Unlock()
	if !focusedTaskDead {
		return
	}
	// Show splash + trigger reattach. No-op when already reattaching or when
	// no trigger is wired (tests without a live session).
	if pane != nil && !pane.reattaching && a.onDeadPaneReattach != nil {
		pane.SetReattaching(true, "connecting to agent...")
		a.scheduleSubtitleUpdate(taskID)
		a.onDeadPaneReattach(taskID)
	}
}

// OnTaskReattached is called after an argus task session is successfully
// restarted (BUG-053 reattach). The new session starts at the argus default
// (80×24); without intervention, ProxyManager's "already applied" resize flag
// silently skips the next ResizeTask dispatch (it thinks the previous session's
// size is already in effect), leaving cursor positions wrong until the operator
// manually resizes the terminal.
//
// This method:
//  1. Resets ProxyManager's applied flag via the paneResizeInvalidator optional
//     interface so the next dispatch reaches argus.
//  2. Schedules clearReattachAndResize on the tview event loop, enforcing a
//     minimum 1-second splash hold before the fresh rebind fires. This prevents
//     a fast reattach from causing a disorienting splash flicker.
//
// Satisfies the paneReattachNotifier interface used by the mutation bridge.
func (a *App) OnTaskReattached(taskID string) {
	if taskID == "" {
		return
	}
	// Step 1: clear the ProxyManager's applied flag so the next resize
	// dispatch reaches argus instead of being short-circuited. Synchronous.
	if ri, ok := a.src.(paneResizeInvalidator); ok {
		ri.InvalidateResize(taskID)
	}
	if a.app == nil {
		return
	}
	// Step 2: schedule clear + fresh rebind on the event loop. The goroutine
	// wrapper is non-blocking (same pattern as the reflow callbacks) so it
	// does not block the mutation bridge while the tview queue drains.
	go a.app.QueueUpdateDraw(func() {
		a.mu.Lock()
		var pane *pinnedTerminalPane
		if a.coordTask == taskID && a.pieces.coord != nil {
			pane = a.pieces.coord
		} else if a.agentTask == taskID && a.pieces.agent != nil {
			pane = a.pieces.agent
		}
		a.mu.Unlock()
		if pane == nil {
			return
		}
		// Hold the splash for at least 1 second from when it appeared so a
		// fast reattach does not cause a disorienting flicker (Aaron's request).
		if remaining := time.Second - time.Since(pane.reattachSince); remaining > 0 {
			taskIDCopy := taskID
			time.AfterFunc(remaining, func() {
				go a.app.QueueUpdateDraw(func() { a.clearReattachAndResize(taskIDCopy) })
			})
			return
		}
		a.clearReattachAndResize(taskID)
	})
}

// moveFocusToPaneAfterReattach moves keyboard focus from the RAIL to the agent
// pane (isAgent=true) or coord pane (isAgent=false) after a reattach completes.
// Called by clearReattachAndResize to complete the focus handoff that
// StartPaneReattach deferred. Must run on the tview event loop.
func (a *App) moveFocusToPaneAfterReattach(isAgent bool) {
	if a.focus != nil {
		if isAgent {
			a.focus.JumpToAGENT()
		} else {
			a.focus.JumpToCOORD()
		}
	}
	target := FocusAGENT
	if !isAgent {
		target = FocusCOORD
	}
	a.OnFocusChanged(target)
}

// clearReattachAndResize clears the REATTACHING splash for taskID's pane and
// replaces it with a fresh-subscribed pane (blank emulator) so the old
// session's ring buffer does not flash before new output arrives. The fresh
// rebind also dispatches SIGWINCH at the correct pane dimensions. If the
// layout has not run yet (GetRect returns zero), only the splash is cleared.
// Must run on the tview event loop.
func (a *App) clearReattachAndResize(taskID string) {
	a.mu.Lock()
	var cols, rows int
	var isCoord, isAgent bool
	if a.coordTask == taskID && a.pieces.coord != nil {
		_, _, w, h := a.pieces.coord.GetRect()
		cols, rows = w-2, h-2
		isCoord = true
	} else if a.agentTask == taskID && a.pieces.agent != nil {
		_, _, w, h := a.pieces.agent.GetRect()
		cols, rows = w-2, h-2
		isAgent = true
	}
	a.mu.Unlock()
	if !isCoord && !isAgent {
		return
	}
	if cols > 0 && rows > 0 {
		// BUG-012: discard the existing proxy ring buffer before creating the
		// fresh pane. Without this, the new pane's SubscribeTask call returns a
		// snapshot containing old-session content followed by new-session bytes,
		// and the emulator replays it from the beginning — placing the cursor at
		// a position from the old session (e.g. mid-line inside the "Resume this
		// session with: ..." argus message) rather than at the new prompt.
		if sr, ok := a.src.(paneSubscriptionResetter); ok {
			sr.ResetSubscription(taskID)
		}
		// Replace the pane with a fresh one (blank emulator, re-subscribed to the
		// new session's output). The fresh pane's lastDesiredCols/Rows match
		// cols/rows, so its first Draw short-circuits the resize dispatch; we send
		// it explicitly below to pair with the pre-queued InvalidateResize.
		if isAgent {
			a.forceRebindAgent(taskID, cols, rows)
		} else {
			a.forceRebindCoord(taskID, cols, rows)
		}
		// BUG-012: explicit SIGWINCH for the new session (see OnTaskReattached).
		a.src.ResizeTask(taskID, cols, rows)
		a.moveFocusToPaneAfterReattach(isAgent)
		return
	}
	// Layout not yet run (GetRect == 0) — just clear the splash so the operator
	// is not stuck looking at it forever.
	a.mu.Lock()
	var pane *pinnedTerminalPane
	if isCoord {
		pane = a.pieces.coord
	} else {
		pane = a.pieces.agent
	}
	a.mu.Unlock()
	if pane != nil {
		pane.SetReattaching(false, "")
	}
	a.moveFocusToPaneAfterReattach(isAgent)
}

// StartPaneReattach shows the REATTACHING splash on the pane bound to taskID
// and forces an immediate redraw so the splash renders before any cursor move.
// Focus stays on the RAIL during the splash; clearReattachAndResize moves focus
// to the pane once the new session is ready. Called by the mutation bridge when
// the operator presses Enter on a dead-session row (BUG-008 path 1). Satisfies
// the reattachPaneStarter interface. Runs on the tview event loop.
func (a *App) StartPaneReattach(taskID string) {
	if taskID == "" {
		return
	}
	// BUG-012: always fire any pending debounced selection immediately before
	// looking up the pane. Two cases both require this:
	//
	// (1) Pane not yet bound: the operator pressed Enter within the 120ms
	//     debounce window before applyRailSelection fired, so the pane is still
	//     bound to the previous task — fireSelectionNow binds it to taskID.
	//
	// (2) Pane bound but body mode wrong: the operator navigated to a coordinator
	//     header (switching the body to COORD-only), then back to the dead-session
	//     worker row within the debounce window. agentTask == taskID but
	//     agentPresent=false, so the splash would appear on an invisible pane.
	//     fireSelectionNow calls applyRailSelection → setBodyMode(true,true),
	//     making the agent pane visible before SetReattaching stamps reattachSince.
	//
	// No-op when no selection is pending (selectHasRef=false).
	a.fireSelectionNow()
	// BUG-012: unconditionally reset focus to RAIL after fireSelectionNow.
	// applyRailSelection can call setBodyMode → OnFocusChanged with a
	// non-RAIL state when the pending selection is a live task row (live rows
	// don't trigger applyDeadFocusGuard). Focus must stay on RAIL for the
	// entire splash period; clearReattachAndResize moves it to the pane.
	if a.focus != nil {
		a.focus.ToRAIL()
	}
	a.OnFocusChanged(FocusRAIL)

	a.mu.Lock()
	var pane *pinnedTerminalPane
	switch {
	case a.agentTask == taskID:
		pane = a.pieces.agent
	case a.coordTask == taskID:
		pane = a.pieces.coord
	}
	a.mu.Unlock()
	if pane == nil {
		return
	}
	pane.SetReattaching(true, "connecting to agent...")
	a.scheduleSubtitleUpdate(taskID)
	// Force a redraw on the next event-loop tick so the splash paints before any
	// cursor move. Without this, tview only redraws on the next input event,
	// leaving the old pane content visible until something else triggers a draw
	// (seconds later). The goroutine wrapper matches the codebase-wide pattern
	// for QueueUpdateDraw calls from within the event loop.
	if a.app != nil {
		go a.app.QueueUpdateDraw(func() {})
	}
	// Focus deliberately stays on the RAIL here. clearReattachAndResize moves
	// it to the pane once the new session is ready and correctly sized.
}

// ClearPaneReattach hides the REATTACHING splash on the pane bound to taskID.
// Called by the mutation bridge when a background reattach fails (BUG-008 path
// 1) so the operator is not stuck looking at a frozen splash. Thread-safe:
// schedules the update via QueueUpdateDraw. Satisfies reattachPaneStarter.
func (a *App) ClearPaneReattach(taskID string) {
	if taskID == "" || a.app == nil {
		return
	}
	go a.app.QueueUpdateDraw(func() {
		a.mu.Lock()
		var pane *pinnedTerminalPane
		switch {
		case a.coordTask == taskID:
			pane = a.pieces.coord
		case a.agentTask == taskID:
			pane = a.pieces.agent
		}
		a.mu.Unlock()
		if pane != nil {
			pane.SetReattaching(false, "")
		}
	})
}

// scheduleSubtitleUpdate fires a subtitle change from "connecting to agent..."
// to "waiting for session..." on the reattaching splash after a 2-second delay.
// The update is a no-op if the pane has already finished reattaching by then.
func (a *App) scheduleSubtitleUpdate(taskID string) {
	if a.app == nil {
		return
	}
	taskIDCopy := taskID
	time.AfterFunc(2*time.Second, func() {
		go a.app.QueueUpdateDraw(func() {
			a.mu.Lock()
			var pane *pinnedTerminalPane
			switch {
			case a.agentTask == taskIDCopy:
				pane = a.pieces.agent
			case a.coordTask == taskIDCopy:
				pane = a.pieces.coord
			}
			a.mu.Unlock()
			if pane != nil {
				pane.SetReattachingSubtitle("waiting for session...")
			}
		})
	})
}

// maybeAutoReattachPane checks whether the pane that just received focus has a
// dead session and, if so, shows the REATTACHING splash and fires a background
// reattach (BUG-008 path 2: Ctrl+→ stepping into a dead pane). No-op when the
// pane is already reattaching, the task is alive, or no trigger is wired.
// Freelancer agent panes are skipped: they have no hera binding and the
// auto-reattach goroutine can hang when navigating to a dead-session freelancer
// row. The operator uses Enter (OnReattach) to manually reattach (BUG-009).
// Must run on the tview event loop (called from OnFocusChanged).
func (a *App) maybeAutoReattachPane(state FocusState) {
	if a.onDeadPaneReattach == nil {
		return
	}
	a.mu.Lock()
	var taskID string
	var pane *pinnedTerminalPane
	var isFreelancer bool
	if state == FocusCOORD {
		taskID = a.coordTask
		pane = a.pieces.coord
	} else {
		taskID = a.agentTask
		pane = a.pieces.agent
		isFreelancer = a.agentIsFreelancer
	}
	a.mu.Unlock()
	if taskID == "" || pane == nil || pane.reattaching {
		return
	}
	// BUG-009: freelancer rows have no hera binding; skip auto-reattach so
	// navigating to a dead-session freelancer never hangs hera. The operator
	// can still press Enter to trigger OnReattach (the mutation bridge's Enter
	// path) which handles freelancers correctly.
	//
	// BUG-012: for dead-session freelancers reached via Ctrl+→, also snap focus
	// back to RAIL so keystrokes don't silently go to a dead PTY. Only snap when
	// the task is actually dead-session (alive freelancers enter normally).
	if isFreelancer {
		if prov, ok := a.src.(TaskStateProvider); ok {
			if st, stOK := prov.TaskState(taskID); stOK && !taskStatusAlive(st.Status) {
				if a.focus != nil && a.focus.State() != FocusRAIL {
					a.focus.ToRAIL()
					a.OnFocusChanged(FocusRAIL)
				}
			}
		}
		return
	}
	prov, ok := a.src.(TaskStateProvider)
	if !ok {
		return
	}
	st, ok := prov.TaskState(taskID)
	if !ok || taskStatusAlive(st.Status) {
		return
	}
	pane.SetReattaching(true, "connecting to agent...")
	a.scheduleSubtitleUpdate(taskID)
	a.onDeadPaneReattach(taskID)
}

// snapToRAILFromReattach clears the REATTACHING splash on taskID's pane and
// snaps focus to RAIL. Called on auto-reattach failure (paths 2/3) so the
// operator is not stuck in a pane with a frozen splash. Runs on the tview
// event loop.
func (a *App) snapToRAILFromReattach(taskID string) {
	a.mu.Lock()
	var pane *pinnedTerminalPane
	switch {
	case a.coordTask == taskID:
		pane = a.pieces.coord
	case a.agentTask == taskID:
		pane = a.pieces.agent
	}
	a.mu.Unlock()
	if pane != nil {
		pane.SetReattaching(false, "")
	}
	if a.focus != nil && a.focus.State() != FocusRAIL {
		a.focus.ToRAIL()
		a.OnFocusChanged(FocusRAIL)
	}
}

// refreshDetailsForCurrentSelection re-derives the Details pane from whatever
// coordinator the rail currently shows. Called at the tail of populateRail so
// a DAO-driven repopulate (joining worker, status change) reflects in the pane
// even when the cursor does not move — the normal selection-change callback is
// a no-op when the cursor index is unchanged, leaving the pane on a stale
// snapshot (BUG-037). Runs on the tview event loop. No-op when the rail or
// Details pane are absent, or when no coordinator is currently selected.
func (a *App) refreshDetailsForCurrentSelection() {
	if a.pieces.details == nil || a.pieces.rail == nil {
		return
	}
	switch ref := a.pieces.rail.CurrentRef().(type) {
	case *orchEntry:
		a.updateCoordDetails(ref)
	case *roleEntry:
		if ref == nil {
			return
		}
		if ref.childOrch != nil {
			a.updateCoordDetails(ref.childOrch)
			return
		}
		if ref.RoleKind == string(db.KindCoordinator) {
			orch := findOrchestratorByID(a.pieces.rail.orchestrators, ref.OrchestratorID)
			if orch != nil {
				a.updateCoordDetails(orch)
			}
		}
	}
}
