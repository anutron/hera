package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ConfigDAO is the typed accessor for the config k/v table.
type ConfigDAO struct{ db *sql.DB }

// Get returns the value for a key, or ErrNotFound if the key is absent.
func (c *ConfigDAO) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := c.db.QueryRowContext(ctx, `SELECT value FROM config WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("config.Get: %w", err)
	}
	return v, nil
}

// Set upserts a config key/value.
func (c *ConfigDAO) Set(ctx context.Context, key, value string) error {
	_, err := c.db.ExecContext(ctx,
		`INSERT INTO config (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("config.Set: %w", err)
	}
	return nil
}
