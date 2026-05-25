package argus

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestClient_RegisterSettingsSection(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotVersion, gotContentType string
	var gotBody SettingsSectionDefinition
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("X-Argus-Plugin-Version")
		gotContentType = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"scope":"hera","title":"Hera","id":42}`)
	})
	defer srv.Close()

	def := SettingsSectionDefinition{
		Name:        "hera",
		Title:       "Hera",
		Type:        "form",
		CallbackURL: "http://127.0.0.1:7744/mcp/settings_save",
		AuthHeader:  "Bearer secret",
		Fields: []SettingField{
			{
				Key:         "idle_debounce_seconds",
				Label:       "Idle debounce (seconds)",
				Type:        "int",
				Description: "Seconds an agent's session must stay quiet...",
				Default:     2,
				Min:         intPtr(0),
				Max:         intPtr(60),
			},
			{
				Key:         "auto_inject_enabled",
				Label:       "Auto-inject cross-agent messages",
				Type:        "bool",
				Description: "When on, hera auto-submits...",
				Default:     true,
			},
		},
	}
	resp, err := c.RegisterSettingsSection(context.Background(), def)
	if err != nil {
		t.Fatalf("RegisterSettingsSection: %v", err)
	}
	if resp == nil || resp.Title != "Hera" || resp.Scope != "hera" || resp.ID != 42 {
		t.Fatalf("resp = %+v", resp)
	}
	if gotMethod != "POST" {
		t.Fatalf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/api/plugins/settings/sections" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotVersion != "1" {
		t.Fatalf("X-Argus-Plugin-Version = %q", gotVersion)
	}
	if gotContentType != "application/json" {
		t.Fatalf("Content-Type = %q", gotContentType)
	}
	if gotBody.Title != "Hera" || gotBody.Type != "form" {
		t.Fatalf("body title/type = %q/%q", gotBody.Title, gotBody.Type)
	}
	if gotBody.CallbackURL != "http://127.0.0.1:7744/mcp/settings_save" {
		t.Fatalf("callback_url = %q", gotBody.CallbackURL)
	}
	if gotBody.AuthHeader != "Bearer secret" {
		t.Fatalf("auth_header = %q", gotBody.AuthHeader)
	}
	if len(gotBody.Fields) != 2 {
		t.Fatalf("fields len = %d, want 2", len(gotBody.Fields))
	}
	if gotBody.Fields[0].Key != "idle_debounce_seconds" || gotBody.Fields[0].Type != "int" {
		t.Fatalf("field[0] = %+v", gotBody.Fields[0])
	}
	if gotBody.Fields[0].Label == "" {
		t.Fatalf("field[0].label is empty; argus rejects fields without a label")
	}
	if gotBody.Fields[0].Min == nil || *gotBody.Fields[0].Min != 0 {
		t.Fatalf("field[0].Min = %v, want 0", gotBody.Fields[0].Min)
	}
	if gotBody.Fields[0].Max == nil || *gotBody.Fields[0].Max != 60 {
		t.Fatalf("field[0].Max = %v, want 60", gotBody.Fields[0].Max)
	}
	if gotBody.Fields[1].Key != "auto_inject_enabled" || gotBody.Fields[1].Type != "bool" {
		t.Fatalf("field[1] = %+v", gotBody.Fields[1])
	}
	if gotBody.Fields[1].Label == "" {
		t.Fatalf("field[1].label is empty; argus rejects fields without a label")
	}
}

func TestClient_RegisterSettingsSection_JSONFieldNames(t *testing.T) {
	// Verify the wire-level JSON keys (snake_case) match the substrate contract.
	var raw map[string]any
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&raw)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"scope":"hera","title":"Hera","id":1}`)
	})
	defer srv.Close()

	def := SettingsSectionDefinition{
		Name:        "hera",
		Title:       "Hera",
		Type:        "form",
		CallbackURL: "http://example/cb",
		AuthHeader:  "Bearer x",
		Fields: []SettingField{
			{
				Key:         "f",
				Label:       "Field f",
				Type:        "int",
				Description: "d",
				Default:     1,
				Min:         intPtr(0),
				Max:         intPtr(10),
			},
		},
	}
	if _, err := c.RegisterSettingsSection(context.Background(), def); err != nil {
		t.Fatalf("RegisterSettingsSection: %v", err)
	}

	// Top-level wire keys argus's settings.rawSection decodes (plus the
	// hera-internal Name/auth_header it tolerates).
	for _, k := range []string{"name", "title", "type", "callback_url", "auth_header", "fields"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("missing top-level JSON key: %s", k)
		}
	}
	fields, _ := raw["fields"].([]any)
	if len(fields) != 1 {
		t.Fatalf("fields not encoded as array: %+v", raw["fields"])
	}
	f0, _ := fields[0].(map[string]any)
	// argus's settings.FormField wire keys.
	for _, k := range []string{"key", "label", "type", "description", "default", "min", "max"} {
		if _, ok := f0[k]; !ok {
			t.Errorf("missing field JSON key: %s", k)
		}
	}
}

// TestClient_RegisterSettingsSection_OmitsEmptyOptionalFields locks in the
// omitempty contract for non-int, no-default fields so a bool field doesn't
// leak null min/max onto the wire (where argus would harmlessly drop them
// but the noise muddies anyone tailing /api/plugins/settings/sections).
func TestClient_RegisterSettingsSection_OmitsEmptyOptionalFields(t *testing.T) {
	var raw map[string]any
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&raw)
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{}`)
	})
	defer srv.Close()

	def := SettingsSectionDefinition{
		Name:        "hera",
		Title:       "Hera",
		Type:        "form",
		CallbackURL: "http://example/cb",
		Fields: []SettingField{
			{Key: "b", Label: "Bool field", Type: "bool"},
		},
	}
	if _, err := c.RegisterSettingsSection(context.Background(), def); err != nil {
		t.Fatalf("RegisterSettingsSection: %v", err)
	}
	fields, _ := raw["fields"].([]any)
	f0, _ := fields[0].(map[string]any)
	for _, k := range []string{"min", "max", "default", "description"} {
		if _, ok := f0[k]; ok {
			t.Errorf("optional field %q should be omitted when empty; got %v", k, f0[k])
		}
	}
	if _, ok := raw["auth_header"]; ok {
		t.Errorf("auth_header should be omitted when empty; got %v", raw["auth_header"])
	}
}

func TestClient_RegisterSettingsSection_ErrorPropagated(t *testing.T) {
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad payload"}`, http.StatusBadRequest)
	})
	defer srv.Close()

	_, err := c.RegisterSettingsSection(context.Background(), SettingsSectionDefinition{Name: "hera", Title: "Hera", Type: "form"})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected 400 in error, got %v", err)
	}
}

func TestClient_UnregisterSettingsSection(t *testing.T) {
	var gotMethod, gotPath, gotAuth, gotVersion string
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("X-Argus-Plugin-Version")
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	if err := c.UnregisterSettingsSection(context.Background(), "hera", "Hera"); err != nil {
		t.Fatalf("UnregisterSettingsSection: %v", err)
	}
	if gotMethod != "DELETE" {
		t.Fatalf("method = %s, want DELETE", gotMethod)
	}
	if gotPath != "/api/plugins/settings/sections/hera/Hera" {
		t.Fatalf("path = %s, want /api/plugins/settings/sections/hera/Hera", gotPath)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotVersion != "1" {
		t.Fatalf("X-Argus-Plugin-Version = %q", gotVersion)
	}
}

func TestClient_UnregisterSettingsSection_EscapesPath(t *testing.T) {
	var gotPath string
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	defer srv.Close()

	if err := c.UnregisterSettingsSection(context.Background(), "hera", "title with/slash"); err != nil {
		t.Fatalf("UnregisterSettingsSection: %v", err)
	}
	// PathEscape encodes a literal "/" as %2F. ServeMux decodes the path
	// before we observe it, so we expect the decoded form to round-trip.
	if !strings.HasPrefix(gotPath, "/api/plugins/settings/sections/hera/") {
		t.Fatalf("path = %s", gotPath)
	}
	if !strings.Contains(gotPath, "title with") {
		t.Fatalf("path missing escaped title: %s", gotPath)
	}
}

func TestClient_UnregisterSettingsSection_ErrorPropagated(t *testing.T) {
	srv, c := newTestServerAndClient(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
	})
	defer srv.Close()

	err := c.UnregisterSettingsSection(context.Background(), "hera", "Hera")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected 403 in error, got %v", err)
	}
}

func intPtr(v int) *int { return &v }
