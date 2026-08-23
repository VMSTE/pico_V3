package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

// Волна 106 (ТЗ-106, срез B): чтение файловых логов из workspace/logs/.

func setupLogsTest(t *testing.T) (*http.ServeMux, string) {
	t.Helper()
	ws := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = ws
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	h := NewHandler(cfgPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return mux, ws
}

func writeTestGatewayLog(t *testing.T, ws string) {
	t.Helper()
	dir := filepath.Join(ws, "logs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"level":"debug","component":"agent","time":"t1","message":"dbg line"}`,
		`{"level":"info","component":"agent","time":"t2","message":"hello vision satellite"}`,
		`{"level":"warn","component":"pico","time":"t3","message":"careful"}`,
		`{"level":"error","component":"agent","time":"t4","message":"boom"}`,
		`not json at all`,
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "gateway.log"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func getLogs(t *testing.T, mux *http.ServeMux, query string) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/logs"+query, nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return body
}

func entryMessages(t *testing.T, body map[string]any) []string {
	t.Helper()
	raw, ok := body["entries"].([]any)
	if !ok {
		t.Fatalf("entries missing: %#v", body)
	}
	var out []string
	for _, item := range raw {
		m := item.(map[string]any)
		out = append(out, m["message"].(string))
	}
	return out
}

func TestLogsEndpoint_ParseAndDefaults(t *testing.T) {
	mux, ws := setupLogsTest(t)
	writeTestGatewayLog(t, ws)

	body := getLogs(t, mux, "")
	if body["available"] != true {
		t.Fatal("expected available")
	}
	msgs := entryMessages(t, body)
	if len(msgs) != 4 {
		t.Fatalf("entries = %d, want 4 (malformed line skipped)", len(msgs))
	}
	if msgs[1] != "hello vision satellite" {
		t.Errorf("entry order/content off: %v", msgs)
	}
	first := body["entries"].([]any)[0].(map[string]any)
	if first["level"] != "debug" || first["component"] != "agent" {
		t.Errorf("parsed fields wrong: %#v", first)
	}
}

func TestLogsEndpoint_LevelFilter(t *testing.T) {
	mux, ws := setupLogsTest(t)
	writeTestGatewayLog(t, ws)

	body := getLogs(t, mux, "?level=warn")
	msgs := entryMessages(t, body)
	if len(msgs) != 2 || msgs[0] != "careful" || msgs[1] != "boom" {
		t.Fatalf("level=warn entries = %v, want [careful boom]", msgs)
	}
}

func TestLogsEndpoint_SearchFilter(t *testing.T) {
	mux, ws := setupLogsTest(t)
	writeTestGatewayLog(t, ws)

	body := getLogs(t, mux, "?q=vision")
	msgs := entryMessages(t, body)
	if len(msgs) != 1 || msgs[0] != "hello vision satellite" {
		t.Fatalf("q=vision entries = %v", msgs)
	}
}

func TestLogsEndpoint_FollowAndReset(t *testing.T) {
	mux, ws := setupLogsTest(t)
	writeTestGatewayLog(t, ws)

	body := getLogs(t, mux, "?offset=3")
	msgs := entryMessages(t, body)
	if len(msgs) != 1 || msgs[0] != "boom" {
		t.Fatalf("follow offset=3 entries = %v, want [boom]", msgs)
	}

	body = getLogs(t, mux, "?offset=999")
	if body["reset"] != true {
		t.Fatal("expected reset=true")
	}
	if got := len(entryMessages(t, body)); got != 4 {
		t.Fatalf("reset entries = %d, want 4", got)
	}
}

func TestLogsEndpoint_ErrorsSourceMissing(t *testing.T) {
	mux, _ := setupLogsTest(t)
	body := getLogs(t, mux, "?source=errors")
	if body["available"] != false {
		t.Fatal("missing errors.log must report available=false")
	}
}

func TestLogsEndpoint_UnknownSource(t *testing.T) {
	mux, _ := setupLogsTest(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/logs?source=nope", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
