package api

import (
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
	if len(info.EnvKeys) != 1 || info.EnvKeys[0] != "API_KEY" {
		t.Errorf("EnvKeys = %v, want [API_KEY]", info.EnvKeys)
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
