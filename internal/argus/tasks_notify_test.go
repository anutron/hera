package argus

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
)

// TestNotifyTask_Submitted verifies a 202 response with state="submitted"
// is decoded correctly.
func TestNotifyTask_Submitted(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody NotifyInput
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"delivery_id":"42","state":"submitted"}`)
	})
	defer srv.Close()

	in := NotifyInput{
		Text:       "[hera from coord] msg #42 — do the thing",
		Submit:     true,
		DeliveryID: "42",
		DeadlineMs: 300000,
	}
	resp, err := c.NotifyTask(context.Background(), "T1", in)
	if err != nil {
		t.Fatalf("NotifyTask: %v", err)
	}
	if gotMethod != "POST" {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/tasks/T1/notify" {
		t.Fatalf("path = %q, want /api/tasks/T1/notify", gotPath)
	}
	if gotBody.Text != in.Text {
		t.Fatalf("body.text = %q, want %q", gotBody.Text, in.Text)
	}
	if !gotBody.Submit {
		t.Fatal("body.submit = false, want true")
	}
	if gotBody.DeliveryID != "42" {
		t.Fatalf("body.delivery_id = %q, want 42", gotBody.DeliveryID)
	}
	if gotBody.DeadlineMs != 300000 {
		t.Fatalf("body.deadline_ms = %d, want 300000", gotBody.DeadlineMs)
	}
	if resp.State != "submitted" {
		t.Fatalf("state = %q, want submitted", resp.State)
	}
	if resp.DeliveryID != "42" {
		t.Fatalf("delivery_id = %q, want 42", resp.DeliveryID)
	}
}

// TestNotifyTask_Pending verifies a 202 response with state="pending" is
// decoded correctly.
func TestNotifyTask_Pending(t *testing.T) {
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"delivery_id":"7","state":"pending"}`)
	})
	defer srv.Close()

	resp, err := c.NotifyTask(context.Background(), "T2", NotifyInput{
		Text:       "ping",
		Submit:     false,
		DeliveryID: "7",
		DeadlineMs: 60000,
	})
	if err != nil {
		t.Fatalf("NotifyTask: %v", err)
	}
	if resp.State != "pending" {
		t.Fatalf("state = %q, want pending", resp.State)
	}
}

// TestNotifyTask_NoSession verifies a 404 surfaces as ErrNoTaskInput.
func TestNotifyTask_NoSession(t *testing.T) {
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"no active session"}`)
	})
	defer srv.Close()

	_, err := c.NotifyTask(context.Background(), "T3", NotifyInput{
		Text: "ping", Submit: true, DeliveryID: "1", DeadlineMs: 300000,
	})
	if !errors.Is(err, ErrNoTaskInput) {
		t.Fatalf("err = %v, want ErrNoTaskInput", err)
	}
}

// TestNotifyTask_ServerError verifies a 500 surfaces as an error (not silenced).
func TestNotifyTask_ServerError(t *testing.T) {
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"boom"}`)
	})
	defer srv.Close()

	_, err := c.NotifyTask(context.Background(), "T4", NotifyInput{
		Text: "ping", Submit: true, DeliveryID: "2", DeadlineMs: 300000,
	})
	if err == nil {
		t.Fatal("want error on 500, got nil")
	}
}

// TestNotifyTask_PathRouting verifies the correct route is targeted.
func TestNotifyTask_PathRouting(t *testing.T) {
	var gotPath string
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"delivery_id":"1","state":"pending"}`)
	})
	defer srv.Close()

	_, err := c.NotifyTask(context.Background(), "my-task-id", NotifyInput{
		Text: "x", Submit: true, DeliveryID: "1", DeadlineMs: 300000,
	})
	if err != nil {
		t.Fatalf("NotifyTask: %v", err)
	}
	if gotPath != "/api/tasks/my-task-id/notify" {
		t.Fatalf("path = %q, want /api/tasks/my-task-id/notify", gotPath)
	}
}

// TestCancelNotify_OK verifies a 200 response is handled correctly.
func TestCancelNotify_OK(t *testing.T) {
	var gotMethod, gotPath string
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{"delivery_id":"42","cancelled":true}`)
	})
	defer srv.Close()

	if err := c.CancelNotify(context.Background(), "T1", "42"); err != nil {
		t.Fatalf("CancelNotify: %v", err)
	}
	if gotMethod != "DELETE" {
		t.Fatalf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/api/tasks/T1/notify/42" {
		t.Fatalf("path = %q, want /api/tasks/T1/notify/42", gotPath)
	}
}

// TestCancelNotify_NotFound treats 404 as success (already delivered or cancelled).
func TestCancelNotify_NotFound(t *testing.T) {
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"not found"}`)
	})
	defer srv.Close()

	if err := c.CancelNotify(context.Background(), "T1", "99"); err != nil {
		t.Fatalf("CancelNotify on 404 should be nil, got: %v", err)
	}
}

// TestCancelNotify_ServerError verifies a 500 surfaces as an error.
func TestCancelNotify_ServerError(t *testing.T) {
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":"boom"}`)
	})
	defer srv.Close()

	if err := c.CancelNotify(context.Background(), "T1", "3"); err == nil {
		t.Fatal("want error on 500, got nil")
	}
}

// TestCancelNotify_PathRouting verifies both task ID and delivery ID appear in the path.
func TestCancelNotify_PathRouting(t *testing.T) {
	var gotPath string
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{"delivery_id":"99","cancelled":false}`)
	})
	defer srv.Close()

	if err := c.CancelNotify(context.Background(), "my-task", "99"); err != nil {
		t.Fatalf("CancelNotify: %v", err)
	}
	if gotPath != "/api/tasks/my-task/notify/99" {
		t.Fatalf("path = %q, want /api/tasks/my-task/notify/99", gotPath)
	}
}
