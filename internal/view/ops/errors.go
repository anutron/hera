package ops

import "errors"

// ErrNotFound is returned by DB.Get* methods when the requested row does
// not exist. The production adapter translates db.ErrNotFound to this
// sentinel so the ops layer does not import the db package's sentinel
// directly.
var ErrNotFound = errors.New("ops: not found")

// ValidationError is a user-facing error returned by ops methods that
// reject input the operator typed into a modal (duplicate name, empty
// name, etc.). The modal layer is expected to display Message verbatim
// and keep the modal open so the operator can correct the input.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

// validation builds a *ValidationError. Reads slightly nicer at call
// sites than the struct literal.
func validation(msg string) *ValidationError { return &ValidationError{Message: msg} }
