// PIKA-V3: migrate_test.go — Tests for bot_memory.db schema migration
package pika

import (
	"path/filepath"
	"testing"
)

func tempDBPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test_bot_memory.db")
}

// TestMigrateNewDB — new DB → version=1 + наличие ключевых таблиц/FTS/триггеров
func TestMigrateNewDB(t *testing.T) {
	db, err := Migrate(tempDBPath(t))
	if err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	defer db.Close()

	v, err := CurrentVersion(db)
	if err != nil {
		t.Fatalf("CurrentVersion failed: %v", err)
	}
	if v != 1 {
		t.Fatalf("expected version 1, got %d", v)
	}

	// Check key tables exist
	expectedTables := []string{
		"schema_version",
		"messages",
		"events",
		"knowledge_atoms",
		"messages_archive",
		"events_archive",
		"registry",
		"request_log",
		"reasoning_log",
		"reasoning_log_archive",
		"trace_spans",
		"prompt_versions",
		"prompt_snapshots",
		"atom_usage",
		"daily_metrics",
	}

	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	defer rows.Close()

	tables := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan: %v", err)
		}
		tables[name] = true
	}

	for _, tbl := range expectedTables {
		if !tables[tbl] {
			t.Errorf("table %q not found", tbl)
		}
	}

	// Check FTS5 virtual tables
	expectedVirtual := []string{
		"knowledge_fts",
		"events_archive_fts",
	}
	for _, vt := range expectedVirtual {
		var count int
		err := db.QueryRow(
			"SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", vt,
		).Scan(&count)
		if err != nil {
			t.Fatalf("check virtual table %q: %v", vt, err)
		}
		if count == 0 {
			t.Errorf("virtual table %q not found", vt)
		}
	}

	// Check triggers
	expectedTriggers := []string{
		"katoms_ai",
		"katoms_ad",
		"katoms_au",
		"events_archive_ai",
	}
	for _, tr := range expectedTriggers {
		var count int
		err := db.QueryRow(
			"SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name=?", tr,
		).Scan(&count)
		if err != nil {
			t.Fatalf("check trigger %q: %v", tr, err)
		}
		if count == 0 {
			t.Errorf("trigger %q not found", tr)
		}
	}
}

// TestMigrateIdempotent — 2× Migrate on same DB, no error, version still 1
func TestMigrateIdempotent(t *testing.T) {
	dbPath := tempDBPath(t)

	db1, err := Migrate(dbPath)
	if err != nil {
		t.Fatalf("first Migrate failed: %v", err)
	}
	db1.Close()

	db2, err := Migrate(dbPath)
	if err != nil {
		t.Fatalf("second Migrate failed: %v", err)
	}
	defer db2.Close()

	v, err := CurrentVersion(db2)
	if err != nil {
		t.Fatalf("CurrentVersion failed: %v", err)
	}
	if v != 1 {
		t.Fatalf("expected version 1 after double migrate, got %d", v)
	}
}

// TestMigratePragmas — pragmas (wal + foreign_keys)
func TestMigratePragmas(t *testing.T) {
	db, err := Migrate(tempDBPath(t))
	if err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	defer db.Close()

	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want %q", journalMode, "wal")
	}

	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("PRAGMA foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}

// TestFTS5Works — FTS5 реально работает (smoke-тест MATCH)
func TestFTS5Works(t *testing.T) {
	db, err := Migrate(tempDBPath(t))
	if err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	defer db.Close()

	// Insert a knowledge atom — trigger katoms_ai syncs to knowledge_fts
	_, err = db.Exec(`
		INSERT INTO knowledge_atoms
			(atom_id, session_id, turn_id, category, summary, detail, tags)
		VALUES
			('T-1', 'sess-1', 1, 'pattern', 'test deploy pattern', 'deploy details here', '["deploy","docker"]')
	`)
	if err != nil {
		t.Fatalf("INSERT knowledge_atoms: %v", err)
	}

	// FTS5 MATCH query via knowledge_fts
	var rowid int
	err = db.QueryRow(
		"SELECT rowid FROM knowledge_fts WHERE knowledge_fts MATCH 'deploy'",
	).Scan(&rowid)
	if err != nil {
		t.Fatalf("FTS5 MATCH query failed: %v", err)
	}
	if rowid == 0 {
		t.Error("FTS5 returned rowid 0, expected > 0")
	}
}
