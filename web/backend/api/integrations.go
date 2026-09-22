package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

// Волна 114 (ТЗ-114, D-AUDIT-132): GitHub OAuth «в пару кликов».
// Паттерн — как у логина морды (oauth.go): state в памяти с TTL,
// code меняем на токен сервер-сайд; секреты не покидают бэкенд.

const (
	githubAuthorizeURL = "https://github.com/login/oauth/authorize"
	githubCallbackPath = "/api/integrations/github/callback"
	githubStateTTL     = 10 * time.Minute
)

// URL'ы — var для тестов (httptest).
var (
	githubTokenURL = "https://github.com/login/oauth/access_token"
	githubUserURL  = "https://api.github.com/user"
)

var (
	githubStatesMu sync.Mutex
	githubStates   = map[string]time.Time{}
)

func registerGitHubState(state string) {
	githubStatesMu.Lock()
	defer githubStatesMu.Unlock()
	for s, at := range githubStates {
		if time.Since(at) > githubStateTTL {
			delete(githubStates, s)
		}
	}
	githubStates[state] = time.Now()
}

func takeGitHubState(state string) bool {
	githubStatesMu.Lock()
	defer githubStatesMu.Unlock()
	at, ok := githubStates[state]
	if !ok || time.Since(at) > githubStateTTL {
		return false
	}
	delete(githubStates, state)
	return true
}

// registerIntegrationRoutes привязывает OAuth-эндпоинты интеграций (волна 114).
func (h *Handler) registerIntegrationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/integrations/github/status", h.handleGitHubStatus)
	mux.HandleFunc("GET /api/integrations/github/connect", h.handleGitHubConnect)
	mux.HandleFunc("GET /api/integrations/github/callback", h.handleGitHubCallback)
	mux.HandleFunc("POST /api/integrations/github/disconnect", h.handleGitHubDisconnect)
}

// handleGitHubConnect: редирект на экран авторизации GitHub App.
//
//	GET /api/integrations/github/connect
func (h *Handler) handleGitHubConnect(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	clientID := cfg.Integrations.GitHub.ClientID
	if clientID == "" {
		http.Error(
			w,
			"GitHub App not configured: set integrations.github.client_id",
			http.StatusBadRequest,
		)
		return
	}
	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		http.Error(w, "Failed to generate state", http.StatusInternalServerError)
		return
	}
	state := hex.EncodeToString(stateBytes)
	registerGitHubState(state)

	callbackURL := "http://" + r.Host + githubCallbackPath
	q := url.Values{
		"client_id":    {clientID},
		"redirect_uri": {callbackURL},
		"state":        {state},
	}
	http.Redirect(w, r, githubAuthorizeURL+"?"+q.Encode(), http.StatusFound)
}

// handleGitHubCallback: обмен code → access_token, сохранение в .security.yml.
//
//	GET /api/integrations/github/callback?code=...&state=...
func (h *Handler) handleGitHubCallback(w http.ResponseWriter, r *http.Request) {
	if !takeGitHubState(r.URL.Query().Get("state")) {
		http.Error(w, "Invalid or expired OAuth state — start connect again", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Missing code from GitHub", http.StatusBadRequest)
		return
	}

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	gh := cfg.Integrations.GitHub
	if gh.ClientID == "" || gh.ClientSecret.String() == "" {
		http.Error(w, "GitHub App not configured (client_id/client_secret)", http.StatusBadRequest)
		return
	}

	token, refreshToken, expiresIn, err := exchangeGitHubCode(
		r.Context(),
		gh.ClientID,
		gh.ClientSecret.String(),
		code,
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("Token exchange failed: %v", err), http.StatusBadGateway)
		return
	}

	gh.AccessToken = *config.NewSecureString(token)
	if refreshToken != "" {
		gh.RefreshToken = *config.NewSecureString(refreshToken)
	}
	if expiresIn > 0 {
		gh.ExpiresAt = time.Now().
			Add(time.Duration(expiresIn) * time.Second).
			UTC().
			Format(time.RFC3339)
	}
	gh.Login = fetchGitHubLogin(r.Context(), token)
	cfg.Integrations.GitHub = gh

	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/?github=connected", http.StatusFound)
}

// handleGitHubStatus: подключён ли GitHub; при истёкшем токене — тихий рефреш.
//
//	GET /api/integrations/github/status
func (h *Handler) handleGitHubStatus(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	gh := cfg.Integrations.GitHub
	if gh.Connected() && githubTokenExpired(gh.ExpiresAt) && gh.RefreshToken.String() != "" {
		if rErr := refreshGitHubToken(r.Context(), &gh); rErr == nil {
			cfg.Integrations.GitHub = gh
			_ = config.SaveConfig(h.configPath, cfg)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"connected":  gh.Connected(),
		"login":      gh.Login,
		"expires_at": gh.ExpiresAt,
	})
}

// handleGitHubDisconnect: стирает локальный токен (отзыв на GitHub — руками).
//
//	POST /api/integrations/github/disconnect
func (h *Handler) handleGitHubDisconnect(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	cfg.Integrations.GitHub.AccessToken = config.SecureString{}
	cfg.Integrations.GitHub.RefreshToken = config.SecureString{}
	cfg.Integrations.GitHub.ExpiresAt = ""
	cfg.Integrations.GitHub.Login = ""
	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func exchangeGitHubCode(
	ctx context.Context,
	clientID, clientSecret, code string,
) (string, string, int, error) {
	return postGitHubTokenForm(ctx, url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
	})
}

func refreshGitHubToken(ctx context.Context, gh *config.GitHubIntegrationConfig) error {
	token, refreshToken, expiresIn, err := postGitHubTokenForm(ctx, url.Values{
		"client_id":     {gh.ClientID},
		"client_secret": {gh.ClientSecret.String()},
		"grant_type":    {"refresh_token"},
		"refresh_token": {gh.RefreshToken.String()},
	})
	if err != nil {
		return err
	}
	gh.AccessToken = *config.NewSecureString(token)
	if refreshToken != "" {
		gh.RefreshToken = *config.NewSecureString(refreshToken)
	}
	if expiresIn > 0 {
		gh.ExpiresAt = time.Now().
			Add(time.Duration(expiresIn) * time.Second).
			UTC().
			Format(time.RFC3339)
	}
	return nil
}

func postGitHubTokenForm(ctx context.Context, form url.Values) (string, string, int, error) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		githubTokenURL,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", "", 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", 0, err
	}
	var out struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", "", 0, fmt.Errorf("bad token response: %w", err)
	}
	if out.Error != "" {
		return "", "", 0, fmt.Errorf("github: %s (%s)", out.Error, out.ErrorDesc)
	}
	if out.AccessToken == "" {
		return "", "", 0, fmt.Errorf("github: empty access_token (http %d)", resp.StatusCode)
	}
	return out.AccessToken, out.RefreshToken, out.ExpiresIn, nil
}

func fetchGitHubLogin(ctx context.Context, token string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubUserURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var out struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ""
	}
	return out.Login
}

func githubTokenExpired(expiresAt string) bool {
	if expiresAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return false
	}
	return time.Until(t) < 5*time.Minute
}
