package settings

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anutron/hera/internal/argus"
)

// fakeSettingsRegistry stubs argus's settings-section CRUD surface:
//
//	POST   /api/plugins/settings/sections                  -> register/heartbeat
//	DELETE /api/plugins/settings/sections/{scope}/{title}  -> unregister
//
// It records every body argus receives so tests can assert payload shape,
// callback_url, auth_header, heartbeat counts, and unregister calls.
// Unregister calls are captured as "{scope}/{title}" strings so tests can
// assert both segments at once.
type fakeSettingsRegistry struct {
	mu          sync.Mutex
	registered  []argus.SettingsSectionDefinition
	unregister  []string
	authHeaders []string
}

func (f *fakeSettingsRegistry) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/plugins/settings/sections", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		var body argus.SettingsSectionDefinition
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "decode: "+err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.registered = append(f.registered, body)
		f.authHeaders = append(f.authHeaders, body.AuthHeader)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"scope":"hera","title":"`+body.Title+`","id":1}`)
	})
	mux.HandleFunc("/api/plugins/settings/sections/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/api/plugins/settings/sections/")
		f.mu.Lock()
		f.unregister = append(f.unregister, path)
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func (f *fakeSettingsRegistry) snapshot() ([]argus.SettingsSectionDefinition, []string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reg := append([]argus.SettingsSectionDefinition(nil), f.registered...)
	un := append([]string(nil), f.unregister...)
	auth := append([]string(nil), f.authHeaders...)
	return reg, un, auth
}

// fixtureSection is a minimal section the tests use when payload shape is
// not the focus; payload-shape assertions live in their own test.
func fixtureSection() argus.SettingsSectionDefinition {
	return argus.SettingsSectionDefinition{
		Name:        "hera",
		Title:       "Hera",
		Type:        "form",
		CallbackURL: "http://127.0.0.1:7744/mcp/settings_save",
		AuthHeader:  "Bearer test-secret",
		Fields: []argus.SettingField{
			{Key: "f", Label: "Field f", Type: "int", Description: "d", Default: 1},
		},
	}
}

func TestRegistrar_StartRegistersAndHeartbeats(t *testing.T) {
	fake := &fakeSettingsRegistry{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	client := argus.New(srv.URL, "tok")

	r := NewRegistrar(client, nil)
	r.Add(fixtureSection())
	r.SetHeartbeat(50 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Initial registration: one section registered.
	reg, _, auth := fake.snapshot()
	if len(reg) < 1 {
		t.Fatalf("expected ≥1 registration after Start, got %d", len(reg))
	}
	if reg[0].Name != "hera" {
		t.Fatalf("registered section name = %q, want hera", reg[0].Name)
	}
	if auth[0] != "Bearer test-secret" {
		t.Fatalf("auth_header = %q", auth[0])
	}
	initialCount := len(reg)

	// Wait for at least one heartbeat tick (50ms heartbeat × ~2 ticks).
	time.Sleep(150 * time.Millisecond)

	reg2, _, _ := fake.snapshot()
	if len(reg2) <= initialCount {
		t.Fatalf("expected heartbeat to re-register, got %d (was %d)",
			len(reg2), initialCount)
	}

	if err := r.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestRegistrar_StopDeletesEachRegisteredSection(t *testing.T) {
	fake := &fakeSettingsRegistry{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	client := argus.New(srv.URL, "tok")

	r := NewRegistrar(client, nil)
	r.Add(fixtureSection())
	// Second section to verify each section gets its own DELETE.
	r.Add(argus.SettingsSectionDefinition{
		Name:        "hera-extra",
		Title:       "Hera Extra",
		Type:        "form",
		CallbackURL: "http://127.0.0.1:7744/mcp/settings_save",
		AuthHeader:  "Bearer test-secret",
	})
	r.SetHeartbeat(time.Hour) // suppress heartbeats during this test

	ctx := context.Background()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	_, un, _ := fake.snapshot()
	if len(un) != 2 {
		t.Fatalf("expected 2 unregister calls, got %d (%v)", len(un), un)
	}
	// Unregister path is /api/plugins/settings/sections/{scope}/{title};
	// the fake captures the "{scope}/{title}" tail. Scope is the hera
	// plugin-token scope per HeraSectionScope.
	got := map[string]bool{un[0]: true, un[1]: true}
	if !got["hera/Hera"] || !got["hera/Hera Extra"] {
		t.Fatalf("expected DELETEs for both sections by scope/title, got %v", un)
	}
}

// TestRegistrar_PayloadShapeMatchesSpec asserts the spec's required field
// shape: one form section, two fields (idle_debounce_seconds bounded [0,60]
// default 2, auto_inject_enabled bool default true), and the locked
// callback URL. This is the payload coord wires up in Stage 7; the
// registrar itself only needs to relay what callers Add, but the test
// double-checks the locked descriptions from tasks.md task 3.4 land
// on the wire untouched.
func TestRegistrar_PayloadShapeMatchesSpec(t *testing.T) {
	fake := &fakeSettingsRegistry{}
	srv := httptest.NewServer(fake.handler())
	defer srv.Close()
	client := argus.New(srv.URL, "tok")

	section := HeraSection("Bearer abc123")

	r := NewRegistrar(client, nil)
	r.Add(section)
	r.SetHeartbeat(time.Hour)

	ctx := context.Background()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = r.Stop(context.Background()) }()

	reg, _, _ := fake.snapshot()
	if len(reg) != 1 {
		t.Fatalf("expected 1 registration, got %d", len(reg))
	}
	s := reg[0]
	if s.Name != "hera" {
		t.Errorf("section name = %q, want hera", s.Name)
	}
	// Title must be non-empty — argus rejects empty titles with HTTP 400.
	// Regression: hera v1 omitted this field and crash-looped on startup
	// once the section registration first hit a real argus build.
	if s.Title == "" {
		t.Errorf("section title is empty; argus requires non-empty title")
	}
	if s.Type != "form" {
		t.Errorf("section type = %q, want form", s.Type)
	}
	if s.CallbackURL != "http://127.0.0.1:7744/mcp/settings_save" {
		t.Errorf("callback_url = %q, want http://127.0.0.1:7744/mcp/settings_save", s.CallbackURL)
	}
	if s.AuthHeader != "Bearer abc123" {
		t.Errorf("auth_header = %q", s.AuthHeader)
	}
	if len(s.Fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(s.Fields))
	}

	f0 := s.Fields[0]
	if f0.Key != "idle_debounce_seconds" {
		t.Errorf("field[0].key = %q, want idle_debounce_seconds", f0.Key)
	}
	// Label is the short user-facing field name argus renders next to the
	// input. Argus rejects fields with an empty label.
	if f0.Label == "" {
		t.Errorf("field[0].label is empty; argus requires a non-empty label")
	}
	if f0.Type != "int" {
		t.Errorf("field[0].type = %q", f0.Type)
	}
	if f0.Default == nil {
		t.Errorf("field[0].default is nil; want 2")
	}
	// JSON-decoded default comes back as float64 since SettingField.Default is `any`.
	if fl, ok := f0.Default.(float64); !ok || fl != 2 {
		t.Errorf("field[0].default = %v (%T), want 2", f0.Default, f0.Default)
	}
	if f0.Min == nil || *f0.Min != 0 {
		t.Errorf("field[0].min = %v, want 0", f0.Min)
	}
	if f0.Max == nil || *f0.Max != 60 {
		t.Errorf("field[0].max = %v, want 60", f0.Max)
	}
	// Description must mention low/high impact and the meaning of 0 and 60
	// (spec requirement: "Settings field descriptions explain impact").
	d := f0.Description
	for _, needle := range []string{"Lower", "Higher", "0", "60"} {
		if !strings.Contains(d, needle) {
			t.Errorf("field[0].description missing %q; full text:\n%s", needle, d)
		}
	}

	f1 := s.Fields[1]
	if f1.Key != "auto_inject_enabled" {
		t.Errorf("field[1].key = %q, want auto_inject_enabled", f1.Key)
	}
	if f1.Label == "" {
		t.Errorf("field[1].label is empty; argus requires a non-empty label")
	}
	if f1.Type != "bool" {
		t.Errorf("field[1].type = %q", f1.Type)
	}
	if b, ok := f1.Default.(bool); !ok || !b {
		t.Errorf("field[1].default = %v (%T), want true", f1.Default, f1.Default)
	}
	// Description must cover on/off impact and a concrete use case for off.
	d = f1.Description
	for _, needle := range []string{"on", "off"} {
		if !strings.Contains(strings.ToLower(d), needle) {
			t.Errorf("field[1].description missing %q; full text:\n%s", needle, d)
		}
	}
}

// TestRegistrar_HeartbeatLogsButDoesNotKillRegistrarOnTransientArgusError
// guards the heartbeat error path: if argus returns 500 to a heartbeat
// re-POST, the registrar should keep ticking and the subsequent successful
// register should land. Mirrors the equivalent behavior in mcp.Registrar.
func TestRegistrar_HeartbeatSurvivesTransientArgusError(t *testing.T) {
	var failNext atomic
	fake := &fakeSettingsRegistry{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/plugins/settings/sections", func(w http.ResponseWriter, r *http.Request) {
		if failNext.swap(false) {
			http.Error(w, `{"error":"transient"}`, http.StatusInternalServerError)
			return
		}
		var body argus.SettingsSectionDefinition
		_ = json.NewDecoder(r.Body).Decode(&body)
		fake.mu.Lock()
		fake.registered = append(fake.registered, body)
		fake.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"scope":"hera","title":"`+body.Title+`","id":1}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := argus.New(srv.URL, "tok")
	r := NewRegistrar(client, nil)
	r.Add(fixtureSection())
	r.SetHeartbeat(40 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := r.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Arm a single failure for the next heartbeat POST.
	failNext.set(true)
	time.Sleep(150 * time.Millisecond)

	fake.mu.Lock()
	got := len(fake.registered)
	fake.mu.Unlock()
	if got < 2 {
		t.Fatalf("expected at least 2 successful registers after one transient failure, got %d", got)
	}
}

// atomic is a tiny bool-flag helper for the transient-error test.
type atomic struct {
	mu sync.Mutex
	v  bool
}

func (a *atomic) set(v bool) {
	a.mu.Lock()
	a.v = v
	a.mu.Unlock()
}

func (a *atomic) swap(new bool) bool {
	a.mu.Lock()
	old := a.v
	a.v = new
	a.mu.Unlock()
	return old
}
