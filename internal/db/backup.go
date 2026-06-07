package db

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// keepBackups is the maximum number of pre-migration backup files to retain.
// When a new backup is created and the total count exceeds this limit, the
// oldest files (by modification time) are deleted.
const keepBackups = 5

// backupBeforeMigrate creates a point-in-time copy of the SQLite file at
// d.path before any pending migrations run. VACUUM INTO is used so the backup
// is always a complete, single-file, WAL-free snapshot regardless of the live
// DB's journal mode.
//
// The filename encodes the target schema version so it is easy to match a
// backup to the upgrade it preceded: state.sqlite.pre-7.bak was taken
// immediately before the DB was upgraded to version 7.
//
// If a backup with that name already exists (e.g. a prior failed upgrade
// attempt), it is left untouched and we proceed without error — the existing
// file already captured the pre-migration state.
//
// Backup failures are fatal: if we cannot create the safety-net file we do
// not proceed with migrations. Pruning old files is best-effort (non-fatal).
func (d *DB) backupBeforeMigrate(toVersion int) error {
	if d.path == "" {
		return nil // in-memory or anonymous connection; nothing to back up
	}

	backupPath := fmt.Sprintf("%s.pre-%d.bak", d.path, toVersion)

	if _, err := os.Stat(backupPath); err == nil {
		// Backup already exists from a prior attempt; the pre-migration state
		// is already captured. Nothing to do.
		return nil
	}

	// VACUUM INTO creates a complete, consistent snapshot. It does not accept
	// bound parameters, so we format the path inline. Single quotes in the
	// path are escaped to avoid SQL syntax errors.
	escaped := strings.ReplaceAll(backupPath, "'", "''")
	if _, err := d.sqldb.Exec(fmt.Sprintf(`VACUUM INTO '%s'`, escaped)); err != nil {
		return fmt.Errorf("VACUUM INTO %s: %w", backupPath, err)
	}

	_ = pruneBackups(d.path, keepBackups) // best-effort; a prune failure is not worth aborting for

	return nil
}

// pruneBackups deletes the oldest pre-migration backup files for the given
// DB path, keeping at most keep files. Files are sorted by modification time;
// the oldest are removed first. Errors from individual removals are silently
// ignored — pruning is best-effort.
func pruneBackups(dbPath string, keep int) error {
	pattern := dbPath + ".pre-*.bak"
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) <= keep {
		return err
	}

	type entry struct {
		path  string
		mtime time.Time
	}
	var entries []entry
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil {
			entries = append(entries, entry{path: m, mtime: fi.ModTime()})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].mtime.Before(entries[j].mtime) })

	for _, e := range entries[:len(entries)-keep] {
		_ = os.Remove(e.path)
	}
	return nil
}
