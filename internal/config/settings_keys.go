package config

// Settings keys persisted in the config table (see design D4). Values are
// stringified primitives parsed at the call site; the DAO stays untyped.
const (
	// KeyIdleDebounceSeconds stores Config.IdleDebounce as a stringified
	// integer count of seconds, e.g. "3".
	KeyIdleDebounceSeconds = "idle_debounce_seconds"

	// KeyAutoInjectEnabled stores Config.AutoInjectEnabled as "true" or
	// "false".
	KeyAutoInjectEnabled = "auto_inject_enabled"
)
