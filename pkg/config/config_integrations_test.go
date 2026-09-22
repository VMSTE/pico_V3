package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveOAuthPlaceholders(t *testing.T) {
	cfg := &Config{}
	cfg.Integrations.GitHub.AccessToken = *NewSecureString("ghu_test_token")

	in := map[string]string{
		"Authorization": "Bearer ${oauth:github}",
		"X-Other":       "plain",
	}
	out := cfg.ResolveOAuthPlaceholders(in)
	if out["Authorization"] != "Bearer ghu_test_token" {
		t.Fatalf("resolved = %q", out["Authorization"])
	}
	if out["X-Other"] != "plain" {
		t.Fatalf("plain header mangled: %q", out["X-Other"])
	}
	if in["Authorization"] != "Bearer ${oauth:github}" {
		t.Fatal("input map mutated")
	}
}

func TestResolveOAuthPlaceholdersNoTokenKeepsPlaceholder(t *testing.T) {
	cfg := &Config{}
	out := cfg.ResolveOAuthPlaceholders(map[string]string{
		"Authorization": "Bearer ${oauth:github}",
	})
	if out["Authorization"] != "Bearer ${oauth:github}" {
		t.Fatalf("placeholder resolved without token: %q", out["Authorization"])
	}
}

func TestIntegrationsSecretsStayOutOfConfigJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := DefaultConfig()
	cfg.Integrations.GitHub.ClientID = "Iv1.test"
	cfg.Integrations.GitHub.ClientSecret = *NewSecureString("secret_value_xyz")
	cfg.Integrations.GitHub.AccessToken = *NewSecureString("ghu_secret_token")
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"ghu_secret_token", "secret_value_xyz"} {
		if strings.Contains(string(raw), s) {
			t.Fatalf("config.json leaks %s", s)
		}
	}
	if !strings.Contains(string(raw), "Iv1.test") {
		t.Fatal("client_id missing from config.json")
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if loaded.Integrations.GitHub.AccessToken.String() != "ghu_secret_token" {
		t.Fatal("access token not restored from security.yml")
	}
	if loaded.Integrations.GitHub.ClientSecret.String() != "secret_value_xyz" {
		t.Fatal("client secret not restored from security.yml")
	}
	if loaded.Integrations.GitHub.ClientID != "Iv1.test" {
		t.Fatal("client id lost")
	}
}
