package argus

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsWorktreeMissing(t *testing.T) {
	// The exact 500 body argus emits when the task's worktree was deleted.
	wtMissing := &HTTPError{
		Method:     "POST",
		Path:       "/api/tasks/1/restart",
		StatusCode: 500,
		Body:       `{"error":"worktree path missing: /Users/aaron/.argus/worktrees/Hera/investigate-kick-rerender (delete the task or recreate the worktree)"}`,
	}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"worktree missing 500", wtMissing, true},
		{"wrapped worktree missing", fmt.Errorf("ops.ReattachAgent: %w", wtMissing), true},
		{"other 500", &HTTPError{StatusCode: 500, Body: `{"error":"boom"}`}, false},
		{"404 not found", &HTTPError{StatusCode: 404, Body: `{"error":"task not found"}`}, false},
		{"non-HTTP error", errors.New("worktree path missing somewhere"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsWorktreeMissing(tc.err); got != tc.want {
				t.Fatalf("IsWorktreeMissing(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsNotFound(t *testing.T) {
	notFound := &HTTPError{Method: "POST", Path: "/api/tasks/1/archive", StatusCode: 404, Body: `{"error":"task not found"}`}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain 404", notFound, true},
		{"wrapped 404", fmt.Errorf("ops: argus archive: %w", notFound), true},
		{"500", &HTTPError{StatusCode: 500}, false},
		{"non-HTTP error", errors.New("connection refused"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsNotFound(tc.err); got != tc.want {
				t.Fatalf("IsNotFound(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
