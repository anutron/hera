package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/anutron/hera/internal/config"
)

// stubConfigStore is a tiny in-memory ConfigDAO replacement. Tests assert
// the keys/values it received post-handle.
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

func (s *stubConfigStore) Set(ctx context.Context, key, value string) error {
	s.mu.Lock()
	s.values[key] = value
	s.mu.Unlock()
	return nil
}

func (s *stubConfigStore) Get(ctx context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.values[key]; ok {
		return v, nil
	}
	return "", errStubNotFound
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

// errStubNotFound mimics db.ErrNotFound for the stub; the handler treats
// "key missing" as identical to "no prior value" so the exact identity
// doesn't matter for these tests.
var errStubNotFound = stubErr("not found")

type stubErr string

func (e stubErr) Error() string { return string(e) }

// stubTracker captures SetDebounce calls.
type stubTracker struct {
	mu    sync.Mutex
	calls []time.Duration
}

func (s *stubTracker) SetDebounce(d time.Duration) {
	s.mu.Lock()
	s.calls = append(s.calls, d)
	s.mu.Unlock()
}

func (s *stubTracker) lastCall() (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return 0, false
	}
	return s.calls[len(s.calls)-1], true
}

func (s *stubTracker) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// stubInjector captures SetAutoInjectEnabled calls.
type stubInjector struct {
	mu    sync.Mutex
	calls []bool
}

func (s *stubInjector) SetAutoInjectEnabled(b bool) {
	s.mu.Lock()
	s.calls = append(s.calls, b)
	s.mu.Unlock()
}

func (s *stubInjector) lastCall() (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return false, false
	}
	return s.calls[len(s.calls)-1], true
}

func (s *stubInjector) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func newSaveHandlerWithStubs(t *testing.T, seed map[string]string) (*SettingsSaveHandler, *stubConfigStore, *stubTracker, *stubInjector) {
	t.Helper()
	store := newStubConfigStore(seed)
	tr := &stubTracker{}
	inj := &stubInjector{}
	h := NewSettingsSaveHandler(store, tr, inj)
	return h, store, tr, inj
}

// TestSettingsSave_ValidSavePersistsAndHotReloads covers the spec
// scenario "Valid save persists and hot-reloads":
// { idle_debounce_seconds: 3, auto_inject_enabled: false } → config
// table contains both keys with stringified values AND Tracker /
// Injector setters were called with the right values.
func TestSettingsSave_ValidSavePersistsAndHotReloads(t *testing.T) {
	h, store, tr, inj := newSaveHandlerWithStubs(t, nil)

	resp := h.Handle(context.Background(),
		json.RawMessage(`{"idle_debounce_seconds":3,"auto_inject_enabled":false}`))
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content[0].Text)
	}

	vals := store.snapshot()
	if vals[config.KeyIdleDebounceSeconds] != "3" {
		t.Errorf("config[idle_debounce_seconds] = %q, want %q",
			vals[config.KeyIdleDebounceSeconds], "3")
	}
	if vals[config.KeyAutoInjectEnabled] != "false" {
		t.Errorf("config[auto_inject_enabled] = %q, want %q",
			vals[config.KeyAutoInjectEnabled], "false")
	}

	if d, ok := tr.lastCall(); !ok || d != 3*time.Second {
		t.Errorf("Tracker.SetDebounce last call = %v (ok=%v), want 3s", d, ok)
	}
	if b, ok := inj.lastCall(); !ok || b != false {
		t.Errorf("Injector.SetAutoInjectEnabled last call = %v (ok=%v), want false", b, ok)
	}
}

// TestSettingsSave_OutOfRangeIntRejected covers "Out-of-range debounce
// rejected, no rows written".
func TestSettingsSave_OutOfRangeIntRejected(t *testing.T) {
	h, store, tr, inj := newSaveHandlerWithStubs(t, nil)

	resp := h.Handle(context.Background(),
		json.RawMessage(`{"idle_debounce_seconds":99}`))
	if !resp.IsError {
		t.Fatalf("expected isError=true, got success: %+v", resp)
	}
	text := resp.Content[0].Text
	if !contains(text, "idle_debounce_seconds") {
		t.Errorf("error text should name the offending field; got %q", text)
	}

	if len(store.snapshot()) != 0 {
		t.Errorf("expected no DB writes on validation failure, got %v", store.snapshot())
	}
	if tr.callCount() != 0 {
		t.Errorf("expected no Tracker.SetDebounce calls on validation failure, got %d", tr.callCount())
	}
	if inj.callCount() != 0 {
		t.Errorf("expected no Injector.SetAutoInjectEnabled calls on validation failure, got %d", inj.callCount())
	}
}

// TestSettingsSave_NegativeIntRejected guards the lower bound too.
func TestSettingsSave_NegativeIntRejected(t *testing.T) {
	h, store, _, _ := newSaveHandlerWithStubs(t, nil)
	resp := h.Handle(context.Background(),
		json.RawMessage(`{"idle_debounce_seconds":-1}`))
	if !resp.IsError {
		t.Fatalf("expected isError=true for negative, got success: %+v", resp)
	}
	if len(store.snapshot()) != 0 {
		t.Errorf("expected no DB writes on negative input, got %v", store.snapshot())
	}
}

// TestSettingsSave_NonBoolRejected covers "Non-boolean auto-inject
// rejected" — the spec scenario sends the string "maybe".
func TestSettingsSave_NonBoolRejected(t *testing.T) {
	h, store, tr, inj := newSaveHandlerWithStubs(t, nil)

	resp := h.Handle(context.Background(),
		json.RawMessage(`{"auto_inject_enabled":"maybe"}`))
	if !resp.IsError {
		t.Fatalf("expected isError=true, got success: %+v", resp)
	}
	if !contains(resp.Content[0].Text, "auto_inject_enabled") {
		t.Errorf("error text should name the offending field; got %q", resp.Content[0].Text)
	}
	if len(store.snapshot()) != 0 {
		t.Errorf("expected no DB writes on validation failure, got %v", store.snapshot())
	}
	if tr.callCount() != 0 || inj.callCount() != 0 {
		t.Errorf("expected no setter calls on validation failure")
	}
}

// TestSettingsSave_PartialSaveOnlyUpdatesSuppliedField covers the
// "Partial save updates only supplied field" scenario. Pre-seed both
// keys, then send only idle_debounce_seconds, and verify
// auto_inject_enabled is untouched.
func TestSettingsSave_PartialSaveOnlyUpdatesSuppliedField(t *testing.T) {
	h, store, tr, inj := newSaveHandlerWithStubs(t, map[string]string{
		config.KeyIdleDebounceSeconds: "2",
		config.KeyAutoInjectEnabled:   "true",
	})

	resp := h.Handle(context.Background(),
		json.RawMessage(`{"idle_debounce_seconds":5}`))
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content[0].Text)
	}

	vals := store.snapshot()
	if vals[config.KeyIdleDebounceSeconds] != "5" {
		t.Errorf("idle_debounce_seconds = %q, want %q", vals[config.KeyIdleDebounceSeconds], "5")
	}
	if vals[config.KeyAutoInjectEnabled] != "true" {
		t.Errorf("auto_inject_enabled should be unchanged at %q, got %q",
			"true", vals[config.KeyAutoInjectEnabled])
	}

	if tr.callCount() != 1 {
		t.Errorf("Tracker.SetDebounce call count = %d, want 1", tr.callCount())
	}
	if inj.callCount() != 0 {
		t.Errorf("Injector.SetAutoInjectEnabled should NOT be called for partial save; got %d calls",
			inj.callCount())
	}
}

// TestSettingsSave_TolerantIntDecoding covers the design point that the
// substrate's form payload may serialize the int field as a JSON number
// (3) or a JSON string ("3"). The handler must accept both.
func TestSettingsSave_TolerantIntDecoding(t *testing.T) {
	for _, raw := range []string{
		`{"idle_debounce_seconds":3}`,
		`{"idle_debounce_seconds":"3"}`,
		`{"idle_debounce_seconds":3.0}`,
	} {
		h, store, tr, _ := newSaveHandlerWithStubs(t, nil)
		resp := h.Handle(context.Background(), json.RawMessage(raw))
		if resp.IsError {
			t.Fatalf("payload %s: unexpected error: %s", raw, resp.Content[0].Text)
		}
		if store.snapshot()[config.KeyIdleDebounceSeconds] != "3" {
			t.Errorf("payload %s: stored value = %q, want %q",
				raw, store.snapshot()[config.KeyIdleDebounceSeconds], "3")
		}
		if d, _ := tr.lastCall(); d != 3*time.Second {
			t.Errorf("payload %s: Tracker.SetDebounce got %v, want 3s", raw, d)
		}
	}
}

// TestSettingsSave_TolerantBoolDecoding mirrors the int tolerance for
// the bool field — JSON bool or string-encoded bool both accepted.
func TestSettingsSave_TolerantBoolDecoding(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{`{"auto_inject_enabled":false}`, false},
		{`{"auto_inject_enabled":"false"}`, false},
		{`{"auto_inject_enabled":true}`, true},
		{`{"auto_inject_enabled":"true"}`, true},
	}
	for _, c := range cases {
		h, store, _, inj := newSaveHandlerWithStubs(t, nil)
		resp := h.Handle(context.Background(), json.RawMessage(c.raw))
		if resp.IsError {
			t.Fatalf("payload %s: unexpected error: %s", c.raw, resp.Content[0].Text)
		}
		wantStr := "true"
		if !c.want {
			wantStr = "false"
		}
		if store.snapshot()[config.KeyAutoInjectEnabled] != wantStr {
			t.Errorf("payload %s: stored value = %q, want %q",
				c.raw, store.snapshot()[config.KeyAutoInjectEnabled], wantStr)
		}
		if b, _ := inj.lastCall(); b != c.want {
			t.Errorf("payload %s: Injector.SetAutoInjectEnabled got %v, want %v", c.raw, b, c.want)
		}
	}
}

// TestSettingsSave_ResponseEchoesEffectiveValues covers task 6.5:
// success response carries the new effective values so the substrate UI
// can re-render. The response shape we expect:
//   {"idle_debounce_seconds": 3, "auto_inject_enabled": false}
// surfaced as a JSON text block.
func TestSettingsSave_ResponseEchoesEffectiveValues(t *testing.T) {
	h, _, _, _ := newSaveHandlerWithStubs(t, nil)
	resp := h.Handle(context.Background(),
		json.RawMessage(`{"idle_debounce_seconds":7,"auto_inject_enabled":false}`))
	if resp.IsError {
		t.Fatalf("unexpected error: %s", resp.Content[0].Text)
	}
	if len(resp.Content) == 0 {
		t.Fatalf("expected at least one content block")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(resp.Content[0].Text), &out); err != nil {
		t.Fatalf("response not JSON: %v\n%s", err, resp.Content[0].Text)
	}
	// The supplied int comes back as JSON number → float64 here.
	if v, ok := out["idle_debounce_seconds"].(float64); !ok || v != 7 {
		t.Errorf("response idle_debounce_seconds = %v (%T), want 7", out["idle_debounce_seconds"], out["idle_debounce_seconds"])
	}
	if v, ok := out["auto_inject_enabled"].(bool); !ok || v != false {
		t.Errorf("response auto_inject_enabled = %v (%T), want false", out["auto_inject_enabled"], out["auto_inject_enabled"])
	}
}

// TestSettingsSave_EmptyPayloadIsNoOp guards against accidentally
// clearing the config table with an empty save (no keys). Empty
// payload should not error and should not write anything.
func TestSettingsSave_EmptyPayloadIsNoOp(t *testing.T) {
	h, store, tr, inj := newSaveHandlerWithStubs(t, map[string]string{
		config.KeyIdleDebounceSeconds: "2",
		config.KeyAutoInjectEnabled:   "true",
	})
	resp := h.Handle(context.Background(), json.RawMessage(`{}`))
	if resp.IsError {
		t.Fatalf("unexpected error for empty payload: %s", resp.Content[0].Text)
	}
	// No setter calls.
	if tr.callCount() != 0 || inj.callCount() != 0 {
		t.Errorf("expected no setter calls on empty payload; got tracker=%d, injector=%d",
			tr.callCount(), inj.callCount())
	}
	// Pre-seeded values intact.
	vals := store.snapshot()
	if vals[config.KeyIdleDebounceSeconds] != "2" || vals[config.KeyAutoInjectEnabled] != "true" {
		t.Errorf("pre-seeded values should be unchanged, got %v", vals)
	}
}

// TestSettingsSave_InvalidJSONRejected guards the envelope-decode error
// path so a malformed payload doesn't panic or get silently accepted.
func TestSettingsSave_InvalidJSONRejected(t *testing.T) {
	h, _, _, _ := newSaveHandlerWithStubs(t, nil)
	resp := h.Handle(context.Background(), json.RawMessage(`{not json`))
	if !resp.IsError {
		t.Fatalf("expected isError=true for invalid JSON, got %+v", resp)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
