package pika

import (
	"path/filepath"
	"testing"
)

// Волна 108 (D-AUDIT-131): миграция v7 — artifact_passports.
// Чисто добавочная (expand-only): существующие таблицы не трогаются.
func TestMigrateV7_ArtifactPassports(t *testing.T) {
	db, err := Migrate(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	defer db.Close()

	var name string
	if err := db.QueryRow(
		`SELECT name FROM sqlite_master
		WHERE type='table' AND name='artifact_passports'`,
	).Scan(&name); err != nil {
		t.Fatalf("artifact_passports missing after v7: %v", err)
	}

	if _, err := db.Exec(
		`INSERT INTO artifact_passports (path, tool)
		VALUES ('docs/a.txt','write_file')`,
	); err != nil {
		t.Fatalf("insert passport: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO artifact_passports (path, tool) VALUES ('docs/a.txt','edit_file')
		ON CONFLICT(path) DO UPDATE SET tool=excluded.tool`,
	); err != nil {
		t.Fatalf("upsert passport: %v", err)
	}
	var tool string
	var cnt int
	if err := db.QueryRow(
		`SELECT tool, (SELECT COUNT(*) FROM artifact_passports)
		FROM artifact_passports`,
	).Scan(&tool, &cnt); err != nil {
		t.Fatal(err)
	}
	if tool != "edit_file" || cnt != 1 {
		t.Errorf("upsert: tool=%q cnt=%d, want edit_file/1", tool, cnt)
	}
}
