package api

// Волна 121 (срез А): фоновый рефреш OAuth-токенов интеграций.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestShouldRefreshExpiresAt(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		exp  string
		want bool
	}{
		{"empty", "", false},
		{"garbage", "not-a-time", false},
		{"far future", now.Add(time.Hour).Format(time.RFC3339), false},
		{"within buffer", now.Add(4 * time.Minute).Format(time.RFC3339), true},
		{"just past", now.Add(-time.Minute).Format(time.RFC3339), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRefreshExpiresAt(tc.exp, now); got != tc.want {
				t.Errorf("shouldRefreshExpiresAt(%q) = %v, want %v", tc.exp, got, tc.want)
			}
		})
	}
}

func writeIntegrationsConfig(t *testing.T, cfg *config.Config) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	return path
}

func TestRefreshIntegrationsOnce_GitHubRefreshed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if got := r.PostForm.Get("grant_type"); got != "refresh_token" {
			t.Errorf("grant_type = %q, want refresh_token", got)
		}
		if got := r.PostForm.Get("refresh_token"); got != "gh-old-refresh" {
			t.Errorf("refresh_token = %q, want gh-old-refresh", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "gh-new",
			"refresh_token": "gh-new-refresh",
			"expires_in":    3600,
		})
	}))
	defer srv.Close()
	old := githubTokenURL
	githubTokenURL = srv.URL
	defer func() { githubTokenURL = old }()

	cfg := &config.Config{}
	gh := cfg.Integrations.GitHub
	gh.ClientID = "cid"
	gh.AccessToken = *config.NewSecureString("gh-old")
	gh.RefreshToken = *config.NewSecureString("gh-old-refresh")
	gh.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	cfg.Integrations.GitHub = gh
	path := writeIntegrationsConfig(t, cfg)

	refreshIntegrationsOnce(path, time.Now())

	cfg2, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	got := cfg2.Integrations.GitHub
	if got.AccessToken.String() != "gh-new" {
		t.Errorf("AccessToken = %q, want gh-new", got.AccessToken.String())
	}
	if got.RefreshToken.String() != "gh-new-refresh" {
		t.Errorf("RefreshToken not rotated: %q", got.RefreshToken.String())
	}
	if got.AuthError != "" {
		t.Errorf("AuthError = %q, want empty", got.AuthError)
	}
	exp, err := time.Parse(time.RFC3339, got.ExpiresAt)
	if err != nil || time.Until(exp) < 50*time.Minute {
		t.Errorf("ExpiresAt not recomputed: %q (%v)", got.ExpiresAt, err)
	}
}

func TestRefreshIntegrationsOnce_InvalidGrantStopsProvider(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error":             "invalid_grant",
			"error_description": "refresh token expired",
		})
	}))
	defer srv.Close()
	old := githubTokenURL
	githubTokenURL = srv.URL
	defer func() { githubTokenURL = old }()

	cfg := &config.Config{}
	gh := cfg.Integrations.GitHub
	gh.ClientID = "cid"
	gh.ClientSecret = *config.NewSecureString("secret")
	gh.AccessToken = *config.NewSecureString("gh-old")
	gh.RefreshToken = *config.NewSecureString("gh-dead-refresh")
	gh.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	cfg.Integrations.GitHub = gh
	path := writeIntegrationsConfig(t, cfg)

	refreshIntegrationsOnce(path, time.Now())

	cfg2, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg2.Integrations.GitHub.AuthError != authErrorReconnectRequired {
		t.Fatalf("AuthError = %q, want %q",
			cfg2.Integrations.GitHub.AuthError, authErrorReconnectRequired)
	}

	// Второй тик НЕ дёргает сервер: провайдер остановлен до переподключения.
	refreshIntegrationsOnce(path, time.Now())
	if calls.Load() != 1 {
		t.Errorf("token endpoint calls = %d, want 1 (stopped after invalid_grant)", calls.Load())
	}
}

func TestRefreshIntegrationsOnce_NotionRefreshed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.PostForm.Get("client_secret") != "" {
			t.Error("notion is a public client: client_secret must not be sent")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "ntn-new",
			"expires_in":   3600,
		})
	}))
	defer srv.Close()
	old := notionTokenURL
	notionTokenURL = srv.URL
	defer func() { notionTokenURL = old }()

	cfg := &config.Config{}
	n := cfg.Integrations.Notion
	n.ClientID = "notion-cid"
	n.AccessToken = *config.NewSecureString("ntn-old")
	n.RefreshToken = *config.NewSecureString("ntn-old-refresh")
	n.ExpiresAt = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	cfg.Integrations.Notion = n
	path := writeIntegrationsConfig(t, cfg)

	refreshIntegrationsOnce(path, time.Now())

	cfg2, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	got := cfg2.Integrations.Notion
	if got.AccessToken.String() != "ntn-new" {
		t.Errorf("AccessToken = %q, want ntn-new", got.AccessToken.String())
	}
	// Ответ без refresh_token → старый сохраняется (контракт ротации).
	if got.RefreshToken.String() != "ntn-old-refresh" {
		t.Errorf("RefreshToken = %q, want preserved ntn-old-refresh", got.RefreshToken.String())
	}
	if got.ExpiresAt == "" {
		t.Error("ExpiresAt must be set from expires_in")
	}
}

func TestExchangeNotionCode_ParsesExpiresIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "ntn-x",
			"refresh_token": "ntn-r",
			"expires_in":    7200,
		})
	}))
	defer srv.Close()
	old := notionTokenURL
	notionTokenURL = srv.URL
	defer func() { notionTokenURL = old }()

	access, refresh, workspace, expiresIn, err := exchangeNotionCode(
		context.Background(), "cid", "code", "verifier", "http://localhost/cb",
	)
	if err != nil {
		t.Fatalf("exchangeNotionCode: %v", err)
	}
	if access != "ntn-x" || refresh != "ntn-r" || workspace != "" {
		t.Errorf("got (%q, %q, %q), want (ntn-x, ntn-r, empty)", access, refresh, workspace)
	}
	if expiresIn != 7200 {
		t.Errorf("expiresIn = %d, want 7200", expiresIn)
	}
}
