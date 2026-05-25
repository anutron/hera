package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/anutron/hera/internal/config"
)

// settingsConfigStore is the persistence dependency the save handler
// needs from db.ConfigDAO. Declared at the consumer per Go convention so
// the handler test can stub it without touching the DB.
type settingsConfigStore interface {
	Set(ctx context.Context, key, value string) error
}

// debounceSetter is the runtime dep the handler uses to hot-reload the
// idle window. *idle.Tracker satisfies it.
type debounceSetter interface {
	SetDebounce(d time.Duration)
}

// autoInjectSwitch is the runtime dep the handler uses to hot-reload the
// auto-inject master switch. *inject.Injector satisfies it.
type autoInjectSwitch interface {
	SetAutoInjectEnabled(b bool)
}

// SettingsSaveHandler implements the settings_save callback registered as
// the callback_url on the hera settings-section. On a valid save it
// validates input, persists supplied values to the config table, and
// pushes the new values into the live Tracker and Injector so the
// operator sees the effect without a daemon restart.
//
// The route lives under /mcp/settings_save by design — the substrate
// callback listener only dispatches via the /mcp/ mux, and using one
// listener with one auth surface is cheaper than a parallel route
// (design D3).
type SettingsSaveHandler struct {
	store    settingsConfigStore
	tracker  debounceSetter
	injector autoInjectSwitch
}

// NewSettingsSaveHandler constructs a SettingsSaveHandler. Callers wire
// it via mcp.Server.RegisterHandler("settings_save", h).
func NewSettingsSaveHandler(store settingsConfigStore, tracker debounceSetter, injector autoInjectSwitch) *SettingsSaveHandler {
	return &SettingsSaveHandler{store: store, tracker: tracker, injector: injector}
}

// settingsSaveInput captures the two callback-form fields with any-typed
// values. The substrate's form payload may serialize the int field as a
// JSON number or a JSON string (and similarly for the bool); we parse
// tolerantly and reject only at the validation step.
type settingsSaveInput struct {
	IdleDebounceSeconds any `json:"idle_debounce_seconds,omitempty"`
	AutoInjectEnabled   any `json:"auto_inject_enabled,omitempty"`
}

// settingsSaveOutput is the success payload returned in the response's
// content block so the substrate UI can re-render with confirmed state.
// Only the supplied fields are echoed back — a partial save returns just
// what changed, mirroring the substrate's optimistic-render convention.
type settingsSaveOutput struct {
	IdleDebounceSeconds *int  `json:"idle_debounce_seconds,omitempty"`
	AutoInjectEnabled   *bool `json:"auto_inject_enabled,omitempty"`
}

// Handle implements Handler. Parses, validates, then on success persists
// and applies. NO DB writes occur on validation failure (spec
// requirement: "the config table row for X MUST be unchanged").
func (h *SettingsSaveHandler) Handle(ctx context.Context, raw json.RawMessage) Response {
	var in settingsSaveInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ErrorResponse("settings_save: invalid input JSON: " + err.Error())
	}

	var (
		newDebounce *int
		newAuto     *bool
	)

	if in.IdleDebounceSeconds != nil {
		secs, err := parseDebounceSeconds(in.IdleDebounceSeconds)
		if err != nil {
			return ErrorResponse("settings_save: idle_debounce_seconds: " + err.Error())
		}
		newDebounce = &secs
	}

	if in.AutoInjectEnabled != nil {
		b, err := parseBool(in.AutoInjectEnabled)
		if err != nil {
			return ErrorResponse("settings_save: auto_inject_enabled: " + err.Error())
		}
		newAuto = &b
	}

	// All validation passed. Persist + apply. Persist BEFORE applying so a
	// transient DB failure aborts the save without leaving the in-memory
	// Tracker/Injector ahead of the persisted state.
	if newDebounce != nil {
		if err := h.store.Set(ctx, config.KeyIdleDebounceSeconds, strconv.Itoa(*newDebounce)); err != nil {
			return ErrorResponse("settings_save: persist idle_debounce_seconds: " + err.Error())
		}
		h.tracker.SetDebounce(time.Duration(*newDebounce) * time.Second)
	}
	if newAuto != nil {
		if err := h.store.Set(ctx, config.KeyAutoInjectEnabled, strconv.FormatBool(*newAuto)); err != nil {
			return ErrorResponse("settings_save: persist auto_inject_enabled: " + err.Error())
		}
		h.injector.SetAutoInjectEnabled(*newAuto)
	}

	return jsonText(settingsSaveOutput{
		IdleDebounceSeconds: newDebounce,
		AutoInjectEnabled:   newAuto,
	})
}

// parseDebounceSeconds accepts JSON number (int or float), or
// string-encoded int. Returns ErrRange-style error on out-of-bounds.
func parseDebounceSeconds(v any) (int, error) {
	var n int
	switch x := v.(type) {
	case float64:
		// Reject fractional values explicitly — substrate forms send
		// integers; a float arriving here means the operator (or a
		// broken serializer) shoved a decimal in.
		if x != float64(int(x)) {
			return 0, fmt.Errorf("must be an integer, got %v", x)
		}
		n = int(x)
	case string:
		parsed, err := strconv.Atoi(x)
		if err != nil {
			return 0, fmt.Errorf("must be an integer, got %q", x)
		}
		n = parsed
	default:
		return 0, fmt.Errorf("must be an integer, got %T", v)
	}
	if n < 0 || n > 60 {
		return 0, fmt.Errorf("must be in range [0, 60], got %d", n)
	}
	return n, nil
}

// parseBool accepts JSON bool or string-encoded bool ("true"/"false",
// case-insensitive via strconv.ParseBool which also accepts 1/0/t/f).
func parseBool(v any) (bool, error) {
	switch x := v.(type) {
	case bool:
		return x, nil
	case string:
		b, err := strconv.ParseBool(x)
		if err != nil {
			return false, fmt.Errorf("must be a boolean, got %q", x)
		}
		return b, nil
	default:
		return false, fmt.Errorf("must be a boolean, got %T", v)
	}
}
