package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/anutron/hera/internal/config"
)

// stubConfigStore is a tiny in-memory ConfigDAO replacement.
type stubConfigStore struct {
	mu     sync.Mutex
	values map[string]string
}

func newStubConfigStore(seed map[string]string) *stubConfigStore {
	cp := map[string]string{}
	for k, v := range seed {
		cp[k] = v
	}
	return &stubConfigStore{values: cp}
}

func (s *stubConfigStore) Set(_ context.Context, key, value string) error {
	s.mu.Lock()
	s.values[key] = value
	s.mu.Unlock()
	return nil
}

func (s *stubConfigStore) snapshot() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for k, v := range s.values {
		out[k] = v
	}
	return out
}

// errStubNotFound mimics db.ErrNotFound for the stub.
var errStubNotFound = stubErr("not found")

type stubErr string

func (e stubErr) Error() string { return string(e) }

// stubAutoInject captures SetAutoInjectEnabled calls.
type stubAutoInject struct {
	mu    sync.Mutex
	calls []bool
}

func (s *stubAutoInject) SetAutoInjectEnabled(b bool) {
	s.mu.Lock()
	s.calls = append(s.calls, b)
	s.mu.Unlock()
}

func (s *stubAutoInject) lastCall() (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return false, false
	}
	return s.calls[len(s.calls)-1], true
}

func newSaveHandlerWithStubs(t *testing.T, seed map[string]string) (*SettingsSaveHandler, *stubConfigStore, *stubAutoInject) {
	t.Helper()
	store := newStubConfigStore(seed)
	inj := &stubAutoInject{}
	h := NewSettingsSaveHandler(store, inj)
	return h, store, inj
}

// TestSettingsSave_AutoInjectFalse verifies setting auto_inject_enabled=false
// persists and calls SetAutoInjectEnabled.
func TestSettingsSave_AutoInjectFalse(t *testing.T) {
	h, store, inj := newSaveHandlerWithStubs(t, nil)

	resp := h.Handle(context.Background(),
		json.RawMessage(`{"auto_inject_enabled":false}`))
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content[0].Text)
	}

	vals := store.snapshot()
	if vals[config.KeyAutoInjectEnabled] != "false" {
		t.Errorf("config[auto_inject_enabled] = %q, want false", vals[config.KeyAutoInjectEnabled])
	}
	got, ok := inj.lastCall()
	if !ok {
		t.Fatal("SetAutoInjectEnabled not called")
	}
	if got {
		t.Fatalf("SetAutoInjectEnabled called with true, want false")
	}
}

// TestSettingsSave_AutoInjectTrue verifies setting auto_inject_enabled=true.
func TestSettingsSave_AutoInjectTrue(t *testing.T) {
	h, store, inj := newSaveHandlerWithStubs(t, nil)

	resp := h.Handle(context.Background(),
		json.RawMessage(`{"auto_inject_enabled":true}`))
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content[0].Text)
	}

	vals := store.snapshot()
	if vals[config.KeyAutoInjectEnabled] != "true" {
		t.Errorf("config[auto_inject_enabled] = %q, want true", vals[config.KeyAutoInjectEnabled])
	}
	got, ok := inj.lastCall()
	if !ok || !got {
		t.Fatalf("SetAutoInjectEnabled called with %v (ok=%v), want true", got, ok)
	}
}

// TestSettingsSave_StringBool verifies the string-encoded bool form.
func TestSettingsSave_StringBool(t *testing.T) {
	h, store, inj := newSaveHandlerWithStubs(t, nil)

	resp := h.Handle(context.Background(),
		json.RawMessage(`{"auto_inject_enabled":"false"}`))
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content[0].Text)
	}

	vals := store.snapshot()
	if vals[config.KeyAutoInjectEnabled] != "false" {
		t.Errorf("config[auto_inject_enabled] = %q, want false", vals[config.KeyAutoInjectEnabled])
	}
	got, _ := inj.lastCall()
	if got {
		t.Fatal("SetAutoInjectEnabled called with true, want false")
	}
}

// TestSettingsSave_EmptyBodyIsNoOp verifies an empty save has no effect.
func TestSettingsSave_EmptyBodyIsNoOp(t *testing.T) {
	seed := map[string]string{config.KeyAutoInjectEnabled: "true"}
	h, store, inj := newSaveHandlerWithStubs(t, seed)

	resp := h.Handle(context.Background(), json.RawMessage(`{}`))
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content[0].Text)
	}

	vals := store.snapshot()
	if vals[config.KeyAutoInjectEnabled] != "true" {
		t.Errorf("config should be unchanged; got %q", vals[config.KeyAutoInjectEnabled])
	}
	if _, ok := inj.lastCall(); ok {
		t.Fatal("SetAutoInjectEnabled should not be called on no-op save")
	}
}

// TestSettingsSave_InvalidBoolRejected verifies an invalid bool value fails.
func TestSettingsSave_InvalidBoolRejected(t *testing.T) {
	h, store, inj := newSaveHandlerWithStubs(t, nil)

	resp := h.Handle(context.Background(),
		json.RawMessage(`{"auto_inject_enabled":"maybe"}`))
	if !resp.IsError {
		t.Fatal("expected error for invalid bool, got success")
	}

	vals := store.snapshot()
	if _, ok := vals[config.KeyAutoInjectEnabled]; ok {
		t.Errorf("config should not be written on validation failure")
	}
	if _, ok := inj.lastCall(); ok {
		t.Fatal("SetAutoInjectEnabled must not be called on validation failure")
	}
}

// TestSettingsSave_InvalidJSON verifies bad JSON is rejected.
func TestSettingsSave_InvalidJSON(t *testing.T) {
	h, _, _ := newSaveHandlerWithStubs(t, nil)

	resp := h.Handle(context.Background(), json.RawMessage(`{invalid`))
	if !resp.IsError {
		t.Fatal("expected error for invalid JSON")
	}
}

// TestSettingsSave_ResponseEchosChanged verifies the response contains only
// the changed field.
func TestSettingsSave_ResponseEchosChanged(t *testing.T) {
	h, _, _ := newSaveHandlerWithStubs(t, nil)

	resp := h.Handle(context.Background(),
		json.RawMessage(`{"auto_inject_enabled":false}`))
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content[0].Text)
	}

	var out settingsSaveOutput
	if err := json.Unmarshal([]byte(resp.Content[0].Text), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out.AutoInjectEnabled == nil || *out.AutoInjectEnabled {
		t.Fatalf("response.auto_inject_enabled = %v, want false pointer", out.AutoInjectEnabled)
	}
}
