package pika

import (
	"os"
	"path/filepath"
	"testing"
)

// PIKA-V3: migrate_test.go — Tests for bot_memory.db schema migration

func TestMigrateNewDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := Migrate(dbPath)
	if err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	defer db.Close()

	// Check version == 1
	ver, err := CurrentVersion(db)
	if err != nil {
		t.Fatalf("CurrentVersion failed: %v", err)
	}
	if ver != 1 {
		t.Fatalf("expected version 1, got %d", ver)
	}

	// Check key tables exist
	expected := map[string]bool{
		"messages": false, "events": false, "knowledge_atoms": false,
		"knowledge_fts": false, "messages_archive": false,
		"events_archive": false, "events_archive_fts": false,
		"registry": false, "request_log": false, "reasoning_log": false,
		"reasoning_log_archive": false, "trace_spans": false,
		"prompt_versions": false, "prompt_snapshots": false,
		"atom_usage": false, "daily_metrics": false,
		"schema_version": false,
	}
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		t.Fatalf("query tables: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			t.Fatalf("scan: %v", scanErr)
		}
		if _, ok := expected[name]; ok {
			expected[name] = true
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		t.Fatalf("rows iteration: %v", rowsErr)
	}
	for tbl, found := range expected {
		if !found {
			t.Errorf("table %q not found", tbl)
		}
	}

	// Check triggers (FTS5 sync)
	triggers := []string{"katoms_ai", "katoms_ad", "katoms_au", "events_archive_ai"}
	for _, trg := range triggers {
		var cnt int
		if qErr := db.QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name=?", trg,
		).Scan(&cnt); qErr != nil {
			t.Fatalf("check trigger %s: %v", trg, qErr)
		}
		if cnt != 1 {
			t.Errorf("trigger %q not found", trg)
		}
	}
}

func TestMigrateIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

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

	ver, err := CurrentVersion(db2)
	if err != nil {
		t.Fatalf("CurrentVersion failed: %v", err)
	}
	if ver != 1 {
		t.Fatalf("expected version 1 after second Migrate, got %d", ver)
	}
}

func TestMigratePragmas(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := Migrate(dbPath)
	if err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	defer db.Close()

	// WAL
	var journalMode string
	if qErr := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); qErr != nil {
		t.Fatalf("query journal_mode: %v", qErr)
	}
	if journalMode != "wal" {
		t.Errorf("expected journal_mode=wal, got %q", journalMode)
	}

	// foreign_keys
	var fk int
	if qErr := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); qErr != nil {
		t.Fatalf("query foreign_keys: %v", qErr)
	}
	if fk != 1 {
		t.Errorf("expected foreign_keys=1, got %d", fk)
	}
}

func TestFTS5Works(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := Migrate(dbPath)
	if err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}
	defer db.Close()

	// Insert a knowledge atom
	_, err = db.Exec(`INSERT INTO knowledge_atoms
		(atom_id, session_id, turn_id, category, summary, detail, tags)
		VALUES ('P-1', 'sess-1', 1, 'pattern', 'deploy OOM fix', 'Increased memory limit to 512MB', '["deploy","OOM"]')`)
	if err != nil {
		t.Fatalf("insert atom: %v", err)
	}

	// FTS5 MATCH query
	var matchID int
	if qErr := db.QueryRow(
		"SELECT rowid FROM knowledge_fts WHERE knowledge_fts MATCH 'deploy'",
	).Scan(&matchID); qErr != nil {
		t.Fatalf("FTS5 MATCH failed: %v", qErr)
	}
	if matchID == 0 {
		t.Error("FTS5 MATCH returned zero rowid")
	}

	// Cleanup: remove temp files
	_ = os.RemoveAll(dir)
}
