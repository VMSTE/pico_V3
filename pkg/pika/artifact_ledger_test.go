package pika

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// Волна 108 (ТЗ-108): паспорта артефактов (таблица artifact_passports).

func setupArtifactLedger(t *testing.T) (*ArtifactLedger, *BotMemory, string) {
	t.Helper()
	ws := t.TempDir()
	mem := setupTestDB(t)
	return NewArtifactLedger(mem, ws), mem, ws
}

func artifactCtx() context.Context {
	return toolshared.WithToolSessionContext(
		context.Background(), "main", "sk_v1_9", nil,
	)
}

func TestArtifactLedger_RecordWritesPassport(t *testing.T) {
	ledger, mem, ws := setupArtifactLedger(t)
	if err := os.MkdirAll(filepath.Join(ws, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(ws, "docs", "a.txt"), []byte("hello"), 0o600,
	); err != nil {
		t.Fatal(err)
	}

	ledger.Record(artifactCtx(), "write_file", map[string]any{"path": "docs/a.txt"})

	var sha, sess, tool string
	err := mem.db.QueryRow(
		`SELECT sha256, session, tool
		FROM artifact_passports WHERE path='docs/a.txt'`,
	).Scan(&sha, &sess, &tool)
	if err != nil {
		t.Fatalf("passport missing: %v", err)
	}
	h := sha256.Sum256([]byte("hello"))
	if sha != hex.EncodeToString(h[:]) {
		t.Errorf("sha256 = %q, want %q", sha, hex.EncodeToString(h[:]))
	}
	if sess != "sk_v1_9" {
		t.Errorf("session = %q, want sk_v1_9", sess)
	}
	if tool != "write_file" {
		t.Errorf("tool = %q, want write_file", tool)
	}
}

func TestArtifactLedger_UpsertOnSecondWrite(t *testing.T) {
	ledger, mem, ws := setupArtifactLedger(t)
	p := filepath.Join(ws, "a.txt")
	if err := os.WriteFile(p, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	ledger.Record(artifactCtx(), "write_file", map[string]any{"path": "a.txt"})
	if err := os.WriteFile(p, []byte("v2-longer"), 0o600); err != nil {
		t.Fatal(err)
	}
	ledger.Record(artifactCtx(), "edit_file", map[string]any{"path": "a.txt"})

	var sha, tool string
	var cnt int
	err := mem.db.QueryRow(
		`SELECT sha256, tool, (SELECT COUNT(*) FROM artifact_passports)
		FROM artifact_passports WHERE path='a.txt'`,
	).Scan(&sha, &tool, &cnt)
	if err != nil {
		t.Fatalf("passport missing after upsert: %v", err)
	}
	h := sha256.Sum256([]byte("v2-longer"))
	if sha != hex.EncodeToString(h[:]) {
		t.Errorf("passport must show the LATEST hash, got %q", sha)
	}
	if tool != "edit_file" {
		t.Errorf("passport must show the LATEST tool, got %q", tool)
	}
	if cnt != 1 {
		t.Errorf("one row per path, got %d", cnt)
	}
}

func TestArtifactLedger_MissingFileAndDirNoRow(t *testing.T) {
	ledger, mem, _ := setupArtifactLedger(t)
	ledger.Record(artifactCtx(), "write_file", map[string]any{"path": "gone.txt"})
	ledger.Record(artifactCtx(), "write_file", map[string]any{"path": "."})

	var cnt int
	if err := mem.db.QueryRow(
		`SELECT COUNT(*) FROM artifact_passports`,
	).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 0 {
		t.Errorf("missing file / directory must not produce passports, got %d", cnt)
	}
}

func TestArtifactLedger_NilMemSafe(t *testing.T) {
	ledger := NewArtifactLedger(nil, t.TempDir())
	ledger.Record(artifactCtx(), "write_file", map[string]any{"path": "x"})
	// no panic = pass
}

func TestArtifactLedger_PassportVisibleInSearchMemory(t *testing.T) {
	ledger, mem, ws := setupArtifactLedger(t)
	if err := os.WriteFile(
		filepath.Join(ws, "report.md"), []byte("quarterly"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	ledger.Record(artifactCtx(), "write_file", map[string]any{"path": "report.md"})

	ms := NewMemorySearch(mem)
	res, err := ms.searchArtifacts(context.Background(), "report", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || !strings.Contains(res[0].Summary, "report.md") {
		t.Fatalf("passport not searchable: %#v", res)
	}
}
