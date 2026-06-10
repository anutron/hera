package ops

import "errors"

// ErrNotFound is returned by DB.Get* methods when the requested row does
// not exist. The production adapter translates db.ErrNotFound to this
// sentinel so the ops layer does not import the db package's sentinel
// directly.
var ErrNotFound = errors.New("ops: not found")

// ErrArgusTaskGone is the sentinel the production adapter wraps around an
// argus HTTP 404: the addressed task no longer exists argus-side (argus
// prunes tasks by deleting them outright, so a binding's recorded task id
// can dangle). Archive/unarchive verbs treat it as a successful no-op for
// the argus half of the operation — there is nothing left to (un)archive —
// while status stepping translates it into a plain "task no longer exists"
// error (a nonexistent task cannot be stepped).
var ErrArgusTaskGone = errors.New("ops: argus task no longer exists (pruned)")

// ErrWorktreeMissing is the sentinel ReattachAgent returns when argus refuses
// to restart a task because its worktree directory is gone (BUG-020). The task
// RECORD still exists (so it is NOT ErrArgusTaskGone), but with no working tree
// the agent backend cannot resume. The view layer recognizes it to offer the
// operator a "delete this orphaned coordinator" recovery path instead of
// surfacing an opaque argus 500.
var ErrWorktreeMissing = errors.New("ops: argus task worktree is gone — cannot restart")

// ValidationError is a user-facing error returned by ops methods that
// reject input the operator typed into a modal (duplicate name, empty
// name, etc.). The modal layer is expected to display Message verbatim
// and keep the modal open so the operator can correct the input.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

// validation builds a *ValidationError. Reads slightly nicer at call
// sites than the struct literal.
func validation(msg string) *ValidationError { return &ValidationError{Message: msg} }
