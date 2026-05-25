package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config bundles hera's runtime configuration. All fields have working
// defaults; the daemon does not require a user-supplied config file in v1.
type Config struct {
	// StateDir is where hera keeps its SQLite, token file, log, and PID.
	// Default: ~/.hera
	StateDir string

	// ArgusBaseURL is the argus daemon's HTTP root.
	// Default: http://127.0.0.1:7743
	ArgusBaseURL string

	// ListenAddr is the address the MCP callback HTTP listener binds to.
	// Default: 127.0.0.1:7744. Use "127.0.0.1:0" in tests to bind to an
	// arbitrary free port; the daemon derives the callback URL it
	// advertises to argus from the actual bound address.
	ListenAddr string

	// IdleDebounce is how long session.idle must persist before a task
	// becomes auto-submit-eligible (see design D10).
	// Default: 2s.
	IdleDebounce time.Duration

	// MCPHeartbeat is how often hera re-POSTs its tool registrations
	// to argus to stay within the substrate's idle sweep window.
	// Default: 5m.
	MCPHeartbeat time.Duration

	// AutoInjectEnabled is the master switch over the auto-submit branch
	// of Injector.Inject. When false, every message is delivered in
	// busy_buffer mode regardless of recipient idle state.
	// Default: true.
	AutoInjectEnabled bool

	// ArgusSocketPath is the unix-domain socket exposed by the argus
	// daemon for the Daemon.* RPC family (Ports, Ping). Hera queries it
	// on startup to discover argus's dynamic REST port and again on every
	// reconnect after argus restarts.
	// Default: ~/.argus/daemon.sock
	ArgusSocketPath string

	// ArgusPIDPath is the pid file argus rewrites on every daemon start.
	// Hera polls its mtime in the Watcher to detect restarts.
	// Default: ~/.argus/daemon.pid
	ArgusPIDPath string
}

// Default returns a Config populated with the v1 defaults.
func Default() *Config {
	home, _ := os.UserHomeDir()
	stateDir := filepath.Join(home, ".hera")
	argusDir := filepath.Join(home, ".argus")
	return &Config{
		StateDir:          stateDir,
		ArgusBaseURL:      "http://127.0.0.1:7743",
		ListenAddr:        "127.0.0.1:7744",
		IdleDebounce:      2 * time.Second,
		MCPHeartbeat:      5 * time.Minute,
		AutoInjectEnabled: true,
		ArgusSocketPath:   filepath.Join(argusDir, "daemon.sock"),
		ArgusPIDPath:      filepath.Join(argusDir, "daemon.pid"),
	}
}

// TokenPath returns the path to the scope-token file hera reads on
// startup.
func (c *Config) TokenPath() string {
	return filepath.Join(c.StateDir, "api-token")
}

// StatePath returns the path to the SQLite state file.
func (c *Config) StatePath() string {
	return filepath.Join(c.StateDir, "state.sqlite")
}

// PIDPath returns the path to hera's PID file.
func (c *Config) PIDPath() string {
	return filepath.Join(c.StateDir, "hera.pid")
}

// LogPath returns the path to hera's log file.
func (c *Config) LogPath() string {
	return filepath.Join(c.StateDir, "hera.log")
}

// LoadToken reads the scope token from ~/.hera/api-token (or whatever
// the configured TokenPath resolves to). Missing or empty files produce
// an actionable error message.
func (c *Config) LoadToken() (string, error) {
	path := c.TokenPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf(
				"hera: scope token file %s not found\n\n"+
					"Run: argus token mint --scope hera > %s\n"+
					"     chmod 600 %s\n",
				path, path, path,
			)
		}
		return "", fmt.Errorf("hera: read token file %s: %w", path, err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf(
			"hera: scope token file %s is empty\n\n"+
				"Run: argus token mint --scope hera > %s\n",
			path, path,
		)
	}
	return token, nil
}

// EnsureStateDir creates the configured StateDir with 0o700 permissions
// (token-grade) if missing.
func (c *Config) EnsureStateDir() error {
	return os.MkdirAll(c.StateDir, 0o700)
}
