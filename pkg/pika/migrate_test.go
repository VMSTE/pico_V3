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
	if ver != 6 {
		t.Fatalf("expected version 6, got %d", ver)
	}

	// Check key tables exist
	expected := map[string]bool{
		"messages": false, "events": false, "knowledge_atoms": false,
		"knowledge_fts": false, "messages_fts": false, "messages_archive": false,
		"events_archive": false, "events_archive_fts": false,
		"registry": false, "request_log": false, "reasoning_log": false,
		"reasoning_log_archive": false, "trace_spans": false,
		"prompt_versions": false, "prompt_snapshots": false,
		"atom_usage": false, "daily_metrics": false,
		"schema_version": false, "reasoning_fts": false,
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
	triggers := []string{
		"katoms_ai", "katoms_ad", "katoms_au", "events_archive_ai",
		"rlog_fts_ai", "rlog_fts_ad", "rlog_fts_au",
	}
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
	if ver != 6 {
		t.Fatalf("expected version 6 after second Migrate, got %d", ver)
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
		(atom_id, chat_id, pika_session_id, category, summary, detail, tags)
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

// PIKA-V3 (D-AUDIT-108): request_log.agent_id существует после миграции.
func TestMigrateV4AgentIDColumn(t *testing.T) {
	db, err := Migrate(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	defer db.Close()

	var name string
	err = db.QueryRow(
		`SELECT name FROM pragma_table_info('request_log') WHERE name='agent_id'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("agent_id column missing in request_log: %v", err)
	}
}

// Волна 94: под тестовым бинарём путь на .picoclaw перенаправляется
// во временный файл — боевой путь не создаётся и не трогается.
// Регрессия на бой 20 авг (go test писал в продовую bot_memory.db).
func TestMigrate_TestGuardRedirectsProdPath(t *testing.T) {
	prodLike := filepath.Join(
		t.TempDir(), ".picoclaw", "workspace", "memory", "bot_memory.db",
	)
	db, err := Migrate(prodLike)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	defer db.Close()

	if _, err := os.Stat(prodLike); !os.IsNotExist(err) {
		t.Fatalf("prod-like path touched under tests: %v", err)
	}
	var v int
	if err := db.QueryRow(
		"SELECT COALESCE(MAX(version),0) FROM schema_version",
	).Scan(&v); err != nil {
		t.Fatalf("redirected db not migrated: %v", err)
	}
	if v < 4 {
		t.Fatalf("redirected db version = %d, want >= 4", v)
	}
}

// D-AUDIT-126 (волна 103): бэкфилл провенанса детерминирован и идемпотентен.
func TestMigrateV6_BackfillsProvenance(t *testing.T) {
	db, err := Migrate(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`INSERT INTO messages_archive
		(id, chat_id, pika_session_id, ts, role, tokens, blob)
		VALUES (42, 's1', '1', '2026-08-22 10:00:00', 'user', 5, NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO knowledge_atoms
		(atom_id, chat_id, pika_session_id, category, summary,
		 confidence, polarity, source_turns)
		VALUES ('S-1', 's1', '1', 'summary', 'old atom without provenance',
		        0.5, 'neutral', '["1"]')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrationV6); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	var smid int64
	if err := db.QueryRow(
		`SELECT COALESCE(source_message_id,0)
		FROM knowledge_atoms WHERE atom_id='S-1'`,
	).Scan(&smid); err != nil {
		t.Fatal(err)
	}
	if smid != 42 {
		t.Errorf("source_message_id = %d, want 42", smid)
	}
	if _, err := db.Exec(migrationV6); err != nil {
		t.Fatal(err)
	}
	var cnt int
	if err := db.QueryRow(
		`SELECT json_array_length(history)
		FROM knowledge_atoms WHERE atom_id='S-1'`,
	).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Errorf("history entries = %d, want 1 (idempotent)", cnt)
	}
}
