package db

import (
	"testing"
)

// TestMigration0009_NudgeColumnsDropped verifies that after Open() the messages
// table does NOT have nudge_count or nudged_at columns. These were used by the
// deleted DeliveryWatcher; migration 0009 drops them.
func TestMigration0009_NudgeColumnsDropped(t *testing.T) {
	d, err := Open(t.TempDir() + "/test.sqlite")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = d.Close() }()

	rows, err := d.sqldb.Query(`PRAGMA table_info(messages)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var found []string
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dflt, pk any
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if name == "nudge_count" || name == "nudged_at" {
			found = append(found, name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	if len(found) > 0 {
		t.Fatalf("messages table still has deleted nudge columns: %v", found)
	}
}
