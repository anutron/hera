package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Config bundles ludwig's runtime configuration. All fields have working
// defaults; the daemon does not require a user-supplied config file in v1.
type Config struct {
	// StateDir is where ludwig keeps its SQLite, token file, log, and PID.
	// Default: ~/.ludwig
	StateDir string

	// ArgusBaseURL is the argus daemon's HTTP root.
	// Default: http://127.0.0.1:7743
	ArgusBaseURL string

	// ListenAddr is the address the MCP callback HTTP listener binds to.
	// Default: 127.0.0.1:7744
	ListenAddr string

	// CallbackBaseURL is what ludwig advertises to argus as its
	// callback_url prefix. Usually "http://" + the actual listen address.
	// Default: derived from ListenAddr.
	CallbackBaseURL string

	// IdleDebounce is how long session.idle must persist before a task
	// becomes auto-submit-eligible (see design D10).
	// Default: 2s.
	IdleDebounce time.Duration

	// MCPHeartbeat is how often ludwig re-POSTs its tool registrations
	// to argus to stay within the substrate's idle sweep window.
	// Default: 5m.
	MCPHeartbeat time.Duration
}

// Default returns a Config populated with the v1 defaults.
func Default() *Config {
	home, _ := os.UserHomeDir()
	stateDir := filepath.Join(home, ".ludwig")
	listenAddr := "127.0.0.1:7744"
	return &Config{
		StateDir:        stateDir,
		ArgusBaseURL:    "http://127.0.0.1:7743",
		ListenAddr:      listenAddr,
		CallbackBaseURL: "http://" + listenAddr,
		IdleDebounce:    2 * time.Second,
		MCPHeartbeat:    5 * time.Minute,
	}
}

// TokenPath returns the path to the scope-token file ludwig reads on
// startup.
func (c *Config) TokenPath() string {
	return filepath.Join(c.StateDir, "api-token")
}

// StatePath returns the path to the SQLite state file.
func (c *Config) StatePath() string {
	return filepath.Join(c.StateDir, "state.sqlite")
}

// PIDPath returns the path to ludwig's PID file.
func (c *Config) PIDPath() string {
	return filepath.Join(c.StateDir, "ludwig.pid")
}

// LogPath returns the path to ludwig's log file.
func (c *Config) LogPath() string {
	return filepath.Join(c.StateDir, "ludwig.log")
}

// LoadToken reads the scope token from ~/.ludwig/api-token (or whatever
// the configured TokenPath resolves to). Missing or empty files produce
// an actionable error message.
func (c *Config) LoadToken() (string, error) {
	path := c.TokenPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf(
				"ludwig: scope token file %s not found\n\n"+
					"Run: argus token mint --scope ludwig > %s\n"+
					"     chmod 600 %s\n",
				path, path, path,
			)
		}
		return "", fmt.Errorf("ludwig: read token file %s: %w", path, err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf(
			"ludwig: scope token file %s is empty\n\n"+
				"Run: argus token mint --scope ludwig > %s\n",
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
