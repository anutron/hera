package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// EndReasonUserDeleted is stamped on every binding ended by `^d`. The
// existing hera-coordination spec already prescribes this exact string
// for operator-initiated deletes.
const EndReasonUserDeleted = "user_deleted"

// DeleteRole handles `^d` against a role. It:
//  1. Ends the role's live binding (if any) with end_reason=user_deleted.
//  2. Sets archived_at on the role (the row survives; only "active"
//     visibility is removed).
//  3. Runs `git worktree remove --force` against the binding's worktree
//     path. Soft no-op if path is empty or directory missing.
//
// The role row is preserved so archive-visibility (`l`) and role-as-
// identity guarantees hold. Prompt and argus_project survive for a future
// resurrect.
func (s *Service) DeleteRole(ctx context.Context, id int64) error {
	role, err := s.DB.GetRoleByID(ctx, id)
	if err != nil {
		return fmt.Errorf("ops.DeleteRole: load %d: %w", id, err)
	}

	return s.deleteRoleInternal(ctx, role)
}

// deleteRoleInternal is the shared body of DeleteRole and the cascade
// inside DeleteOrchestrator. Operates on a pre-loaded role to avoid a
// second DB read in the cascade case.
func (s *Service) deleteRoleInternal(ctx context.Context, role *Role) error {
	// Resolve the role's binding to recover its argus task id + worktree path.
	// Prefer the LIVE binding (which we must also END); fall back to the most
	// recent ENDED binding so an ARCHIVED role still surrenders its task for
	// destruction. Archiving a task ends its binding (end_reason=argus_archived)
	// while keeping the argus_task_id, so an archived role fails the live-only
	// lookup even when its argus task is still alive. Without the fallback, an
	// archived child role swept up by a `^d` subtree teardown would leave its
	// live argus task orphaned — exactly the freelancer spray BUG-021 reports.
	liveBnd, err := s.DB.GetLiveBindingByRole(ctx, role.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("ops.DeleteRole: live binding lookup for role %d: %w", role.ID, err)
	}

	worktreePath := ""
	argusTaskID := ""
	if liveBnd != nil {
		worktreePath = liveBnd.WorktreePath
		argusTaskID = liveBnd.ArgusTaskID
		if err := s.DB.EndBinding(ctx, liveBnd.ID, EndReasonUserDeleted); err != nil {
			return fmt.Errorf("ops.DeleteRole: end binding %d: %w", liveBnd.ID, err)
		}
	} else {
		// No live binding — recover the argus task id from the latest ended
		// binding (archived-role case). ErrNotFound means the role was never
		// bound, so there is nothing to destroy argus-side.
		latest, err := s.DB.GetLatestBindingByRole(ctx, role.ID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("ops.DeleteRole: latest binding lookup for role %d: %w", role.ID, err)
		}
		if latest != nil {
			worktreePath = latest.WorktreePath
			argusTaskID = latest.ArgusTaskID
		}
	}

	if !role.Archived {
		if err := s.DB.ArchiveRole(ctx, role.ID); err != nil {
			return fmt.Errorf("ops.DeleteRole: archive role %d: %w", role.ID, err)
		}
	}

	// Destroy the argus task — argus stops the session and cleans up the
	// task's git worktree AND branch server-side (handleDeleteTask). This is
	// the extra destruction `^d` promises over `a` archive. A task that is
	// already gone (404) is treated as success by the argus client, so a
	// cascade won't abort on a sibling already deleted out-of-band.
	if argusTaskID != "" {
		s.logf("delete: destroying argus task %q (worktree+branch)", argusTaskID)
		if err := s.Argus.DeleteTask(ctx, argusTaskID); err != nil {
			return fmt.Errorf("ops.DeleteRole: argus delete task %s: %w", argusTaskID, err)
		}
	}

	// Local worktree remove is a defensive fallback: argus already cleaned
	// the worktree above, so this is a soft no-op in the common case
	// (directory already gone). It still runs to cover bindings whose argus
	// task vanished out-of-band but whose worktree lingers on disk. Best-effort
	// (BUG-018 / BUG-021): a stale or unremovable worktree must never abort the
	// delete — least of all a subtree cascade where one bad worktree would
	// strand every sibling task. Log and proceed.
	if err := s.removeWorktree(ctx, worktreePath); err != nil {
		s.logf("delete: worktree remove failed for role %d, continuing: %v", role.ID, err)
	}
	return nil
}

// DeleteOrchestrator handles `^d` against an orchestrator. It tears down the
// orchestrator's ENTIRE subtree: every descendant orchestrator reachable via
// shared sub-coordinator argus tasks, every role under each (active AND
// archived), and every bound argus task + worktree. Then it physically removes
// each orchestrator row from the DB.
//
// Two enumeration details are load-bearing for BUG-021 ("deleting a
// coordinator orphans its workers' argus tasks into freelancers"):
//
//   - SUBTREE, not just this orchestrator. A sub-coordinator's workers live in
//     a descendant orchestrator. Deleting only this orchestrator's roles would
//     leave those descendant tasks alive and unmanaged — freelancer spray.
//   - INCLUSIVE roles, not just active ones. The physical DELETE cascades to
//     archived role rows too, so their still-alive argus tasks must be
//     destroyed here; otherwise an archived worker's task is orphaned (often
//     with an already-removed worktree — the "zombie freelancer" of the bug).
//
// The physical DELETE cascades (via ON DELETE CASCADE) to roles and bindings,
// so no ghost row remains in the rail — neither in the active section nor in
// the Archive section. This is intentionally more destructive than `a` (which
// only archives, preserving rows for resurrection); `^d` tears down the whole
// subtree permanently.
func (s *Service) DeleteOrchestrator(ctx context.Context, id int64) error {
	if _, err := s.DB.GetOrchestratorByID(ctx, id); err != nil {
		return fmt.Errorf("ops.DeleteOrchestrator: load %d: %w", id, err)
	}

	// Snapshot the subtree BEFORE any deletion — the BFS follows LIVE coord
	// bindings, which the cascade below ends. Capturing IDs up front keeps the
	// frontier intact for the whole teardown.
	orchIDs, err := s.DB.SubtreeOrchIDs(ctx, id)
	if err != nil {
		return fmt.Errorf("ops.DeleteOrchestrator: subtree of %d: %w", id, err)
	}

	for _, orchID := range orchIDs {
		roles, err := s.DB.ListRolesByOrchestratorInclusive(ctx, orchID)
		if err != nil {
			return fmt.Errorf("ops.DeleteOrchestrator: list roles for %d: %w", orchID, err)
		}
		for _, role := range roles {
			if err := s.deleteRoleInternal(ctx, role); err != nil {
				return fmt.Errorf("ops.DeleteOrchestrator: cascade role %d: %w", role.ID, err)
			}
		}
	}

	// Physical delete every orchestrator in the subtree. Removes each row and,
	// via ON DELETE CASCADE, all its roles and bindings. Unlike
	// ArchiveOrchestrator, this leaves no row in the DB — the rail's
	// ListInclusive will not return them and the Archive section shows no ghost.
	for _, orchID := range orchIDs {
		if err := s.DB.DeleteOrchestratorByID(ctx, orchID); err != nil && !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("ops.DeleteOrchestrator: delete %d: %w", orchID, err)
		}
	}
	return nil
}

// removeWorktree dispatches to the WorktreeRemover dependency, with
// soft-no-op handling and audit logging baked in.
//
// Empty path → log and skip.
// Path missing on disk → log and skip (the spec scenario
// "Worktree missing is soft no-op" prescribes this).
// Path exists → delegate to s.WorktreeRemover.Remove (production runs
// `git worktree remove --force` via os/exec; tests substitute a fake).
//
// Every invocation is logged with the worktree path per design.md
// Risks (audit trail for destructive operations).
func (s *Service) removeWorktree(ctx context.Context, worktreePath string) error {
	if worktreePath == "" {
		s.logf("worktree remove: skipping empty path")
		return nil
	}
	if _, err := os.Stat(worktreePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.logf("worktree remove: skipping missing path %q", worktreePath)
			return nil
		}
		return fmt.Errorf("stat %q: %w", worktreePath, err)
	}

	// If the directory exists but has no .git file, argus already cleaned up
	// the worktree internals. Skip the git command — it would exit 128 —
	// and proceed with the DB deletion below.
	gitInfo, err := os.Stat(filepath.Join(worktreePath, ".git"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.logf("worktree remove: skipping %q (no .git, already cleaned up)", worktreePath)
			return nil
		}
		return fmt.Errorf("stat .git in %q: %w", worktreePath, err)
	}

	// A linked worktree's .git is a FILE pointing at the parent repo's admin
	// entry (.git/worktrees/<name>). If an earlier cleanup pruned that admin
	// entry while the worktree dir + .git file lingered, `git worktree remove`
	// exits 128 ("fatal: not a git repository: .../.git/worktrees/<name>").
	// Detect the detached state and soft-skip — there is nothing for git to
	// remove. (BUG-018: the exact failure that aborted a bulk prune.)
	if !gitInfo.IsDir() && !worktreeAdminEntryExists(worktreePath) {
		s.logf("worktree remove: skipping %q (git admin entry gone, already detached)", worktreePath)
		return nil
	}

	s.logf("worktree remove: %q", worktreePath)
	if s.WorktreeRemover == nil {
		return fmt.Errorf("no WorktreeRemover configured")
	}
	return s.WorktreeRemover.Remove(ctx, worktreePath)
}

// worktreeAdminEntryExists reports whether a linked worktree's git admin
// entry still exists. A linked worktree's .git is a one-line file of the form
// "gitdir: <path-to-parent/.git/worktrees/<name>>". When that target is gone
// (e.g. pruned by an earlier cleanup) `git worktree remove` fails with exit
// 128, so callers should soft-skip.
//
// Fails open: any read or parse problem returns true so git still gets a
// chance to remove a genuinely-removable worktree rather than the guard
// silently skipping it.
func worktreeAdminEntryExists(worktreePath string) bool {
	data, err := os.ReadFile(filepath.Join(worktreePath, ".git"))
	if err != nil {
		return true // can't tell — let git try
	}
	line := strings.TrimSpace(string(data))
	const prefix = "gitdir:"
	if !strings.HasPrefix(line, prefix) {
		return true // unexpected format — let git try
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if gitDir == "" {
		return true
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(worktreePath, gitDir)
	}
	_, statErr := os.Stat(gitDir)
	return statErr == nil
}

// logf is a nil-safe wrapper around the Logger dep. Audit logs are
// non-critical — running without a logger should still let tests pass.
func (s *Service) logf(format string, args ...any) {
	if s.Logger == nil {
		return
	}
	s.Logger.Printf(format, args...)
}

// ExecWorktreeRemover is the production WorktreeRemover implementation.
// Invokes `git -C <path> worktree remove --force <path>` so the command
// runs from inside a known git working tree (the worktree itself); git
// resolves the parent repo and removes the linked worktree.
type ExecWorktreeRemover struct{}

// Remove runs `git worktree remove --force <path>` against the given
// worktree directory. Returns the wrapped command error (with combined
// output) on non-zero exit.
func (ExecWorktreeRemover) Remove(ctx context.Context, worktreePath string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", worktreePath, "worktree", "remove", "--force", worktreePath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree remove %q: %w (output: %s)", worktreePath, err, string(out))
	}
	return nil
}
