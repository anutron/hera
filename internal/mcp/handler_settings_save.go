package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/anutron/hera/internal/config"
)

// settingsConfigStore is the persistence dependency the save handler
// needs from db.ConfigDAO.
type settingsConfigStore interface {
	Set(ctx context.Context, key, value string) error
}

// autoInjectSwitch is the runtime dep the handler uses to hot-reload the
// auto-inject master switch. *SendHandler satisfies it.
type autoInjectSwitch interface {
	SetAutoInjectEnabled(b bool)
}

// SettingsSaveHandler implements the settings_save callback registered as
// the callback_url on the hera settings-section. On a valid save it
// validates input, persists supplied values to the config table, and
// pushes the new values into the live SendHandler so the operator sees
// the effect without a daemon restart.
type SettingsSaveHandler struct {
	store    settingsConfigStore
	injector autoInjectSwitch
}

// NewSettingsSaveHandler constructs a SettingsSaveHandler.
func NewSettingsSaveHandler(store settingsConfigStore, injector autoInjectSwitch) *SettingsSaveHandler {
	return &SettingsSaveHandler{store: store, injector: injector}
}

// settingsSaveInput captures the callback-form field with any-typed value.
// The substrate's form payload may serialize the bool as a JSON bool or a
// JSON string; we parse tolerantly.
type settingsSaveInput struct {
	AutoInjectEnabled any `json:"auto_inject_enabled,omitempty"`
}

// settingsSaveOutput is the success payload echoing confirmed state back.
type settingsSaveOutput struct {
	AutoInjectEnabled *bool `json:"auto_inject_enabled,omitempty"`
}

// Handle implements Handler.
func (h *SettingsSaveHandler) Handle(ctx context.Context, raw json.RawMessage) Response {
	var in settingsSaveInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return ErrorResponse("settings_save: invalid input JSON: " + err.Error())
	}

	var newAuto *bool

	if in.AutoInjectEnabled != nil {
		b, err := parseBool(in.AutoInjectEnabled)
		if err != nil {
			return ErrorResponse("settings_save: auto_inject_enabled: " + err.Error())
		}
		newAuto = &b
	}

	if newAuto != nil {
		if err := h.store.Set(ctx, config.KeyAutoInjectEnabled, strconv.FormatBool(*newAuto)); err != nil {
			return ErrorResponse("settings_save: persist auto_inject_enabled: " + err.Error())
		}
		h.injector.SetAutoInjectEnabled(*newAuto)
	}

	return jsonText(settingsSaveOutput{AutoInjectEnabled: newAuto})
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
