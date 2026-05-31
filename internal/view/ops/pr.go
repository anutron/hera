package ops

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// OpenPR implements `^p`: open a pull request for the selected role's bound
// task via the host git flow. It resolves the role's live binding to find the
// worktree path, then delegates to the configured PRCreator (production:
// `gh pr create` via os/exec from the worktree).
//
// Substrate note: argus exposes no PR endpoint, so `^p` runs entirely on the
// host through os/exec — the hera daemon is unsandboxed under launchd, the
// same property the worktree-remove flow relies on. Opening a real PR can't be
// exercised headless in a unit test, so the behavior is verified against a
// fake PRCreator; the exec path is a thin wrapper covered by a construction
// smoke test.
//
// Returns the PR URL (when the creator reports one) or an error. Errors when
// the role has no live binding / worktree (nothing to open a PR from) or when
// no PRCreator is wired.
func (s *Service) OpenPR(ctx context.Context, roleID int64) (string, error) {
	if s.PR == nil {
		return "", fmt.Errorf("ops.OpenPR: no PR flow configured")
	}
	bnd, err := s.DB.GetLiveBindingByRole(ctx, roleID)
	if err != nil {
		return "", fmt.Errorf("ops.OpenPR: role %d has no live binding: %w", roleID, err)
	}
	if bnd.WorktreePath == "" {
		return "", fmt.Errorf("ops.OpenPR: role %d binding has no worktree path", roleID)
	}
	s.logf("open-pr: %q", bnd.WorktreePath)
	url, err := s.PR.CreatePR(ctx, bnd.WorktreePath)
	if err != nil {
		return "", fmt.Errorf("ops.OpenPR: create PR from %q: %w", bnd.WorktreePath, err)
	}
	return url, nil
}

// ExecPRCreator is the production PRCreator. It runs `gh pr create --fill`
// from the worktree directory; gh pushes the branch and opens the PR against
// the repo's default base, printing the PR URL on stdout. The daemon is
// unsandboxed under launchd so it can reach gh + the host git remotes.
//
// `--fill` derives title/body from the commits so the headless flow needs no
// operator input; the operator confirms the action via the `^p` modal first.
type ExecPRCreator struct{}

// CreatePR runs `gh pr create --fill` in worktreePath and returns the trimmed
// stdout (the PR URL on success).
func (ExecPRCreator) CreatePR(ctx context.Context, worktreePath string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", "pr", "create", "--fill")
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gh pr create in %q: %w (output: %s)", worktreePath, err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}
