package pika

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Волна 109 (ТЗ-109): машина времени — чекпоинты + откат.

func setupCheckpoints(t *testing.T) (string, *CheckpointManager) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	ws := t.TempDir()
	return ws, NewCheckpointManager(ws)
}

func writeWS(t *testing.T, ws, rel, content string) {
	t.Helper()
	p := filepath.Join(ws, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readWS(t *testing.T, ws, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ws, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func shaOf(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func seedPassport(t *testing.T, mem *BotMemory, path, sha string) {
	t.Helper()
	_, err := mem.db.ExecContext(context.Background(),
		`INSERT INTO artifact_passports (path, tool, sha256) VALUES (?,?,?)`,
		path, "write_file", sha)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCheckpoint_TakenAndSkippedWhenNoChange(t *testing.T) {
	ws, m := setupCheckpoints(t)
	writeWS(t, ws, "a.txt", "v1")

	m.MaybeCheckpoint("before write_file")
	if got := len(m.List()); got != 1 {
		t.Fatalf("checkpoints = %d, want 1", got)
	}
	if m.List()[0].Reason != "before write_file" {
		t.Errorf("reason = %q", m.List()[0].Reason)
	}

	m.MaybeCheckpoint("before write_file") // нет изменений — пропуск
	if got := len(m.List()); got != 1 {
		t.Fatalf("no-change checkpoint must be skipped, got %d", got)
	}

	writeWS(t, ws, "a.txt", "v2")
	m.MaybeCheckpoint("before edit_file")
	if got := len(m.List()); got != 2 {
		t.Fatalf("checkpoints = %d, want 2", got)
	}
}

func TestCheckpoint_ExcludesNoise(t *testing.T) {
	ws, m := setupCheckpoints(t)
	writeWS(t, ws, "doc.md", "real work")
	writeWS(t, ws, "memory/bot_memory.db", "db bytes")
	writeWS(t, ws, "logs/gateway.log", "log line")
	writeWS(t, ws, "files/2026-09/upload.pdf", "pdf")

	m.MaybeCheckpoint("before write_file")
	if len(m.List()) != 1 {
		t.Fatal("no checkpoint taken")
	}
	out, err := m.runGit("ls-files")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"memory/", "logs/", "files/", ".vault/"} {
		if strings.Contains(out, bad) {
			t.Errorf("excluded path %q leaked into checkpoint:\n%s", bad, out)
		}
	}
	if !strings.Contains(out, "doc.md") {
		t.Errorf("doc.md missing from checkpoint:\n%s", out)
	}
}

func TestCheckpoint_DisabledWithoutGit(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // пустой PATH — git не найдётся
	m := NewCheckpointManager(t.TempDir())
	if m.Enabled() {
		t.Fatal("must be disabled without git")
	}
	m.MaybeCheckpoint("noop") // не паникует, ничего не делает
	if m.List() != nil {
		t.Error("List must be nil when disabled")
	}
}

func TestRollback_RestoresPikaFile(t *testing.T) {
	ws, m := setupCheckpoints(t)
	mem := setupTestDB(t)

	writeWS(t, ws, "doc.md", "original")
	m.MaybeCheckpoint("before write_file")
	first := m.List()[0].Hash

	writeWS(t, ws, "doc.md", "broken by agent")
	seedPassport(t, mem, "doc.md", shaOf("broken by agent"))

	restored, skipped, err := m.Rollback(context.Background(), first, mem, false)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := readWS(t, ws, "doc.md"); got != "original" {
		t.Errorf("doc.md = %q, want original", got)
	}
	if len(restored) != 1 || len(skipped) != 0 {
		t.Errorf("restored=%v skipped=%v", restored, skipped)
	}
}

func TestRollback_KeepsHandEdits(t *testing.T) {
	ws, m := setupCheckpoints(t)
	mem := setupTestDB(t)

	writeWS(t, ws, "doc.md", "original")
	m.MaybeCheckpoint("before write_file")
	first := m.List()[0].Hash

	writeWS(t, ws, "doc.md", "broken by agent")
	seedPassport(t, mem, "doc.md", shaOf("broken by agent"))
	// founder правит руками ПОСЛЕ Пики — паспорт протух
	writeWS(t, ws, "doc.md", "hand edit by founder")

	restored, skipped, err := m.Rollback(context.Background(), first, mem, false)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := readWS(t, ws, "doc.md"); got != "hand edit by founder" {
		t.Errorf("hand edit must survive rollback, got %q", got)
	}
	if len(skipped) != 1 || skipped[0] != "doc.md" {
		t.Errorf("skipped = %v, want [doc.md]", skipped)
	}
	if len(restored) != 0 {
		t.Errorf("restored = %v, want empty", restored)
	}
}

func TestRollback_ForceRestoresEverything(t *testing.T) {
	ws, m := setupCheckpoints(t)
	mem := setupTestDB(t)

	writeWS(t, ws, "doc.md", "original")
	m.MaybeCheckpoint("before write_file")
	first := m.List()[0].Hash
	writeWS(t, ws, "doc.md", "hand edit") // паспорта нет — но force

	_, skipped, err := m.Rollback(context.Background(), first, mem, true)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := readWS(t, ws, "doc.md"); got != "original" {
		t.Errorf("force must restore, got %q", got)
	}
	if len(skipped) != 0 {
		t.Errorf("force must not skip: %v", skipped)
	}
}

func TestRollback_PreRollbackSnapshotExists(t *testing.T) {
	ws, m := setupCheckpoints(t)
	mem := setupTestDB(t)

	writeWS(t, ws, "doc.md", "v1")
	m.MaybeCheckpoint("before write_file")
	writeWS(t, ws, "doc.md", "v2")
	seedPassport(t, mem, "doc.md", shaOf("v2"))

	if _, _, err := m.Rollback(context.Background(), "1", mem, false); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	found := false
	for _, c := range m.List() {
		if strings.HasPrefix(c.Reason, "pre-rollback") {
			found = true
		}
	}
	if !found {
		t.Error("pre-rollback snapshot missing — undo-the-undo impossible")
	}
}

func TestRollback_RemovesNewPikaFileKeepsUserFile(t *testing.T) {
	ws, m := setupCheckpoints(t)
	mem := setupTestDB(t)

	writeWS(t, ws, "doc.md", "v1")
	m.MaybeCheckpoint("before write_file")

	// Пика создала новый файл после чекпоинта (паспорт совпадает)
	writeWS(t, ws, "agent-draft.md", "draft")
	seedPassport(t, mem, "agent-draft.md", shaOf("draft"))
	// founder создал свой файл (паспорта нет)
	writeWS(t, ws, "my-notes.md", "mine")

	restored, skipped, err := m.Rollback(context.Background(), "1", mem, false)
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "agent-draft.md")); !os.IsNotExist(err) {
		t.Error("agent-created file must be removed on rollback")
	}
	if got := readWS(t, ws, "my-notes.md"); got != "mine" {
		t.Errorf("user file must survive, got %q", got)
	}
	if len(skipped) != 1 || skipped[0] != "my-notes.md" {
		t.Errorf("skipped = %v, want [my-notes.md]", skipped)
	}
	if len(restored) != 1 {
		t.Errorf("restored = %v", restored)
	}
}

func TestRollback_InvalidTarget(t *testing.T) {
	ws, m := setupCheckpoints(t)
	writeWS(t, ws, "a.txt", "v1")
	m.MaybeCheckpoint("before write_file")

	if _, _, err := m.Rollback(context.Background(), "zzz", nil, false); err == nil {
		t.Error("invalid target must error")
	}
	if _, _, err := m.Rollback(context.Background(), "99", nil, false); err == nil {
		t.Error("out-of-range number must error")
	}
}
