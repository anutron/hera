package ops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	bnd, err := s.DB.GetLiveBindingByRole(ctx, role.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("ops.DeleteRole: live binding lookup for role %d: %w", role.ID, err)
	}

	worktreePath := ""
	argusTaskID := ""
	if bnd != nil {
		worktreePath = bnd.WorktreePath
		argusTaskID = bnd.ArgusTaskID
		if err := s.DB.EndBinding(ctx, bnd.ID, EndReasonUserDeleted); err != nil {
			return fmt.Errorf("ops.DeleteRole: end binding %d: %w", bnd.ID, err)
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
	// task vanished out-of-band but whose worktree lingers on disk.
	if err := s.removeWorktree(ctx, worktreePath); err != nil {
		return fmt.Errorf("ops.DeleteRole: worktree remove: %w", err)
	}
	return nil
}

// DeleteOrchestrator handles `^d` against an orchestrator. Cascades the
// role-delete path to every role under the orchestrator (active and
// already-archived alike — we still want to remove leftover worktrees
// on already-archived roles), then archives the orchestrator row.
func (s *Service) DeleteOrchestrator(ctx context.Context, id int64) error {
	orch, err := s.DB.GetOrchestratorByID(ctx, id)
	if err != nil {
		return fmt.Errorf("ops.DeleteOrchestrator: load %d: %w", id, err)
	}

	roles, err := s.DB.ListRolesByOrchestrator(ctx, id)
	if err != nil {
		return fmt.Errorf("ops.DeleteOrchestrator: list roles: %w", err)
	}
	for _, role := range roles {
		if err := s.deleteRoleInternal(ctx, role); err != nil {
			return fmt.Errorf("ops.DeleteOrchestrator: cascade role %d: %w", role.ID, err)
		}
	}

	if !orch.Archived {
		if err := s.DB.ArchiveOrchestrator(ctx, id); err != nil {
			return fmt.Errorf("ops.DeleteOrchestrator: archive: %w", err)
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
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			s.logf("worktree remove: skipping %q (no .git, already cleaned up)", worktreePath)
			return nil
		}
		return fmt.Errorf("stat .git in %q: %w", worktreePath, err)
	}

	s.logf("worktree remove: %q", worktreePath)
	if s.WorktreeRemover == nil {
		return fmt.Errorf("no WorktreeRemover configured")
	}
	return s.WorktreeRemover.Remove(ctx, worktreePath)
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
