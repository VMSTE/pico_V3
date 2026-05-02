package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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

func TestDefaultConfig_HeartbeatEnabled(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Heartbeat.Enabled {
		t.Error("Heartbeat should be enabled by default")
	}
}

func TestDefaultConfig_WorkspacePath(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Agents.Defaults.Workspace == "" {
		t.Error("Workspace should not be empty")
	}
}

func TestDefaultConfig_MaxTokens(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Agents.Defaults.MaxTokens == 0 {
		t.Error("MaxTokens should not be zero")
	}
}

func TestDefaultConfig_MaxToolIterations(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Agents.Defaults.MaxToolIterations == 0 {
		t.Error("MaxToolIterations should not be zero")
	}
}

func TestDefaultConfig_Temperature(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Agents.Defaults.Temperature != nil {
		t.Error("Temperature should be nil when not provided")
	}
}

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
	if err := validateSingletonChannels(channels); err != nil {
		t.Fatalf("expected no error for single pico channel, got: %v", err)
	}
}

func TestValidateSingletonChannels_IgnoresDisabledInstances(t *testing.T) {
	channels := ChannelsConfig{
		"pico1": &Channel{Enabled: true, Type: ChannelPico},
		"pico2": &Channel{Enabled: false, Type: ChannelPico},
	}
	if err := validateSingletonChannels(channels); err != nil {
		t.Fatalf("expected no error when only one pico channel is enabled, got: %v", err)
	}
}

func TestValidateSingletonChannels_AllowsMultiInstanceTypes(t *testing.T) {
	channels := ChannelsConfig{
		"tg1": &Channel{Enabled: true, Type: ChannelTelegram},
		"tg2": &Channel{Enabled: true, Type: ChannelTelegram},
	}
	if err := validateSingletonChannels(channels); err != nil {
		t.Fatalf("telegram should allow multiple instances, got error: %v", err)
	}
}

func TestDefaultConfig_WebTools(t *testing.T) {
	cfg := DefaultConfig()
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

func TestSaveConfig_FiltersVirtualModels(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "config.json")
	cfg := DefaultConfig()
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
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}
	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(reloaded.ModelList) != 1 {
		t.Fatalf("expected 1 model after reload, got %d", len(reloaded.ModelList))
	}
	if reloaded.ModelList[0].ModelName != "gpt-4" {
		t.Errorf("expected model_name 'gpt-4', got %q", reloaded.ModelList[0].ModelName)
	}
	for _, m := range reloaded.ModelList {
		if m.ModelName == "gpt-4__key_1" {
			t.Errorf("virtual model gpt-4__key_1 should not have been saved")
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if strings.Contains(string(data), "gpt-4__key_1") {
		t.Errorf("saved config should not contain virtual model name 'gpt-4__key_1'")
	}
}

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
	if cfg.Gateway.Host != "localhost" {
		t.Error("Gateway host should have default value")
	}
	if cfg.Gateway.Port == 0 {
		t.Error("Gateway port should have default value")
	}
	if !cfg.Heartbeat.Enabled {
		t.Error("Heartbeat should be enabled by default")
	}
	if !cfg.Tools.Exec.AllowRemote {
		t.Error("Exec.AllowRemote should be true by default")
	}
}

func TestDefaultConfig_WebPreferNativeEnabled(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Tools.Web.PreferNative {
		t.Fatal("DefaultConfig().Tools.Web.PreferNative should be true")
	}
}

func TestDefaultConfig_WebProviderIsAuto(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Tools.Web.Provider != "auto" {
		t.Fatalf("DefaultConfig().Tools.Web.Provider = %q, want auto", cfg.Tools.Web.Provider)
	}
}

func TestConfigExample_WebProviderIsAuto(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config", "config.example.json"))
	if err != nil {
		t.Fatalf("ReadFile(config.example.json) error: %v", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("Unmarshal(config.example.json) error: %v", err)
	}
	if cfg.Tools.Web.Provider != "auto" {
		t.Fatalf("config.example.json tools.web.provider = %q, want auto", cfg.Tools.Web.Provider)
	}
}

func TestDefaultConfig_ToolFeedbackDisabled(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Agents.Defaults.ToolFeedback.Enabled {
		t.Fatal("DefaultConfig().Agents.Defaults.ToolFeedback.Enabled should be false")
	}
	if cfg.Agents.Defaults.ToolFeedback.SeparateMessages {
		t.Fatal("DefaultConfig().Agents.Defaults.ToolFeedback.SeparateMessages should be false")
	}
}

func TestLoadConfig_ToolFeedbackDefaultsFalseWhenUnset(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cfgJSON := `{"version":3,"agents":{"defaults":{"workspace":"./workspace"}}}`
	if err := os.WriteFile(configPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.Agents.Defaults.ToolFeedback.Enabled {
		t.Fatal("agents.defaults.tool_feedback.enabled should remain false when unset in config file")
	}
	if cfg.Agents.Defaults.ToolFeedback.SeparateMessages {
		t.Fatal("agents.defaults.tool_feedback.separate_messages should remain false when unset in config file")
	}
}

func TestLoadConfig_WebPreferNativeDefaultsTrueWhenUnset(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"version":3,"tools":{"web":{"enabled":true}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if !cfg.Tools.Web.PreferNative {
		t.Fatal("PreferNative should remain true when unset in config file")
	}
}

func TestLoadConfig_WebPreferNativeCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cfgJSON := `{"version":3,"tools":{"web":{"prefer_native":false}}}`
	if err := os.WriteFile(configPath, []byte(cfgJSON), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.Tools.Web.PreferNative {
		t.Fatal("PreferNative should be false when disabled in config file")
	}
}

func TestLoadConfig_SyntaxErrorReportsLineAndColumn(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := "{\n  \"version\": 3,\n  \"tools\": {\n    \"web\": {\n      \"enabled\": true,,\n      \"format\": \"markdown\"\n    }\n  }\n}\n"
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	_, err := LoadConfig(configPath)
	if err == nil {
		t.Fatal("expected syntax error, got nil")
	}
	if !strings.Contains(err.Error(), "syntax error at line 5, column 23") {
		t.Fatalf("expected line/column diagnostic, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "\"enabled\": true,,") {
		t.Fatalf("expected source snippet in diagnostic, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "^") {
		t.Fatalf("expected caret marker in diagnostic, got %q", err.Error())
	}
}

func TestLoadConfig_TypeErrorReportsFieldPath(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := "{\n  \"version\": 3,\n  \"tools\": {\n    \"web\": {\n      \"fetch_limit_bytes\": \"oops\"\n    }\n  }\n}\n"
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	_, err := LoadConfig(configPath)
	if err == nil {
		t.Fatal("expected type error, got nil")
	}
	if !strings.Contains(err.Error(), "type error at line 5, column 33") {
		t.Fatalf("expected line/column diagnostic, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "fetch_limit_bytes") {
		t.Fatalf("expected field name in diagnostic, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "\"fetch_limit_bytes\": \"oops\"") {
		t.Fatalf("expected source snippet in diagnostic, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "^") {
		t.Fatalf("expected caret marker in diagnostic, got %q", err.Error())
	}
}

func TestLoadConfig_UnknownFieldsReportsExactPaths(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := "{\n  \"version\": 3,\n  \"tools\": {\n    \"weeb\": {\n      \"enabled\": true\n    },\n    \"web\": {\n      \"fatch_limit_bytes\": 123\n    }\n  }\n}\n"
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	_, err := LoadConfig(configPath)
	if err == nil {
		t.Fatal("expected unknown field error, got nil")
	}
	if !strings.Contains(err.Error(), "tools.weeb") || !strings.Contains(err.Error(), "tools.web.fatch_limit_bytes") {
		t.Fatalf("expected exact unknown field paths, got %q", err.Error())
	}
}

func TestDefaultConfig_ExecAllowRemoteEnabled(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Tools.Exec.AllowRemote {
		t.Fatal("DefaultConfig().Tools.Exec.AllowRemote should be true")
	}
}

func TestDefaultConfig_FilterSensitiveDataEnabled(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Tools.FilterSensitiveData {
		t.Fatal("DefaultConfig().Tools.FilterSensitiveData should be true")
	}
}

func TestDefaultConfig_FilterMinLength(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Tools.FilterMinLength != 8 {
		t.Fatalf("DefaultConfig().Tools.FilterMinLength = %d, want 8", cfg.Tools.FilterMinLength)
	}
}

func TestToolsConfig_GetFilterMinLength(t *testing.T) {
	tests := []struct {
		name     string
		minLen   int
		expected int
	}{
		{"zero returns default", 0, 8},
		{"negative returns default", -1, 8},
		{"positive returns value", 16, 16},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &ToolsConfig{FilterMinLength: tt.minLen}
			if got := cfg.GetFilterMinLength(); got != tt.expected {
				t.Errorf("GetFilterMinLength() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestDefaultConfig_CronAllowCommandEnabled(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Tools.Cron.AllowCommand {
		t.Fatal("DefaultConfig().Tools.Cron.AllowCommand should be true")
	}
}

func TestDefaultConfig_HooksDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.Hooks.Enabled {
		t.Fatal("DefaultConfig().Hooks.Enabled should be true")
	}
	if cfg.Hooks.Defaults.ObserverTimeoutMS != 500 {
		t.Fatalf("ObserverTimeoutMS = %d, want 500", cfg.Hooks.Defaults.ObserverTimeoutMS)
	}
	if cfg.Hooks.Defaults.InterceptorTimeoutMS != 5000 {
		t.Fatalf("InterceptorTimeoutMS = %d, want 5000", cfg.Hooks.Defaults.InterceptorTimeoutMS)
	}
	if cfg.Hooks.Defaults.ApprovalTimeoutMS != 60000 {
		t.Fatalf("ApprovalTimeoutMS = %d, want 60000", cfg.Hooks.Defaults.ApprovalTimeoutMS)
	}
}

func TestDefaultConfig_LogLevel(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Gateway.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want \"fatal\"", cfg.Gateway.LogLevel)
	}
}

func TestLoadConfig_ExecAllowRemoteDefaultsTrueWhenUnset(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"version":3,"tools":{"exec":{"enable_deny_patterns":true}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if !cfg.Tools.Exec.AllowRemote {
		t.Fatal("tools.exec.allow_remote should remain true when unset in config file")
	}
}

func TestLoadConfig_CronAllowCommandDefaultsTrueWhenUnset(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"version":3,"tools":{"cron":{"exec_timeout_minutes":5}}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if !cfg.Tools.Cron.AllowCommand {
		t.Fatal("tools.cron.allow_command should remain true when unset in config file")
	}
}

func TestLoadConfig_WebToolsProxy(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{
  "version": 3,
  "agents": {"defaults":{"workspace":"./workspace","model":"gpt4","max_tokens":8192,"max_tool_iterations":20}},
  "model_list": [{"model_name":"gpt4","model":"openai/gpt-5.4","api_key":"x"}],
  "tools": {"web":{"proxy":"http://127.0.0.1:7890"}}
}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error: %v", err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.Tools.Web.Proxy != "http://127.0.0.1:7890" {
		t.Fatalf("Tools.Web.Proxy = %q, want %q", cfg.Tools.Web.Proxy, "http://127.0.0.1:7890")
	}
}

func TestLoadConfig_HooksProcessConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	configJSON := `{
  "version": 3,
  "hooks": {
    "processes": {
      "review-gate": {
        "enabled": true,
        "transport": "stdio",
        "command": ["uvx", "picoclaw-hook-reviewer"],
        "dir": "/tmp/hooks",
        "env": {
          "HOOK_MODE": "rewrite"
        },
        "observe": ["turn_start", "turn_end"],
        "intercept": ["before_tool", "approve_tool"]
      }
    },
    "builtins": {
      "audit": {
        "enabled": true,
        "priority": 5,
        "config": {
          "label": "audit"
        }
      }
    }
  }
}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error: %v", err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	processCfg, ok := cfg.Hooks.Processes["review-gate"]
	if !ok {
		t.Fatal("expected review-gate process hook")
	}
	if !processCfg.Enabled {
		t.Fatal("expected review-gate process hook to be enabled")
	}
	if processCfg.Transport != "stdio" {
		t.Fatalf("Transport = %q, want stdio", processCfg.Transport)
	}
	if len(processCfg.Command) != 2 || processCfg.Command[0] != "uvx" {
		t.Fatalf("Command = %v", processCfg.Command)
	}
	if processCfg.Dir != "/tmp/hooks" {
		t.Fatalf("Dir = %q, want /tmp/hooks", processCfg.Dir)
	}
	if processCfg.Env["HOOK_MODE"] != "rewrite" {
		t.Fatalf("HOOK_MODE = %q, want rewrite", processCfg.Env["HOOK_MODE"])
	}
	if len(processCfg.Observe) != 2 || processCfg.Observe[1] != "turn_end" {
		t.Fatalf("Observe = %v", processCfg.Observe)
	}
	if len(processCfg.Intercept) != 2 || processCfg.Intercept[1] != "approve_tool" {
		t.Fatalf("Intercept = %v", processCfg.Intercept)
	}
	builtinCfg, ok := cfg.Hooks.Builtins["audit"]
	if !ok {
		t.Fatal("expected audit builtin hook")
	}
	if !builtinCfg.Enabled {
		t.Fatal("expected audit builtin hook to be enabled")
	}
	if builtinCfg.Priority != 5 {
		t.Fatalf("Priority = %d, want 5", builtinCfg.Priority)
	}
	if !strings.Contains(string(builtinCfg.Config), `"audit"`) {
		t.Fatalf("Config = %s", string(builtinCfg.Config))
	}
	if cfg.Hooks.Defaults.ApprovalTimeoutMS != 60000 {
		t.Fatalf("ApprovalTimeoutMS = %d, want 60000", cfg.Hooks.Defaults.ApprovalTimeoutMS)
	}
}

func TestDefaultConfig_SummarizationThresholds(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Agents.Defaults.SummarizeMessageThreshold != 20 {
		t.Errorf("SummarizeMessageThreshold = %d, want 20", cfg.Agents.Defaults.SummarizeMessageThreshold)
	}
	if cfg.Agents.Defaults.SummarizeTokenPercent != 75 {
		t.Errorf("SummarizeTokenPercent = %d, want 75", cfg.Agents.Defaults.SummarizeTokenPercent)
	}
}

func TestDefaultConfig_SessionDimensions(t *testing.T) {
	cfg := DefaultConfig()
	if len(cfg.Session.Dimensions) != 1 || cfg.Session.Dimensions[0] != "chat" {
		t.Errorf("Session.Dimensions = %v, want [chat]", cfg.Session.Dimensions)
	}
}

func TestDefaultConfig_WorkspacePath_Default(t *testing.T) {
	t.Setenv("PICOCLAW_HOME", "")
	var fakeHome string
	if runtime.GOOS == "windows" {
		fakeHome = `C:\tmp\home`
		t.Setenv("USERPROFILE", fakeHome)
	} else {
		fakeHome = "/tmp/home"
		t.Setenv("HOME", fakeHome)
	}
	cfg := DefaultConfig()
	want := filepath.Join(fakeHome, ".picoclaw", "workspace")
	if cfg.Agents.Defaults.Workspace != want {
		t.Errorf("Default workspace path = %q, want %q", cfg.Agents.Defaults.Workspace, want)
	}
}

func TestDefaultConfig_WorkspacePath_WithPicoclawHome(t *testing.T) {
	t.Setenv("PICOCLAW_HOME", "/custom/picoclaw/home")
	cfg := DefaultConfig()
	want := filepath.Join("/custom/picoclaw/home", "workspace")
	if cfg.Agents.Defaults.Workspace != want {
		t.Errorf("Workspace path with PICOCLAW_HOME = %q, want %q", cfg.Agents.Defaults.Workspace, want)
	}
}

func TestDefaultConfig_IsolationEnabled(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Isolation.Enabled {
		t.Fatal("DefaultConfig().Isolation.Enabled should be false")
	}
}

func TestConfig_UnmarshalIsolation(t *testing.T) {
	cfg := DefaultConfig()
	raw := []byte(`{
		"isolation": {
			"enabled": false,
			"expose_paths": [
				{"source":"/src","target":"/dst","mode":"ro"}
			]
		}
	}`)
	if err := json.Unmarshal(raw, cfg); err != nil {
		t.Fatalf("json.Unmarshal isolation config: %v", err)
	}
	if cfg.Isolation.Enabled {
		t.Fatal("Isolation.Enabled should be false after unmarshal")
	}
	if len(cfg.Isolation.ExposePaths) != 1 {
		t.Fatalf("ExposePaths len = %d, want 1", len(cfg.Isolation.ExposePaths))
	}
	if got := cfg.Isolation.ExposePaths[0]; got.Source != "/src" || got.Target != "/dst" || got.Mode != "ro" {
		t.Fatalf("ExposePaths[0] = %+v, want source=/src target=/dst mode=ro", got)
	}
}

func TestFlexibleStringSlice_UnmarshalText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"English commas only", "123,456,789", []string{"123", "456", "789"}},
		{"Chinese commas only", "123\uff0c456\uff0c789", []string{"123", "456", "789"}},
		{"Mixed English and Chinese commas", "123,456\uff0c789", []string{"123", "456", "789"}},
		{"Single value", "123", []string{"123"}},
		{"Values with whitespace", " 123 , 456 , 789 ", []string{"123", "456", "789"}},
		{"Empty string", "", nil},
		{"Only commas - English", ",,", []string{}},
		{"Only commas - Chinese", "\uff0c\uff0c", []string{}},
		{"Mixed commas with empty parts", "123,,456\uff0c\uff0c789", []string{"123", "456", "789"}},
		{"Complex mixed values", "user1@example.com\uff0cuser2@test.com, admin@domain.org", []string{"user1@example.com", "user2@test.com", "admin@domain.org"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f FlexibleStringSlice
			err := f.UnmarshalText([]byte(tt.input))
			if err != nil {
				t.Fatalf("UnmarshalText(%q) error = %v", tt.input, err)
			}
			if tt.expected == nil {
				if f != nil {
					t.Errorf("UnmarshalText(%q) = %v, want nil", tt.input, f)
				}
				return
			}
			if len(f) != len(tt.expected) {
				t.Errorf("UnmarshalText(%q) length = %d, want %d", tt.input, len(f), len(tt.expected))
				return
			}
			for i, v := range tt.expected {
				if f[i] != v {
					t.Errorf("UnmarshalText(%q)[%d] = %q, want %q", tt.input, i, f[i], v)
				}
			}
		})
	}
}

func TestFlexibleStringSlice_UnmarshalText_EmptySliceConsistency(t *testing.T) {
	t.Run("Empty string returns nil", func(t *testing.T) {
		var f FlexibleStringSlice
		if err := f.UnmarshalText([]byte("")); err != nil {
			t.Fatalf("UnmarshalText error = %v", err)
		}
		if f != nil {
			t.Errorf("Empty string should return nil, got %v", f)
		}
	})
	t.Run("Commas only returns empty slice", func(t *testing.T) {
		var f FlexibleStringSlice
		if err := f.UnmarshalText([]byte(",,,")); err != nil {
			t.Fatalf("UnmarshalText error = %v", err)
		}
		if f == nil {
			t.Error("Commas only should return empty slice, not nil")
		}
		if len(f) != 0 {
			t.Errorf("Expected empty slice, got %v", f)
		}
	})
}

func TestFlexibleStringSlice_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"null", `null`, nil},
		{"single string", `"Thinking..."`, []string{"Thinking..."}},
		{"single number", `123`, []string{"123"}},
		{"string array", `["Thinking...", "Still working..."]`, []string{"Thinking...", "Still working..."}},
		{"mixed array", `["123", 456]`, []string{"123", "456"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f FlexibleStringSlice
			if err := json.Unmarshal([]byte(tt.input), &f); err != nil {
				t.Fatalf("json.Unmarshal(%s) error = %v", tt.input, err)
			}
			if tt.expected == nil {
				if f != nil {
					t.Fatalf("json.Unmarshal(%s) = %#v, want nil slice", tt.input, f)
				}
				return
			}
			if len(f) != len(tt.expected) {
				t.Fatalf("json.Unmarshal(%s) len = %d, want %d", tt.input, len(f), len(tt.expected))
			}
			for i, want := range tt.expected {
				if f[i] != want {
					t.Fatalf("json.Unmarshal(%s)[%d] = %q, want %q", tt.input, i, f[i], want)
				}
			}
		})
	}
}
