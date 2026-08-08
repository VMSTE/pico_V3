package agent

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/pika"
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

// Р-5 шаг 5: per-server ACL переопределяет дефолты, пустое наследует.
func TestMapMCPServerPolicies_PerServerACL(t *testing.T) {
	allow := false
	cfg := &config.Config{}
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"github":   {Enabled: true, Type: "stdio", Command: "npx"},
		"internal": {Enabled: true, Type: "http", URL: "http://localhost/mcp"},
		"off":      {Enabled: false},
	}
	cfg.Security.MCP.PerServerRPM = 60
	cfg.Security.MCP.DefaultAllowResources = true
	cfg.Security.MCP.Servers = map[string]config.MCPServerACLConfig{
		"github": {
			TrustLevel:     "external",
			AllowedTools:   []string{"get_issue", "list_issues"},
			AllowResources: &allow,
			RPM:            10,
		},
		"internal": {
			TrustLevel: "internal",
		},
	}

	policies := mapMCPServerPolicies(cfg)
	if len(policies) != 2 {
		t.Fatalf("expected 2 policies (disabled skipped), got %d", len(policies))
	}
	byName := map[string]pika.MCPServerPolicy{}
	for _, p := range policies {
		byName[p.Name] = p
	}

	gh := byName["github"]
	if gh.TrustLevel != "external" || gh.RPM != 10 {
		t.Errorf("github overrides not applied: %+v", gh)
	}
	if len(gh.AllowedTools) != 2 {
		t.Errorf("github allowed_tools: %v", gh.AllowedTools)
	}
	if gh.AllowResources {
		t.Error("github allow_resources must be overridden to false")
	}
	if gh.AllowPrompts {
		t.Error("github allow_prompts must inherit default false")
	}

	in := byName["internal"]
	if in.TrustLevel != "internal" {
		t.Errorf("internal trust_level: %q", in.TrustLevel)
	}
	if !in.AllowResources {
		t.Error("internal must inherit default_allow_resources=true")
	}
	if in.RPM != 60 {
		t.Errorf("internal must inherit per_server_rpm=60, got %d", in.RPM)
	}
}

// Р-2: mapRADConfig — дефолтные фразы доезжают, переопределения работают.
func TestMapRADConfig_DefaultsCarryPhrases(t *testing.T) {
	cfg := mapRADConfig(config.RADConfig{Enabled: true})
	if len(cfg.PatternKeywordsRU) == 0 || len(cfg.PatternKeywordsEN) == 0 {
		t.Error("default phrases must reach the detector (was the Р-2 bug)")
	}
	if cfg.DriftThreshold != 0.2 || cfg.BlockScore != 3 || cfg.WarnScore != 2 {
		t.Errorf("default thresholds: %+v", cfg)
	}
}

func TestMapRADConfig_Overrides(t *testing.T) {
	cfg := mapRADConfig(config.RADConfig{
		Enabled:           true,
		BlockScore:        4, // warn-режим на первый прогон (D-AUDIT-53)
		PatternKeywordsRU: []string{"кастомная фраза"},
	})
	if cfg.BlockScore != 4 {
		t.Errorf("block_score override: %d", cfg.BlockScore)
	}
	if len(cfg.PatternKeywordsRU) != 1 ||
		cfg.PatternKeywordsRU[0] != "кастомная фраза" {
		t.Errorf("custom RU phrases: %v", cfg.PatternKeywordsRU)
	}
	if len(cfg.PatternKeywordsEN) == 0 {
		t.Error("EN phrases must fall back to defaults")
	}
}
