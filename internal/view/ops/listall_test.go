package ops

import "testing"

func TestListAllState_StartsHidden(t *testing.T) {
	s := NewListAllState()
	if s.Visible() {
		t.Fatalf("expected hidden by default")
	}
}

func TestListAllState_Toggle(t *testing.T) {
	s := NewListAllState()
	if got := s.Toggle(); !got {
		t.Fatalf("first toggle should reveal (true), got %v", got)
	}
	if !s.Visible() {
		t.Fatalf("Visible should be true after first toggle")
	}
	if got := s.Toggle(); got {
		t.Fatalf("second toggle should hide (false), got %v", got)
	}
	if s.Visible() {
		t.Fatalf("Visible should be false after second toggle")
	}
}

func TestListAllState_NilSafeConstruction(t *testing.T) {
	// NewService allocates a fresh ListAllState if the caller didn't.
	s, _, _, _, _ := newTestService()
	if s.ListAll == nil {
		t.Fatalf("Service.ListAll must not be nil after NewService")
	}
	if s.ListAll.Visible() {
		t.Fatalf("Service.ListAll should start hidden")
	}
}
