package daemon

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anutron/hera/internal/config"
	"github.com/anutron/hera/internal/db"
)

// openTestDB opens a fresh hera state DB under t.TempDir(). The caller does
// not need to Close it; t.Cleanup wires teardown.
func openTestDB(t *testing.T) *db.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func TestLoadPersistedSettings_PersistedAutoInjectFalseWins(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	if err := d.Config.Set(ctx, config.KeyAutoInjectEnabled, "false"); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	cfg := config.Default()
	if cfg.AutoInjectEnabled != true {
		t.Fatalf("precondition: Default().AutoInjectEnabled = %v, want true", cfg.AutoInjectEnabled)
	}

	if err := LoadPersistedSettings(ctx, cfg, d.Config); err != nil {
		t.Fatalf("LoadPersistedSettings: %v", err)
	}

	if cfg.AutoInjectEnabled != false {
		t.Fatalf("AutoInjectEnabled = %v, want false (persisted)", cfg.AutoInjectEnabled)
	}
}

func TestLoadPersistedSettings_MissingKeysKeepDefaults(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	cfg := config.Default()
	defAuto := cfg.AutoInjectEnabled

	if err := LoadPersistedSettings(ctx, cfg, d.Config); err != nil {
		t.Fatalf("LoadPersistedSettings: %v", err)
	}

	if cfg.AutoInjectEnabled != defAuto {
		t.Fatalf("AutoInjectEnabled mutated: got %v, want %v", cfg.AutoInjectEnabled, defAuto)
	}
}

func TestLoadPersistedSettings_CorruptAutoInjectAborts(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	if err := d.Config.Set(ctx, config.KeyAutoInjectEnabled, "maybe"); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	cfg := config.Default()
	err := LoadPersistedSettings(ctx, cfg, d.Config)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), config.KeyAutoInjectEnabled) {
		t.Fatalf("error must name the key %q: %v", config.KeyAutoInjectEnabled, err)
	}
	if !strings.Contains(err.Error(), "maybe") {
		t.Fatalf("error must name the offending value %q: %v", "maybe", err)
	}
}
