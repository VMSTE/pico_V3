package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"

	"github.com/sipeed/picoclaw/pkg/credential"
)

func TestLoadConfig_TelegramPlaceholderTextAcceptsSingleString(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	data := `{
		"version": 3,
		"agents": { "defaults": { "workspace": "", "model": "", "max_tokens": 0, "max_tool_iterations": 0 } },
		"session": {},
		"channel_list": {
			"telegram": {
				"type": "telegram",
				"enabled": true,
				"placeholder": {
					"enabled": true,
					"text": "Thinking..."
				},
				"settings": {
					"bot_token": "",
					"allow_from": []
				}
			}
		},
		"model_list": [],
		"gateway": {},
		"tools": {},
		"heartbeat": {},
		"devices": {},
		"voice": {}
	}`
	if err := os.WriteFile(configPath, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	bc := cfg.Channels.Get("telegram")
	if bc == nil {
		t.Fatal("telegram channel config is nil")
	}
	if bc.Placeholder.Text == nil {
		t.Fatal("placeholder text should not be nil")
	}
	if len(bc.Placeholder.Text) != 1 || bc.Placeholder.Text[0] != "Thinking..." {
		t.Fatalf("Placeholder.Text = %v, want [\"Thinking...\"]", bc.Placeholder.Text)
	}
}

func TestLoadConfig_WarnsForPlaintextAPIKey(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	const original = `{"version":3,"model_list":[{"model_name":"test","model":"openai/gpt-4","api_key":"sk-1234567890abcdef"}]}`
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.ModelList[0].APIKey() != "sk-1234567890abcdef" {
		t.Fatalf("APIKey() = %q, want sk-1234567890abcdef", cfg.ModelList[0].APIKey())
	}
	raw, _ := os.ReadFile(configPath)
	if string(raw) != original {
		t.Errorf("config file was rewritten on load; want it unchanged\n  got:  %s\n  want: %s", string(raw), original)
	}
}

func TestSaveConfig_EncryptsPlaintextAPIKey(t *testing.T) {
	mustSetupSSHKey(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cfg := DefaultConfig()
	cfg.ModelList = []*ModelConfig{
		{ModelName: "test", Model: "openai/gpt-4", APIKeys: SimpleSecureStrings("sk-1234567890abcdef")},
	}
	if err := SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error: %v", err)
	}
	raw, _ := os.ReadFile(configPath)
	if strings.Contains(string(raw), "sk-1234567890abcdef") {
		t.Fatal("plaintext API key found in saved file")
	}
	reloaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if reloaded.ModelList[0].APIKey() != "sk-1234567890abcdef" {
		t.Fatalf("decrypted APIKey() = %q", reloaded.ModelList[0].APIKey())
	}
}

func TestLoadConfig_NoSealWithoutPassphrase(t *testing.T) {
	mustSetupSSHKey(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"version":3,"model_list":[{"model_name":"test","model":"openai/gpt-4","api_key":"sk-plaintext"}]}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	_, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	raw, _ := os.ReadFile(configPath)
	if strings.Contains(string(raw), "SEALED{") {
		t.Fatal("found SEALED in config file when passphrase was not set \u2014 api_key must not be sealed without passphrase")
	}
}

func TestLoadConfig_FileRefNotSealed(t *testing.T) {
	mustSetupSSHKey(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := `{"version":3,"model_list":[{"model_name":"test","model":"openai/gpt-4","api_key":"file://api.key"}]}`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.ModelList[0].APIKey() != "file://api.key" {
		t.Fatalf("APIKey() = %q, want file://api.key", cfg.ModelList[0].APIKey())
	}
}

func TestSaveConfig_MixedKeys(t *testing.T) {
	mustSetupSSHKey(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cfg := DefaultConfig()
	cfg.ModelList = []*ModelConfig{
		{ModelName: "enc", Model: "openai/gpt-4", APIKeys: SimpleSecureStrings("sk-secret")},
		{ModelName: "file", Model: "openai/gpt-4", APIKeys: SimpleSecureStrings("file://api.key")},
	}
	if err := SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error: %v", err)
	}
	raw, _ := os.ReadFile(configPath)
	if strings.Contains(string(raw), "sk-secret") {
		t.Fatal("plaintext API key found in saved file")
	}
	if !strings.Contains(string(raw), "file://api.key") {
		t.Fatal("file:// reference should remain as-is")
	}
	reloaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if reloaded.ModelList[0].APIKey() != "sk-secret" {
		t.Fatalf("decrypted APIKey() = %q", reloaded.ModelList[0].APIKey())
	}
	if reloaded.ModelList[1].APIKey() != "file://api.key" {
		t.Fatalf("file APIKey() = %q", reloaded.ModelList[1].APIKey())
	}
	secPath := filepath.Join(dir, SecurityConfigFile)
	secData, err := os.ReadFile(secPath)
	if err != nil {
		t.Fatalf("ReadFile(security) error: %v", err)
	}
	var secYAML map[string]any
	if err := yaml.Unmarshal(secData, &secYAML); err != nil {
		t.Fatalf("YAML unmarshal security file: %v", err)
	}
	ml, _ := secYAML["model_list"].(map[string]any)
	encEntry, _ := ml["enc"].(map[string]any)
	apiKeysRaw, _ := encEntry["api_keys"].([]any)
	if len(apiKeysRaw) != 1 {
		t.Fatalf("expected 1 api_key in security YAML, got %d", len(apiKeysRaw))
	}
	encValue, _ := apiKeysRaw[0].(string)
	if !strings.HasPrefix(encValue, "ENC{") {
		t.Fatalf("expected ENC{...} in security YAML, got %q", encValue)
	}
}

func TestLoadConfig_MixedKeys_NoPassphrase(t *testing.T) {
	mustSetupSSHKey(t)
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	t.Setenv("PICOCLAW_PASSPHRASE", "test-pass-phrase")
	cfg := DefaultConfig()
	cfg.ModelList = []*ModelConfig{
		{ModelName: "enc", Model: "openai/gpt-4", APIKeys: SimpleSecureStrings("sk-secret")},
	}
	if err := SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error: %v", err)
	}
	secPath := filepath.Join(dir, SecurityConfigFile)
	secData, _ := os.ReadFile(secPath)
	var secYAML map[string]any
	yaml.Unmarshal(secData, &secYAML)
	ml := secYAML["model_list"].(map[string]any)
	encEntry := ml["enc"].(map[string]any)
	apiKeysRaw := encEntry["api_keys"].([]any)
	encValue := apiKeysRaw[0].(string)
	assert.NotEmpty(t, encValue)
	mixed, _ := json.Marshal(map[string]any{
		"version":    CurrentVersion,
		"model_list": []map[string]any{
			{"model_name": "enc", "model": "openai/gpt-4", "api_key": encValue},
			{"model_name": "plain", "model": "openai/gpt-4", "api_key": "sk-plain"},
			{"model_name": "file", "model": "openai/gpt-4", "api_key": "file://api.key"},
		},
	})
	os.WriteFile(configPath, mixed, 0o600)
	os.Remove(secPath)
	t.Setenv("PICOCLAW_PASSPHRASE", "")
	loaded, err := LoadConfig(configPath)
	assert.NoError(t, err)
	assert.Equal(t, "sk-secret", loaded.ModelList[0].APIKey())
	assert.Equal(t, "sk-plain", loaded.ModelList[1].APIKey())
	assert.Equal(t, "file://api.key", loaded.ModelList[2].APIKey())
	raw, _ := os.ReadFile(configPath)
	if strings.Contains(string(raw), "SEALED{") {
		t.Fatal("found SEALED in config file when passphrase was not set")
	}
}

func TestSaveConfig_UsesPassphraseProvider(t *testing.T) {
	mustSetupSSHKey(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := DefaultConfig()
	cfg.ModelList = []*ModelConfig{
		{ModelName: "test", Model: "openai/gpt-4", APIKeys: SimpleSecureStrings("sk-1234567890abcdef")},
	}
	cfg.PassphraseProvider = credential.PassphraseProvider{Passphrase: "test-passphrase"}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig() error: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "sk-1234567890abcdef") {
		t.Fatal("plaintext API key found in saved file")
	}
	if !strings.Contains(string(raw), "SEALED{") {
		t.Fatal("expected SEALED{...} in saved file when passphrase provider is set")
	}
}

func TestLoadConfig_UsesPassphraseProvider(t *testing.T) {
	mustSetupSSHKey(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	passphrase := "test-passphrase"
	encrypted, err := credential.Encrypt("sk-1234567890abcdef", passphrase)
	if err != nil {
		t.Fatalf("Encrypt() error: %v", err)
	}
	raw, _ := json.Marshal(map[string]any{
		"version":    CurrentVersion,
		"model_list": []map[string]any{
			{"model_name": "test", "model": "openai/gpt-4", "api_key": encrypted},
		},
	})
	os.WriteFile(path, raw, 0o600)
	t.Setenv("PICOCLAW_PASSPHRASE", passphrase)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.ModelList[0].APIKey() != "sk-1234567890abcdef" {
		t.Fatalf("APIKey() = %q, want sk-1234567890abcdef", cfg.ModelList[0].APIKey())
	}
}

func TestConfigParsesLogLevel(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := `{"version":3,"gateway":{"log_level":"debug"}}`
	os.WriteFile(configPath, []byte(raw), 0o600)
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.Gateway.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want \"debug\"", cfg.Gateway.LogLevel)
	}
}

func TestConfigLogLevelEmpty(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := `{"version":3}`
	os.WriteFile(configPath, []byte(raw), 0o600)
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	if cfg.Gateway.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want \"warn\"", cfg.Gateway.LogLevel)
	}
}

func TestResolveGatewayLogLevel(t *testing.T) {
	tests := []struct {
		name     string
		cfgLevel string
		expected string
	}{
		{"debug stays debug", "debug", "debug"},
		{"info stays info", "info", "info"},
		{"warn stays warn", "warn", "warn"},
		{"error stays error", "error", "error"},
		{"fatal stays fatal", "fatal", "fatal"},
		{"empty becomes warn", "", "warn"},
		{"unknown becomes warn", "foobar", "warn"},
		{"case insensitive", "DEBUG", "debug"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config.json")
			raw := `{"version":3,"gateway":{"log_level":"` + tt.cfgLevel + `"}}`
			os.WriteFile(configPath, []byte(raw), 0o600)
			cfg, err := LoadConfig(configPath)
			if err != nil {
				t.Fatalf("LoadConfig() error: %v", err)
			}
			if cfg.Gateway.LogLevel != tt.expected {
				t.Errorf("LogLevel = %q, want %q", cfg.Gateway.LogLevel, tt.expected)
			}
		})
	}
}

func TestResolveGatewayLogLevel_UsesEnvOverrideAndNormalizesInvalid(t *testing.T) {
	tests := []struct {
		name     string
		envLevel string
		expected string
	}{
		{"env debug", "debug", "debug"},
		{"env info", "info", "info"},
		{"env warn", "warn", "warn"},
		{"env error", "error", "error"},
		{"env fatal", "fatal", "fatal"},
		{"env case insensitive", "DEBUG", "debug"},
		{"env invalid becomes warn", "foobar", "warn"},
		{"env empty falls back to config", "", "info"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config.json")
			raw := `{"version":3,"gateway":{"log_level":"info"}}`
			os.WriteFile(configPath, []byte(raw), 0o600)
			t.Setenv("PICOCLAW_LOG_LEVEL", tt.envLevel)
			cfg, err := LoadConfig(configPath)
			if err != nil {
				t.Fatalf("LoadConfig() error: %v", err)
			}
			if cfg.Gateway.LogLevel != tt.expected {
				t.Errorf("LogLevel = %q, want %q", cfg.Gateway.LogLevel, tt.expected)
			}
		})
	}
}

func TestLoadConfig_AppliesLegacyClawHubRegistryEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := `{"version":3,"tools":{"skills":{"registries":{"clawhub":{"enabled":true}}}}}`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	t.Setenv("PICOCLAW_CLAWHUB_BASE_URL", "https://custom-clawhub.example.com")
	t.Setenv("PICOCLAW_CLAWHUB_AUTH_TOKEN", "custom-auth-token")
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	clawhub, ok := cfg.Tools.Skills.Registries.Get("clawhub")
	if !ok {
		t.Fatal("clawhub registry not found in config")
	}
	if clawhub.BaseURL != "https://custom-clawhub.example.com" {
		t.Errorf("clawhub BaseURL = %q, want %q", clawhub.BaseURL, "https://custom-clawhub.example.com")
	}
	if clawhub.AuthToken.String() != "custom-auth-token" {
		t.Errorf("clawhub AuthToken = %q, want %q", clawhub.AuthToken.String(), "custom-auth-token")
	}
}

func TestLoadConfig_AppliesGitHubRegistryEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	raw := `{"version":3,"tools":{"skills":{"registries":{"github":{"enabled":true}}}}}`
	if err := os.WriteFile(configPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	t.Setenv("PICOCLAW_GITHUB_BASE_URL", "https://custom-github.example.com")
	t.Setenv("PICOCLAW_GITHUB_AUTH_TOKEN", "custom-github-token")
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error: %v", err)
	}
	github, ok := cfg.Tools.Skills.Registries.Get("github")
	if !ok {
		t.Fatal("github registry not found in config")
	}
	if github.BaseURL != "https://custom-github.example.com" {
		t.Errorf("github BaseURL = %q, want %q", github.BaseURL, "https://custom-github.example.com")
	}
	if github.AuthToken.String() != "custom-github-token" {
		t.Errorf("github AuthToken = %q, want %q", github.AuthToken.String(), "custom-github-token")
	}
}

func TestModelConfig_ExtraBodyRoundTrip(t *testing.T) {
	raw := `{"model_name":"test","model":"openai/gpt-4","extra_body":{"reasoning":{"effort":"high"},"temperature":0.5}}`
	var m ModelConfig
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if m.ExtraBody == nil {
		t.Fatal("ExtraBody should not be nil")
	}
	out, err := json.Marshal(&m)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	if !strings.Contains(string(out), `"reasoning"`) || !strings.Contains(string(out), `"effort"`) {
		t.Fatalf("round-trip lost extra_body: %s", string(out))
	}
}

func TestModelConfig_CustomHeadersRoundTrip(t *testing.T) {
	raw := `{"model_name":"test","model":"openai/gpt-4","custom_headers":{"X-Api-Version":"2024-12-01","Anthropic-Beta":"output-128k"}}`
	var m ModelConfig
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if m.CustomHeaders == nil {
		t.Fatal("CustomHeaders should not be nil")
	}
	if m.CustomHeaders["X-Api-Version"] != "2024-12-01" {
		t.Errorf("X-Api-Version = %q, want %q", m.CustomHeaders["X-Api-Version"], "2024-12-01")
	}
	if m.CustomHeaders["Anthropic-Beta"] != "output-128k" {
		t.Errorf("Anthropic-Beta = %q, want %q", m.CustomHeaders["Anthropic-Beta"], "output-128k")
	}
	out, err := json.Marshal(&m)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	if !strings.Contains(string(out), `"custom_headers"`) {
		t.Fatalf("round-trip lost custom_headers: %s", string(out))
	}
}

func TestDefaultConfig_MinimaxExtraBody(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ModelList = []*ModelConfig{
		{ModelName: "minimax", Model: "minimax/MiniMax-M1", ExtraBody: map[string]any{"stream_options": nil}},
	}
	out, err := json.Marshal(cfg.ModelList[0])
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	if !strings.Contains(string(out), `"extra_body"`) {
		t.Fatalf("ExtraBody should be marshaled even with nil value: %s", string(out))
	}
}

func TestFilterSensitiveData(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ModelList = []*ModelConfig{
		{ModelName: "test", Model: "openai/gpt-4", APIKeys: SimpleSecureStrings("sk-supersecretkey123")},
	}
	cfg.Tools.Web.Brave.APIKeys = SimpleSecureStrings("BSAqwe123456789")
	cfg.Tools.FilterSensitiveData = true
	cfg.Tools.FilterMinLength = 8
	result := "Hello, the API key is sk-supersecretkey123 and Brave key is BSAqwe123456789."
	filtered := cfg.FilterSensitiveData(result)
	assert.NotContains(t, filtered, "sk-supersecretkey123")
	assert.NotContains(t, filtered, "BSAqwe123456789")
}

func TestFilterSensitiveData_MultipleKeys(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ModelList = []*ModelConfig{
		{ModelName: "m1", Model: "openai/gpt-4", APIKeys: SimpleSecureStrings("sk-key-one-12345678")},
		{ModelName: "m2", Model: "openai/gpt-4", APIKeys: SimpleSecureStrings("sk-key-two-87654321")},
	}
	cfg.Tools.FilterSensitiveData = true
	cfg.Tools.FilterMinLength = 8
	result := "Keys: sk-key-one-12345678 and sk-key-two-87654321 used."
	filtered := cfg.FilterSensitiveData(result)
	assert.NotContains(t, filtered, "sk-key-one-12345678")
	assert.NotContains(t, filtered, "sk-key-two-87654321")
}

func TestFilterSensitiveData_AllTokenTypes(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ModelList = []*ModelConfig{
		{ModelName: "test", Model: "openai/gpt-4", APIKeys: SimpleSecureStrings("sk-model-api-key-12345678")},
	}
	cfg.Tools.Web.Brave.APIKeys = SimpleSecureStrings("BSA-brave-key-12345678")
	cfg.Tools.Web.Tavily.APIKeys = SimpleSecureStrings("tvly-tavily-key-12345678")
	cfg.Tools.Web.Perplexity.APIKeys = SimpleSecureStrings("pplx-perplexity-key-12345678")
	cfg.Channels = testChannelsConfigWithTokens()
	cfg.Tools.FilterSensitiveData = true
	cfg.Tools.FilterMinLength = 8
	result := "sk-model-api-key-12345678 BSA-brave-key-12345678 tvly-tavily-key-12345678 pplx-perplexity-key-12345678 telegram-bot-token-12345678 discord-bot-token-12345678 slack-bot-token-12345678"
	filtered := cfg.FilterSensitiveData(result)
	assert.NotContains(t, filtered, "sk-model-api-key-12345678")
	assert.NotContains(t, filtered, "BSA-brave-key-12345678")
	assert.NotContains(t, filtered, "tvly-tavily-key-12345678")
	assert.NotContains(t, filtered, "pplx-perplexity-key-12345678")
	assert.NotContains(t, filtered, "telegram-bot-token-12345678")
	assert.NotContains(t, filtered, "discord-bot-token-12345678")
	assert.NotContains(t, filtered, "slack-bot-token-12345678")
}

func TestMakeBackup_WithDateSuffix(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	os.WriteFile(configPath, []byte(`{"version":3}`), 0o600)
	err := makeBackup(configPath)
	if err != nil {
		t.Fatalf("makeBackup() error: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "config.json.bak.*"))
	if len(matches) != 1 {
		t.Fatalf("expected 1 backup file, got %d", len(matches))
	}
	data, _ := os.ReadFile(matches[0])
	if string(data) != `{"version":3}` {
		t.Errorf("backup content = %q, want original content", string(data))
	}
}

func TestMakeBackup_AlsoBacksSecurityFile(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	os.WriteFile(configPath, []byte(`{"version":3}`), 0o600)
	secPath := filepath.Join(dir, SecurityConfigFile)
	os.WriteFile(secPath, []byte(`model_list:\n  test:\n    api_key: "x"`), 0o600)
	if err := makeBackup(configPath); err != nil {
		t.Fatalf("makeBackup() error: %v", err)
	}
	configBak, _ := filepath.Glob(filepath.Join(dir, "config.json.bak.*"))
	if len(configBak) != 1 {
		t.Fatalf("expected 1 config backup, got %d", len(configBak))
	}
	secBak, _ := filepath.Glob(filepath.Join(dir, SecurityConfigFile+".bak.*"))
	if len(secBak) != 1 {
		t.Fatalf("expected 1 security backup, got %d", len(secBak))
	}
}

func TestMakeBackup_OnlyConfigNoSecurity(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	os.WriteFile(configPath, []byte(`{"version":3}`), 0o600)
	if err := makeBackup(configPath); err != nil {
		t.Fatalf("makeBackup() error: %v", err)
	}
	configBak, _ := filepath.Glob(filepath.Join(dir, "config.json.bak.*"))
	if len(configBak) != 1 {
		t.Fatalf("expected 1 config backup, got %d", len(configBak))
	}
	secBak, _ := filepath.Glob(filepath.Join(dir, SecurityConfigFile+".bak.*"))
	if len(secBak) != 0 {
		t.Fatalf("expected 0 security backups, got %d", len(secBak))
	}
}

func TestMakeBackup_SameDateSuffix(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	os.WriteFile(configPath, []byte(`{"version":3}`), 0o600)
	secPath := filepath.Join(dir, SecurityConfigFile)
	os.WriteFile(secPath, []byte(`model_list: {}`), 0o600)
	if err := makeBackup(configPath); err != nil {
		t.Fatalf("makeBackup() error: %v", err)
	}
	configBak, _ := filepath.Glob(filepath.Join(dir, "config.json.bak.*"))
	secBak, _ := filepath.Glob(filepath.Join(dir, SecurityConfigFile+".bak.*"))
	if len(configBak) != 1 || len(secBak) != 1 {
		t.Fatalf("expected exactly 1 config and 1 security backup, got %d and %d", len(configBak), len(secBak))
	}
	confSuffix := filepath.Ext(configBak[0])
	secSuffix := filepath.Ext(secBak[0])
	if confSuffix != secSuffix {
		t.Errorf("backup suffixes differ: config=%s, security=%s", confSuffix, secSuffix)
	}
}

func testChannelsConfigWithTokens() ChannelsConfig {
	return ChannelsConfig{
		"telegram": &Channel{
			Enabled: true,
			Type:    ChannelTelegram,
			Settings: mustMarshal(TelegramSettings{
				Token: SimpleSecureString("telegram-bot-token-12345678"),
			}),
		},
		"discord": &Channel{
			Enabled: true,
			Type:    ChannelDiscord,
			Settings: mustMarshal(DiscordSettings{
				Token: SimpleSecureString("discord-bot-token-12345678"),
			}),
		},
		"slack": &Channel{
			Enabled: true,
			Type:    ChannelSlack,
			Settings: mustMarshal(SlackSettings{
				BotToken: SimpleSecureString("slack-bot-token-12345678"),
			}),
		},
	}
}
