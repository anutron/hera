package daemon

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/anutron/hera/internal/config"
	"github.com/anutron/hera/internal/db"
)

// LoadPersistedSettings reads hera's two persisted settings keys from the
// config table and overwrites the corresponding Config fields. Missing keys
// are a no-op (the Default() value stands). A corrupt or out-of-range value
// is fatal: it returns a wrapped error naming both the key and the offending
// value so the daemon can surface it on startup and exit non-zero (see
// design D6 and D7).
//
// Wiring this into daemon.Start is the integration step's responsibility;
// this function only delivers the helper.
func LoadPersistedSettings(ctx context.Context, cfg *config.Config, dao *db.ConfigDAO) error {
	if v, err := dao.Get(ctx, config.KeyIdleDebounceSeconds); err == nil {
		n, parseErr := strconv.Atoi(v)
		if parseErr != nil {
			return fmt.Errorf("hera: persisted setting %q has invalid value %q: %w",
				config.KeyIdleDebounceSeconds, v, parseErr)
		}
		if n < 0 {
			return fmt.Errorf("hera: persisted setting %q has invalid value %q: must be >= 0",
				config.KeyIdleDebounceSeconds, v)
		}
		cfg.IdleDebounce = time.Duration(n) * time.Second
	} else if !errors.Is(err, db.ErrNotFound) {
		return fmt.Errorf("hera: read persisted setting %q: %w", config.KeyIdleDebounceSeconds, err)
	}

	if v, err := dao.Get(ctx, config.KeyAutoInjectEnabled); err == nil {
		b, parseErr := strconv.ParseBool(v)
		if parseErr != nil {
			return fmt.Errorf("hera: persisted setting %q has invalid value %q: %w",
				config.KeyAutoInjectEnabled, v, parseErr)
		}
		cfg.AutoInjectEnabled = b
	} else if !errors.Is(err, db.ErrNotFound) {
		return fmt.Errorf("hera: read persisted setting %q: %w", config.KeyAutoInjectEnabled, err)
	}

	return nil
}
