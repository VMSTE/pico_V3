package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestMCPServerInfoFromConfig_StdioMasksSecrets(t *testing.T) {
	deferVal := true
	srv := config.MCPServerConfig{
		Enabled:  true,
		Deferred: &deferVal,
		Command:  "npx",
		Args:     []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
		Env:      map[string]string{"API_KEY": "supersecret"},
	}
	info := mcpServerInfoFromConfig("fs", srv)
	if info.Type != "stdio" {
		t.Errorf("Type = %q, want stdio", info.Type)
	}
	if info.Target != "npx -y @modelcontextprotocol/server-filesystem /tmp" {
		t.Errorf("Target = %q", info.Target)
	}
	if info.Env["API_KEY"] != mcpMaskedSecret {
		t.Errorf("Env[API_KEY] = %q, want masked", info.Env["API_KEY"])
	}
	// The secret value must not appear anywhere in the info struct output.
	if info.Target == "supersecret" {
		t.Error("secret leaked into Target")
	}
}

func TestMCPServerInfoFromConfig_Remote(t *testing.T) {
	srv := config.MCPServerConfig{Enabled: true, URL: "https://mcp.example.com/mcp"}
	info := mcpServerInfoFromConfig("remote", srv)
	if info.Type != "sse" {
		t.Errorf("Type = %q, want sse (inferred from url)", info.Type)
	}
	if info.Target != "https://mcp.example.com/mcp" {
		t.Errorf("Target = %q", info.Target)
	}
}

func TestValidateMCPServerRequest(t *testing.T) {
	if err := validateMCPServerRequest("ok-name_1", &mcpServerRequest{Command: "npx"}); err != nil {
		t.Errorf("valid stdio request rejected: %v", err)
	}
	if err := validateMCPServerRequest("ok", &mcpServerRequest{URL: "https://x", Type: "http"}); err != nil {
		t.Errorf("valid http request rejected: %v", err)
	}
	if err := validateMCPServerRequest("bad name!", &mcpServerRequest{Command: "npx"}); err == nil {
		t.Error("invalid name accepted")
	}
	if err := validateMCPServerRequest("ok", &mcpServerRequest{Type: "carrier-pigeon"}); err == nil {
		t.Error("invalid type accepted")
	}
	if err := validateMCPServerRequest("ok", &mcpServerRequest{}); err == nil {
		t.Error("empty command+url accepted")
	}
}

func TestMaskMCPServerSecrets(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"github": {
			Enabled: true,
			Command: "npx",
			Env:     map[string]string{"GITHUB_TOKEN": "ghp_secret", "EMPTY": ""},
			Headers: map[string]string{"Authorization": "Bearer xyz"},
		},
	}
	maskMCPServerSecrets(cfg)
	srv := cfg.Tools.MCP.Servers["github"]
	if srv.Env["GITHUB_TOKEN"] != mcpMaskedSecret {
		t.Errorf("env value not masked: %q", srv.Env["GITHUB_TOKEN"])
	}
	if srv.Env["EMPTY"] != "" {
		t.Errorf("empty value should stay empty, got %q", srv.Env["EMPTY"])
	}
	if srv.Headers["Authorization"] != mcpMaskedSecret {
		t.Errorf("header value not masked: %q", srv.Headers["Authorization"])
	}
	if _, ok := srv.Env["GITHUB_TOKEN"]; !ok {
		t.Error("key name must be preserved")
	}
}

func TestRestoreMCPServerSecrets(t *testing.T) {
	old := &config.Config{}
	old.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"github": {Env: map[string]string{"GITHUB_TOKEN": "ghp_real"}},
	}

	fresh := &config.Config{}
	fresh.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"github": {Env: map[string]string{
			"GITHUB_TOKEN": mcpMaskedSecret, // клиент вернул маску
			"NEW_VAR":      "brand-new",     // новое значение
		}},
		"brand-new-server": {Env: map[string]string{"K": "v"}},
	}

	restoreMCPServerSecrets(fresh, old)

	got := fresh.Tools.MCP.Servers["github"]
	if got.Env["GITHUB_TOKEN"] != "ghp_real" {
		t.Errorf("masked value not restored: %q", got.Env["GITHUB_TOKEN"])
	}
	if got.Env["NEW_VAR"] != "brand-new" {
		t.Errorf("new value clobbered: %q", got.Env["NEW_VAR"])
	}
	if fresh.Tools.MCP.Servers["brand-new-server"].Env["K"] != "v" {
		t.Error("unknown server must be untouched")
	}
}

func writeMCPServerConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	initial := `{"version":3,"tools":{"mcp":{"enabled":true,` +
		`"servers":{"fs":{"enabled":true,"command":"npx","env":{"K":"secret"}}}}}}`
	if err := os.WriteFile(configPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func TestPatchMCPServer_TogglesEnabledKeepsSecrets(t *testing.T) {
	configPath := writeMCPServerConfig(t)
	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerMCPRoutes(mux)

	req := httptest.NewRequest(
		http.MethodPatch, "/api/mcp/servers/fs", strings.NewReader(`{"enabled":false}`),
	)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := cfg.Tools.MCP.Servers["fs"]
	if srv.Enabled {
		t.Error("expected enabled=false after PATCH")
	}
	if srv.Env["K"] != "secret" {
		t.Errorf("env must be untouched by PATCH, got %q", srv.Env["K"])
	}
}

func TestPutMCPServer_RestoresMaskedSecrets(t *testing.T) {
	configPath := writeMCPServerConfig(t)
	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.registerMCPRoutes(mux)

	body := `{"enabled":true,"command":"npx","env":{"K":"[NOT_HERE]","NEW":"v2"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/mcp/servers/fs", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	srv := cfg.Tools.MCP.Servers["fs"]
	if srv.Env["K"] != "secret" {
		t.Errorf("masked value not restored, got %q", srv.Env["K"])
	}
	if srv.Env["NEW"] != "v2" {
		t.Errorf("new value lost, got %q", srv.Env["NEW"])
	}
}
