package config

import "strings"

// Волна 114 (ТЗ-114, D-AUDIT-132): OAuth-интеграции.
// client_id публичен и живёт в config.json; секреты (client_secret, токены) —
// SecureString: реальные значения только в .security.yml, в config.json и
// GET /api/config не попадают (паттерн api_keys).

type IntegrationsConfig struct {
	GitHub GitHubIntegrationConfig `json:"github,omitempty" yaml:"github"`
}

type GitHubIntegrationConfig struct {
	ClientID     string       `json:"client_id,omitempty"    yaml:"client_id,omitempty"`
	ClientSecret SecureString `json:"client_secret,omitzero" yaml:"client_secret,omitempty"`
	AccessToken  SecureString `json:"access_token,omitzero"  yaml:"access_token,omitempty"`
	RefreshToken SecureString `json:"refresh_token,omitzero" yaml:"refresh_token,omitempty"`
	ExpiresAt    string       `json:"-"                      yaml:"expires_at,omitempty"`
	Login        string       `json:"-"                      yaml:"login,omitempty"`
}

func (g *GitHubIntegrationConfig) Connected() bool {
	return g.AccessToken.String() != ""
}

const (
	oauthPlaceholderPrefix = "${oauth:"
	oauthPlaceholderSuffix = "}"
)

// ResolveOAuthPlaceholders подставляет OAuth-токены интеграций вместо
// плейсхолдеров ${oauth:github} в заголовках MCP-серверов (волна 114).
// Входную мапу не мутирует. Неразрешённый плейсхолдер остаётся как есть:
// коннект упадёт с 401/403 — после волны 113 это не фатально для агента.
func (c *Config) ResolveOAuthPlaceholders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return headers
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		out[k] = c.resolveOAuthValue(v)
	}
	return out
}

func (c *Config) resolveOAuthValue(v string) string {
	i := strings.Index(v, oauthPlaceholderPrefix)
	if i < 0 {
		return v
	}
	rest := v[i+len(oauthPlaceholderPrefix):]
	j := strings.Index(rest, oauthPlaceholderSuffix)
	if j < 0 {
		return v
	}
	name := rest[:j]
	token := ""
	if name == "github" {
		token = c.Integrations.GitHub.AccessToken.String()
	}
	if token == "" {
		return v
	}
	return v[:i] + token + rest[j+len(oauthPlaceholderSuffix):]
}
