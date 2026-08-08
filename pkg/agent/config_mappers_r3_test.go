package agent

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestMapMCPServerPolicies_NilAndEmpty(t *testing.T) {
	if got := mapMCPServerPolicies(nil); got != nil {
		t.Errorf("nil cfg: expected nil, got %v", got)
	}
	if got := mapMCPServerPolicies(&config.Config{}); got != nil {
		t.Errorf("empty cfg: expected nil, got %v", got)
	}
}

func TestMapMCPServerPolicies_DenyByDefaultFromConfig(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"context7":   {Enabled: true, Type: "http", URL: "https://mcp.context7.com/mcp"},
		"filesystem": {Enabled: false, Command: "npx"},
	}
	cfg.Security.MCP.PerServerRPM = 60
	cfg.Security.MCP.DefaultCapabilities = map[string]bool{"sampling": false}
	cfg.Security.MCP.DefaultAllowResources = true

	policies := mapMCPServerPolicies(cfg)
	if len(policies) != 1 {
		t.Fatalf("expected 1 policy (disabled server skipped), got %d", len(policies))
	}
	p := policies[0]
	if p.Name != "context7" {
		t.Errorf("Name = %q, want context7", p.Name)
	}
	if p.TrustLevel != "external" {
		t.Errorf("TrustLevel = %q, want external", p.TrustLevel)
	}
	if len(p.AllowedTools) != 0 {
		t.Errorf("AllowedTools must be empty (deny-by-default), got %v", p.AllowedTools)
	}
	if p.RPM != 60 {
		t.Errorf("RPM = %d, want 60", p.RPM)
	}
	if !p.AllowResources {
		t.Error("AllowResources must come from security.mcp.default_allow_resources")
	}
	if p.Capabilities == nil {
		t.Error("Capabilities must come from security.mcp.default_capabilities")
	}
}

func TestPikaPromptPaths_CoversAllCRComponents(t *testing.T) {
	paths := pikaPromptPaths("/ws")
	want := []string{"archivist", "atomizer", "reflexor", "mcp_guard"}
	if len(paths) != len(want) {
		t.Fatalf("expected %d paths, got %d", len(want), len(paths))
	}
	for _, c := range want {
		p, ok := paths[c]
		if !ok || p == "" {
			t.Errorf("missing prompt path for %q", c)
		}
	}
}
