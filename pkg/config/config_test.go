package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"

	"github.com/sipeed/picoclaw/pkg/credential"
)

// mustSetupSSHKey generates a temporary Ed25519 SSH key in t.TempDir() and sets
// PICOCLAW_SSH_KEY_PATH to its path for the duration of the test. This is required
// whenever a test exercises encryption/decryption via credential.Encrypt or SaveConfig.
func mustSetupSSHKey(t *testing.T) {
	t.Helper()
	keyPath := filepath.Join(t.TempDir(), "picoclaw_ed25519.key")
	if err := credential.GenerateSSHKey(keyPath); err != nil {
		t.Fatalf("mustSetupSSHKey: %v", err)
	}
	t.Setenv("PICOCLAW_SSH_KEY_PATH", keyPath)
}

func TestAgentModelConfig_UnmarshalString(t *testing.T) {
	var m AgentModelConfig
	if err := json.Unmarshal([]byte(`"gpt-4"`), &m); err != nil {
		t.Fatalf("unmarshal string: %v", err)
	}
	if m.Primary != "gpt-4" {
		t.Errorf("Primary = %q, want 'gpt-4'", m.Primary)
	}
	if m.Fallbacks != nil {
		t.Errorf("Fallbacks = %v, want nil", m.Fallbacks)
	}
}

func TestAgentModelConfig_UnmarshalObject(t *testing.T) {
	var m AgentModelConfig
	data := `{"primary": "claude-opus", "fallbacks": ["gpt-4o-mini", "haiku"]}`
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		t.Fatalf("unmarshal object: %v", err)
	}
	if m.Primary != "claude-opus" {
		t.Errorf("Primary = %q, want 'claude-opus'", m.Primary)
	}
	if len(m.Fallbacks) != 2 {
		t.Fatalf("Fallbacks len = %d, want 2", len(m.Fallbacks))
	}
	if m.Fallbacks[0] != "gpt-4o-mini" || m.Fallbacks[1] != "haiku" {
		t.Errorf("Fallbacks = %v", m.Fallbacks)
	}
}

func TestAgentModelConfig_MarshalString(t *testing.T) {
	m := AgentModelConfig{Primary: "gpt-4"}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `"gpt-4"` {
		t.Errorf("marshal = %s, want '\"gpt-4\"'", string(data))
	}
}

func TestAgentModelConfig_MarshalObject(t *testing.T) {
	m := AgentModelConfig{Primary: "claude-opus", Fallbacks: []string{"haiku"}}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var result map[string]any
	json.Unmarshal(data, &result)
	if result["primary"] != "claude-opus" {
		t.Errorf("primary = %v", result["primary"])
	}
}

func TestAgentConfig_FullParse(t *testing.T) {
	jsonData := `{
		"agents": {
			"defaults": {
				"workspace": "~/.picoclaw/workspace",
				"model": "glm-4.7",
				"max_tokens": 8192,
				"max_tool_iterations": 20
			},
			"list": [
				{
					"id": "sales",
					"default": true,
					"name": "Sales Bot",
					"model": "gpt-4"
				},
				{
					"id": "support",
					"name": "Support Bot",
					"model": {
						"primary": "claude-opus",
						"fallbacks": ["haiku"]
					},
					"subagents": {
						"allow_agents": ["sales"]
					}
				}
			]
		},
		"session": {
			"dimensions": ["sender"],
			"identity_links": {
				"john": ["telegram:123", "discord:john#1234"]
			}
		}
	}`

	cfg := DefaultConfig()
	if err := json.Unmarshal([]byte(jsonData), cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(cfg.Agents.List) != 2 {
		t.Fatalf("agents.list len = %d, want 2", len(cfg.Agents.List))
	}

	sales := cfg.Agents.List[0]
	if sales.ID != "sales" || !sales.Default || sales.Name != "Sales Bot" {
		t.Errorf("sales = %+v", sales)
	}
	if sales.Model == nil || sales.Model.Primary != "gpt-4" {
		t.Errorf("sales.Model = %+v", sales.Model)
	}

	support := cfg.Agents.List[1]
	if support.ID != "support" || support.Name != "Support Bot" {
		t.Errorf("support = %+v", support)
	}
	if support.Model == nil || support.Model.Primary != "claude-opus" {
		t.Errorf("support.Model = %+v", support.Model)
	}
	if len(support.Model.Fallbacks) != 1 || support.Model.Fallbacks[0] != "haiku" {
		t.Errorf("support.Model.Fallbacks = %v", support.Model.Fallbacks)
	}
	if support.Subagents == nil || len(support.Subagents.AllowAgents) != 1 {
		t.Errorf("support.Subagents = %+v", support.Subagents)
	}

	if len(cfg.Session.Dimensions) != 1 || cfg.Session.Dimensions[0] != "sender" {
		t.Errorf("Session.Dimensions = %v", cfg.Session.Dimensions)
	}
	if len(cfg.Session.IdentityLinks) != 1 {
		t.Errorf("Session.IdentityLinks = %v", cfg.Session.IdentityLinks)
	}
	links := cfg.Session.IdentityLinks["john"]
	if len(links) != 2 {
		t.Errorf("john links = %v", links)
	}
}

func TestDefaultConfig_MCPMaxInlineTextChars(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Tools.MCP.GetMaxInlineTextChars() != DefaultMCPMaxInlineTextChars {
		t.Fatalf(
			"DefaultConfig().Tools.MCP.GetMaxInlineTextChars() = %d, want %d",
			cfg.Tools.MCP.GetMaxInlineTextChars(),
			DefaultMCPMaxInlineTextChars,
		)
	}
}

func TestLoadConfig_MCPMaxInlineTextChars(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := `{
		"version": 3,
		"tools": {
			"mcp": {
				"enabled": true,
				"max_inline_text_chars": 2048
			}
		}
	}`
	if err := os.WriteFile(configPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile(configPath): %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if got := cfg.Tools.MCP.GetMaxInlineTextChars(); got != 2048 {
		t.Fatalf("cfg.Tools.MCP.GetMaxInlineTextChars() = %d, want 2048", got)
	}
}

func TestConfig_BackwardCompat_NoAgentsList(t *testing.T) {
	jsonData := `{
		"agents": {
			"defaults": {
				"workspace": "~/.picoclaw/workspace",
				"model": "glm-4.7",
				"max_tokens": 8192,
				"max_tool_iterations": 20
			}
		}
	}`

	cfg := DefaultConfig()
	if err := json.Unmarshal([]byte(jsonData), cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(cfg.Agents.List) != 0 {
		t.Errorf("agents.list should be empty for backward compat, got %d", len(cfg.Agents.List))
	}
}

func TestAgentConfig_ParsesDispatchRules(t *testing.T) {
	jsonData := `{
		"agents": {
			"defaults": {
				"workspace": "~/.picoclaw/workspace",
				"model": "glm-4.7"
			},
			"list": [
				{ "id": "main", "default": true },
				{ "id": "support" }
			],
			"dispatch": {
				"rules": [
					{
						"name": "support-vip",
						"agent": "support",
						"when": {
							"channel": "telegram",
							"chat": "group:-100123",
							"sender": "12345",
							"mentioned": true
						},
						"session_dimensions": ["chat", "sender"]
					}
				]
			}
		}
	}`

	cfg := DefaultConfig()
	if err := json.Unmarshal([]byte(jsonData), cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.Agents.Dispatch == nil {
		t.Fatal("Agents.Dispatch should not be nil")
	}
	if len(cfg.Agents.Dispatch.Rules) != 1 {
		t.Fatalf("Dispatch.Rules len = %d, want 1", len(cfg.Agents.Dispatch.Rules))
	}
	rule := cfg.Agents.Dispatch.Rules[0]
	if rule.Name != "support-vip" || rule.Agent != "support" {
		t.Fatalf("rule = %+v", rule)
	}
	if rule.When.Channel != "telegram" || rule.When.Chat != "group:-100123" || rule.When.Sender != "12345" {
		t.Fatalf("rule.When = %+v", rule.When)
	}
	if rule.When.Mentioned == nil || !*rule.When.Mentioned {
		t.Fatalf("rule.When.Mentioned = %+v, want true", rule.When.Mentioned)
	}
	if got := rule.SessionDimensions; len(got) != 2 || got[0] != "chat" || got[1] != "sender" {
		t.Fatalf("rule.SessionDimensions = %v, want [chat sender]", got)
	}
}

// TestDefaultConfig_HeartbeatEnabled verifies heartbeat is enabled by default
func TestDefaultConfig_HeartbeatEnabled(t *testing.T) {
	cfg := DefaultConfig()

	if !cfg.Heartbeat.Enabled {
		t.Error("Heartbeat should be enabled by default")
	}
}

// TestDefaultConfig_WorkspacePath verifies workspace path is correctly set
func TestDefaultConfig_WorkspacePath(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.Workspace == "" {
		t.Error("Workspace should not be empty")
	}
}

// TestDefaultConfig_MaxTokens verifies max tokens has default value
func TestDefaultConfig_MaxTokens(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.MaxTokens == 0 {
		t.Error("MaxTokens should not be zero")
	}
}

// TestDefaultConfig_MaxToolIterations verifies max tool iterations has default value
func TestDefaultConfig_MaxToolIterations(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.MaxToolIterations == 0 {
		t.Error("MaxToolIterations should not be zero")
	}
}

// TestDefaultConfig_Temperature verifies temperature has default value
func TestDefaultConfig_Temperature(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.Temperature != nil {
		t.Error("Temperature should be nil when not provided")
	}
}

// TestDefaultConfig_Gateway verifies gateway defaults
func TestDefaultConfig_Gateway(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Gateway.Host != "localhost" {
		t.Error("Gateway host should have default value")
	}
	if cfg.Gateway.Port == 0 {
		t.Error("Gateway port should have default value")
	}
	if cfg.Gateway.HotReload {
		t.Error("Gateway hot reload should be disabled by default")
	}
}

// TestDefaultConfig_Channels verifies channels are disabled by default
func TestDefaultConfig_Channels(t *testing.T) {
	cfg := DefaultConfig()

	for name, bc := range cfg.Channels {
		if bc.Enabled {
			t.Errorf("Channel %q should be disabled by default", name)
		}
	}
}

func TestValidateSingletonChannels_RejectsMultipleInstances(t *testing.T) {
	channels := ChannelsConfig{
		"pico1": &Channel{Enabled: true, Type: ChannelPico},
		"pico2": &Channel{Enabled: true, Type: ChannelPico},
	}
	err := validateSingletonChannels(channels)
	if err == nil {
		t.Fatal("expected error for multiple pico channels, got nil")
	}
	if !strings.Contains(err.Error(), "singleton") {
		t.Fatalf("expected singleton error, got: %v", err)
	}
}

func TestValidateSingletonChannels_AllowsSingleInstance(t *testing.T) {
	channels := ChannelsConfig{
		"pico1": &Channel{Enabled: true, Type: ChannelPico},
	}
	err := validateSingletonChannels(channels)
	if err != nil {
		t.Fatalf("expected no error for single pico channel, got: %v", err)
	}
}

func TestValidateSingletonChannels_IgnoresDisabledInstances(t *testing.T) {
	channels := ChannelsConfig{
		"pico1": &Channel{Enabled: true, Type: ChannelPico},
		"pico2": &Channel{Enabled: false, Type: ChannelPico},
	}
	err := validateSingletonChannels(channels)
	if err != nil {
		t.Fatalf("expected no error when only one pico channel is enabled, got: %v", err)
	}
}

func TestValidateSingletonChannels_AllowsMultiInstanceTypes(t *testing.T) {
	channels := ChannelsConfig{
		"tg1": &Channel{Enabled: true, Type: ChannelTelegram},
		"tg2": &Channel{Enabled: true, Type: ChannelTelegram},
	}
	err := validateSingletonChannels(channels)
	if err != nil {
		t.Fatalf("telegram should allow multiple instances, got error: %v", err)
	}
}

// TestDefaultConfig_WebTools verifies web tools config
func TestDefaultConfig_WebTools(t *testing.T) {
	cfg := DefaultConfig()

	// Verify web tools defaults
	if cfg.Tools.Web.Brave.MaxResults != 5 {
		t.Error("Expected Brave MaxResults 5, got ", cfg.Tools.Web.Brave.MaxResults)
	}
	if len(cfg.Tools.Web.Brave.APIKeys) != 0 {
		t.Error("Brave API key should be empty by default")
	}
	if cfg.Tools.Web.DuckDuckGo.MaxResults != 5 {
		t.Error("Expected DuckDuckGo MaxResults 5, got ", cfg.Tools.Web.DuckDuckGo.MaxResults)
	}
}

func TestSaveConfig_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits are not enforced on Windows")
	}

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	cfg := DefaultConfig()
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("config file has permission %04o, want 0600", perm)
	}
}

func TestSaveConfig_IncludesEmptyLegacyModelField(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	cfg := DefaultConfig()
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	if !strings.Contains(string(data), `"model_name": ""`) {
		t.Fatalf("saved config should include empty legacy model_name field, got: %s", string(data))
	}
}

func TestSaveConfig_PreservesDisabledTelegramPlaceholder(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	cfg := DefaultConfig()
	if bc := cfg.Channels.Get("telegram"); bc != nil {
		bc.Placeholder.Enabled = false
	}

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !strings.Contains(string(data), `"placeholder": {`) {
		t.Fatalf("saved config should include telegram placeholder config, got: %s", string(data))
	}
	if !strings.Contains(string(data), `"enabled": false`) {
		t.Fatalf("saved config should persist placeholder.enabled=false, got: %s", string(data))
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	bc := loaded.Channels.Get("telegram")
	if bc != nil && bc.Placeholder.Enabled {
		t.Fatal("telegram placeholder should remain disabled after SaveConfig/LoadConfig round-trip")
	}
}

// TestSaveConfig_FiltersVirtualModels verifies that SaveConfig does not write
// virtual models (generated by expandMultiKeyModels) to the config file.
func TestSaveConfig_FiltersVirtualModels(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")

	cfg := DefaultConfig()

	// Manually add a virtual model to ModelList (simulating what expandMultiKeyModels does)
	primaryModel := &ModelConfig{
		ModelName: "gpt-4",
		Model:     "openai/gpt-4o",
		APIKeys:   SimpleSecureStrings("key1"),
	}
	virtualModel := &ModelConfig{
		ModelName: "gpt-4__key_1",
		Model:     "openai/gpt-4o",
		APIKeys:   SimpleSecureStrings("key2"),
		isVirtual: true,
	}
	cfg.ModelList = []*ModelConfig{primaryModel, virtualModel}

	// SaveConfig should filter out virtual models
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	// Reload and verify
	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// Should only have the primary model, not the virtual one
	if len(reloaded.ModelList) != 1 {
		t.Fatalf("expected 1 model after reload, got %d", len(reloaded.ModelList))
	}

	if reloaded.ModelList[0].ModelName != "gpt-4" {
		t.Errorf("expected model_name 'gpt-4', got %q", reloaded.ModelList[0].ModelName)
	}

	// Verify virtual model was not persisted
	for _, m := range reloaded.ModelList {
		if m.ModelName == "gpt-4__key_1" {
			t.Errorf("virtual model gpt-4__key_1 should not have been saved")
		}
	}

	// Verify the saved file does not contain the virtual model name
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if strings.Contains(string(data), "gpt-4__key_1") {
		t.Errorf("saved config should not contain virtual model name 'gpt-4__key_1'")
	}
}

// TestConfig_Complete verifies all config fields are set
func TestConfig_Complete(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Agents.Defaults.Workspace == "" {
		t.Error("Workspace should not be empty")
	}
	if cfg.Agents.Defaults.Temperature != nil {
		t.Error("Temperature should be nil when not provided")
	}
	if cfg.Agents.Defaults.MaxTokens == 0 {
		t.Error("MaxTokens should not be zero")
	}
	if cfg.Agents.Defaults.MaxToolIterations == 0 {
		t.Error("MaxToolIterations should not be zero")
	}
	if cfg.Gateway.