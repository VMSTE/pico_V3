package config

import "testing"

// Волна 117: резолв ${oauth:notion} + регрессия github.
func TestResolveOAuthPlaceholders_Notion(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Integrations.Notion.AccessToken = *NewSecureString("ntn_secret")
	cfg.Integrations.GitHub.AccessToken = *NewSecureString("ghu_secret")

	out := cfg.ResolveOAuthPlaceholders(map[string]string{
		"Authorization": "Bearer ${oauth:notion}",
		"X-Gh":          "${oauth:github}",
	})
	if out["Authorization"] != "Bearer ntn_secret" {
		t.Fatalf("notion = %q", out["Authorization"])
	}
	if out["X-Gh"] != "ghu_secret" {
		t.Fatalf("github = %q", out["X-Gh"])
	}

	// Нет токена → плейсхолдер остаётся как есть (не фатально, волна 113/114).
	cfg2 := DefaultConfig()
	out2 := cfg2.ResolveOAuthPlaceholders(map[string]string{
		"Authorization": "Bearer ${oauth:notion}",
	})
	if out2["Authorization"] != "Bearer ${oauth:notion}" {
		t.Fatalf("unresolved = %q", out2["Authorization"])
	}
}
