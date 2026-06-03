package view

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anutron/hera/internal/argus"
	"github.com/anutron/hera/internal/view/ops"
)

// newAdapterAndServer returns an argusAdapter pointed at an httptest server
// answering every request with the given status and body.
func newAdapterAndServer(t *testing.T, status int, body string) (*argusAdapter, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return newArgusAdapter(argus.New(srv.URL, "test-token")), srv
}

func TestArgusAdapter_ArchiveTask_404TranslatesToTaskGone(t *testing.T) {
	a, _ := newAdapterAndServer(t, http.StatusNotFound, `{"error":"task not found"}`)
	err := a.ArchiveTask(context.Background(), "1779994951479225000")
	if !errors.Is(err, ops.ErrArgusTaskGone) {
		t.Fatalf("ArchiveTask 404 should wrap ops.ErrArgusTaskGone, got: %v", err)
	}
}

func TestArgusAdapter_UnarchiveTask_404TranslatesToTaskGone(t *testing.T) {
	a, _ := newAdapterAndServer(t, http.StatusNotFound, `{"error":"task not found"}`)
	err := a.UnarchiveTask(context.Background(), "T1")
	if !errors.Is(err, ops.ErrArgusTaskGone) {
		t.Fatalf("UnarchiveTask 404 should wrap ops.ErrArgusTaskGone, got: %v", err)
	}
}

func TestArgusAdapter_GetSetTaskStatus_404TranslatesToTaskGone(t *testing.T) {
	a, _ := newAdapterAndServer(t, http.StatusNotFound, `{"error":"task not found"}`)
	if _, err := a.GetTaskStatus(context.Background(), "T1"); !errors.Is(err, ops.ErrArgusTaskGone) {
		t.Fatalf("GetTaskStatus 404 should wrap ops.ErrArgusTaskGone, got: %v", err)
	}
	if _, err := a.SetTaskStatus(context.Background(), "T1", "in_review"); !errors.Is(err, ops.ErrArgusTaskGone) {
		t.Fatalf("SetTaskStatus 404 should wrap ops.ErrArgusTaskGone, got: %v", err)
	}
}

func TestArgusAdapter_ArchiveTask_Non404PassesThrough(t *testing.T) {
	a, _ := newAdapterAndServer(t, http.StatusInternalServerError, `{"error":"boom"}`)
	err := a.ArchiveTask(context.Background(), "T1")
	if err == nil {
		t.Fatalf("expected an error")
	}
	if errors.Is(err, ops.ErrArgusTaskGone) {
		t.Fatalf("non-404 must not translate to ErrArgusTaskGone, got: %v", err)
	}
}
