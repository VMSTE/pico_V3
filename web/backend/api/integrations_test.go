package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func writeIntegrationConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	cfg := config.DefaultConfig()
	cfg.Integrations.GitHub.ClientID = "Iv1.test"
	cfg.Integrations.GitHub.ClientSecret = *config.NewSecureString("test_secret")
	if err := config.SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	return path
}

func TestGitHubConnectRequiresClientID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := config.SaveConfig(path, config.DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(path)
	mux := http.NewServeMux()
	h.registerIntegrationRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/integrations/github/connect", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestGitHubCallbackRejectsBadState(t *testing.T) {
	h := NewHandler(writeIntegrationConfig(t))
	mux := http.NewServeMux()
	h.registerIntegrationRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/integrations/github/callback?code=x&state=bogus",
		nil,
	)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestGitHubCallbackStoresTokenOutsideConfigJSON(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if r.Form.Get("client_id") != "Iv1.test" || r.Form.Get("client_secret") != "test_secret" {
			t.Errorf("bad client credentials in token exchange")
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "ghu_live_token",
			"refresh_token": "ghr_refresh",
			"expires_in":    28800,
			"token_type":    "bearer",
		})
	}))
	defer tokenSrv.Close()
	userSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"login": "gar"})
	}))
	defer userSrv.Close()

	oldToken, oldUser := githubTokenURL, githubUserURL
	githubTokenURL, githubUserURL = tokenSrv.URL, userSrv.URL
	defer func() { githubTokenURL, githubUserURL = oldToken, oldUser }()

	path := writeIntegrationConfig(t)
	h := NewHandler(path)
	mux := http.NewServeMux()
	h.registerIntegrationRoutes(mux)

	connectRec := httptest.NewRecorder()
	connectReq := httptest.NewRequest(http.MethodGet, "/api/integrations/github/connect", nil)
	connectReq.Host = "localhost:18800"
	mux.ServeHTTP(connectRec, connectReq)
	if connectRec.Code != http.StatusFound {
		t.Fatalf("connect status = %d", connectRec.Code)
	}
	loc := connectRec.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://github.com/login/oauth/authorize?") {
		t.Fatalf("redirect = %q", loc)
	}
	parts := strings.Split(loc, "state=")
	if len(parts) != 2 {
		t.Fatalf("no state in redirect %q", loc)
	}
	state := parts[1]

	cbRec := httptest.NewRecorder()
	cbReq := httptest.NewRequest(
		http.MethodGet,
		"/api/integrations/github/callback?code=abc&state="+state,
		nil,
	)
	mux.ServeHTTP(cbRec, cbReq)
	if cbRec.Code != http.StatusFound {
		t.Fatalf("callback status = %d, body=%s", cbRec.Code, cbRec.Body.String())
	}

	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Integrations.GitHub.AccessToken.String() != "ghu_live_token" {
		t.Fatal("access token not stored")
	}
	if cfg.Integrations.GitHub.Login != "gar" {
		t.Fatalf("login = %q", cfg.Integrations.GitHub.Login)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "ghu_live_token") {
		t.Fatal("config.json leaks access token")
	}
}
