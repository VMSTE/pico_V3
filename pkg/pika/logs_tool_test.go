// Волна 107 (ТЗ-107): search_logs.

package pika

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

var _ toolshared.Tool = (*SearchLogsTool)(nil)

func setupSearchLogs(t *testing.T) (string, *SearchLogsTool) {
	t.Helper()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "logs"), 0o700); err != nil {
		t.Fatal(err)
	}
	return ws, NewSearchLogsTool(ws)
}

func writeTestLogLines(t *testing.T, ws, name string, lines []string) {
	t.Helper()
	content := strings.Join(lines, "\n") + "\n"
	err := os.WriteFile(filepath.Join(ws, "logs", name), []byte(content), 0o600)
	if err != nil {
		t.Fatal(err)
	}
}

func sampleLogLines() []string {
	now := time.Now().Format(time.RFC3339)
	old := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	return []string{
		`{"level":"debug","time":"` + now + `","component":"agent","message":"dbg heartbeat"}`,
		`{"level":"info","time":"` + now + `","component":"pico","message":"user said hello"}`,
		`{"level":"warn","time":"` + old + `","component":"gateway","message":"channel slow"}`,
		`{"level":"error","time":"` + now + `","component":"pico","message":"send failed"}`,
		`not a json line`,
	}
}

func TestSearchLogs_BasicQuery(t *testing.T) {
	ws, tool := setupSearchLogs(t)
	writeTestLogLines(t, ws, "gateway.log", sampleLogLines())

	res := tool.Execute(context.Background(), map[string]any{"query": "hello"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "matched=1") {
		t.Errorf("expected matched=1 (malformed line skipped), got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "user said hello") {
		t.Errorf("missing matching line: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "INFO [pico]") {
		t.Errorf("expected formatted level+component: %s", res.ForLLM)
	}
}

func TestSearchLogs_LevelFilter(t *testing.T) {
	ws, tool := setupSearchLogs(t)
	writeTestLogLines(t, ws, "gateway.log", sampleLogLines())

	res := tool.Execute(context.Background(), map[string]any{"level": "warn"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "matched=2") {
		t.Errorf("expected matched=2, got: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "user said hello") {
		t.Errorf("info line must be filtered out: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "send failed") ||
		!strings.Contains(res.ForLLM, "channel slow") {
		t.Errorf("warn+ lines missing: %s", res.ForLLM)
	}
}

func TestSearchLogs_SinceMinutes(t *testing.T) {
	ws, tool := setupSearchLogs(t)
	writeTestLogLines(t, ws, "gateway.log", sampleLogLines())

	res := tool.Execute(context.Background(), map[string]any{
		"since_minutes": 30,
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "matched=3") {
		t.Errorf("expected matched=3 (2h-old warn dropped), got: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "channel slow") {
		t.Errorf("old entry must be dropped: %s", res.ForLLM)
	}
}

func TestSearchLogs_LimitKeepsNewest(t *testing.T) {
	ws, tool := setupSearchLogs(t)
	writeTestLogLines(t, ws, "gateway.log", sampleLogLines())

	res := tool.Execute(context.Background(), map[string]any{"limit": 2})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "matched=4 showing=2") {
		t.Errorf("expected matched=4 showing=2, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "send failed") {
		t.Errorf("newest line missing: %s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "dbg heartbeat") {
		t.Errorf("oldest line must be dropped: %s", res.ForLLM)
	}
}

func TestSearchLogs_MissingFileIsHintNotError(t *testing.T) {
	_, tool := setupSearchLogs(t)

	res := tool.Execute(context.Background(), map[string]any{})
	if res.IsError {
		t.Fatalf("missing file must not be an error, got: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "log file not found") {
		t.Errorf("expected hint text, got: %s", res.ForLLM)
	}
}

func TestSearchLogs_ErrorsSource(t *testing.T) {
	ws, tool := setupSearchLogs(t)
	writeTestLogLines(t, ws, "errors.log", []string{
		`{"level":"error","time":"` + time.Now().Format(time.RFC3339) +
			`","component":"pico","message":"only in errors"}`,
	})

	res := tool.Execute(context.Background(), map[string]any{"source": "errors"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "only in errors") {
		t.Errorf("errors.log not read: %s", res.ForLLM)
	}
}

func TestSearchLogs_InvalidLevel(t *testing.T) {
	_, tool := setupSearchLogs(t)
	res := tool.Execute(context.Background(), map[string]any{"level": "bogus"})
	if !res.IsError {
		t.Error("expected error for invalid level")
	}
}

func TestSearchLogs_InvalidSource(t *testing.T) {
	_, tool := setupSearchLogs(t)
	res := tool.Execute(context.Background(), map[string]any{"source": "secret"})
	if !res.IsError {
		t.Error("expected error for invalid source")
	}
}

func TestSearchLogs_EmptyWorkspace(t *testing.T) {
	tool := NewSearchLogsTool("")
	res := tool.Execute(context.Background(), map[string]any{})
	if !res.IsError {
		t.Error("expected error for empty workspace")
	}
}
