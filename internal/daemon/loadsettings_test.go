package daemon

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestLoadPersistedSettings_PersistedDebounceWins(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	if err := d.Config.Set(ctx, config.KeyIdleDebounceSeconds, "5"); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	cfg := config.Default()
	if err := LoadPersistedSettings(ctx, cfg, d.Config); err != nil {
		t.Fatalf("LoadPersistedSettings: %v", err)
	}

	if cfg.IdleDebounce != 5*time.Second {
		t.Fatalf("IdleDebounce = %v, want 5s", cfg.IdleDebounce)
	}
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
	defDebounce := cfg.IdleDebounce
	defAuto := cfg.AutoInjectEnabled

	if err := LoadPersistedSettings(ctx, cfg, d.Config); err != nil {
		t.Fatalf("LoadPersistedSettings: %v", err)
	}

	if cfg.IdleDebounce != defDebounce {
		t.Fatalf("IdleDebounce mutated: got %v, want %v", cfg.IdleDebounce, defDebounce)
	}
	if cfg.AutoInjectEnabled != defAuto {
		t.Fatalf("AutoInjectEnabled mutated: got %v, want %v", cfg.AutoInjectEnabled, defAuto)
	}
}

func TestLoadPersistedSettings_CorruptDebounceAborts(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	if err := d.Config.Set(ctx, config.KeyIdleDebounceSeconds, "not-an-int"); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	cfg := config.Default()
	err := LoadPersistedSettings(ctx, cfg, d.Config)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), config.KeyIdleDebounceSeconds) {
		t.Fatalf("error must name the key %q: %v", config.KeyIdleDebounceSeconds, err)
	}
	if !strings.Contains(err.Error(), "not-an-int") {
		t.Fatalf("error must name the offending value %q: %v", "not-an-int", err)
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

func TestLoadPersistedSettings_NegativeDebounceRejected(t *testing.T) {
	ctx := context.Background()
	d := openTestDB(t)

	if err := d.Config.Set(ctx, config.KeyIdleDebounceSeconds, "-1"); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	cfg := config.Default()
	err := LoadPersistedSettings(ctx, cfg, d.Config)
	if err == nil {
		t.Fatalf("expected error for negative debounce, got nil")
	}
	if !strings.Contains(err.Error(), config.KeyIdleDebounceSeconds) {
		t.Fatalf("error must name the key %q: %v", config.KeyIdleDebounceSeconds, err)
	}
	if !strings.Contains(err.Error(), "-1") {
		t.Fatalf("error must name the offending value %q: %v", "-1", err)
	}
}
