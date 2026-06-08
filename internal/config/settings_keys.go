package config

// Settings keys persisted in the config table. Values are stringified
// primitives parsed at the call site; the DAO stays untyped.
const (
	// KeyAutoInjectEnabled stores Config.AutoInjectEnabled as "true" or "false".
	KeyAutoInjectEnabled = "auto_inject_enabled"
)
