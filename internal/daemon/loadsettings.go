package daemon

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/anutron/hera/internal/config"
	"github.com/anutron/hera/internal/db"
)

// LoadPersistedSettings reads hera's persisted settings keys from the config
// table and overwrites the corresponding Config fields. Missing keys are a
// no-op (the Default() value stands). A corrupt value is fatal: it returns a
// wrapped error naming the key and offending value.
func LoadPersistedSettings(ctx context.Context, cfg *config.Config, dao *db.ConfigDAO) error {
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
