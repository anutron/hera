package argus

import (
	"errors"
	"fmt"
	"testing"
)

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
