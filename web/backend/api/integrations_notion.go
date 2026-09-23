package api

// Волна 117 (ТЗ-117): Notion MCP «в пару кликов» — hosted mcp.notion.com.
// OAuth public client: PKCE S256 + dynamic client registration (RFC 7591),
// client_secret не существует вовсе. Паттерн — integrations.go (волна 114).

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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

const (
	notionCallbackPath = "/api/integrations/notion/callback"
	notionMCPURL       = "https://mcp.notion.com/mcp"
	notionStateTTL     = 10 * time.Minute
)

// URL'ы — var для тестов (httptest).
var (
	notionAuthorizeURL = "https://mcp.notion.com/authorize"
	notionTokenURL     = "https://mcp.notion.com/token"
	notionRegisterURL  = "https://mcp.notion.com/register"
)

type notionStateEntry struct {
	at       time.Time
	verifier string
}

var (
	notionStatesMu sync.Mutex
	notionStates   = map[string]notionStateEntry{}
)

func registerNotionState(state, verifier string) {
	notionStatesMu.Lock()
	defer notionStatesMu.Unlock()
	for s, e := range notionStates {
		if time.Since(e.at) > notionStateTTL {
			delete(notionStates, s)
		}
	}
	notionStates[state] = notionStateEntry{at: time.Now(), verifier: verifier}
}

func takeNotionState(state string) (string, bool) {
	notionStatesMu.Lock()
	defer notionStatesMu.Unlock()
	e, ok := notionStates[state]
	if !ok || time.Since(e.at) > notionStateTTL {
		return "", false
	}
	delete(notionStates, state)
	return e.verifier, true
}

func notionPKCE() (state, verifier, challenge string, err error) {
	stateBytes := make([]byte, 16)
	if _, err = rand.Read(stateBytes); err != nil {
		return "", "", "", err
	}
	verifierBytes := make([]byte, 32)
	if _, err = rand.Read(verifierBytes); err != nil {
		return "", "", "", err
	}
	state = hex.EncodeToString(stateBytes)
	verifier = hex.EncodeToString(verifierBytes)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return state, verifier, challenge, nil
}

// registerNotionClient: dynamic client registration — свой OAuth-app у Notion
// не нужен, клиент регистрируется на лету (первый connect).
func registerNotionClient(ctx context.Context, redirectURI string) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"client_name":                "AtoMinD",
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, notionRegisterURL, strings.NewReader(string(payload)),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var out struct {
		ClientID string `json:"client_id"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("bad register response: %w", err)
	}
	if out.ClientID == "" {
		return "", fmt.Errorf("notion register: empty client_id (http %d): %s",
			resp.StatusCode, string(body))
	}
	return out.ClientID, nil
}

// handleNotionConnect: DCR при первом запуске → редирект на /authorize с PKCE.
//
//	GET /api/integrations/notion/connect
func (h *Handler) handleNotionConnect(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	callbackURL := "http://" + r.Host + notionCallbackPath
	clientID := cfg.Integrations.Notion.ClientID
	if clientID == "" {
		id, regErr := registerNotionClient(r.Context(), callbackURL)
		if regErr != nil {
			http.Error(w, fmt.Sprintf("Notion client registration failed: %v", regErr),
				http.StatusBadGateway)
			return
		}
		clientID = id
		cfg.Integrations.Notion.ClientID = id
		if err := config.SaveConfig(h.configPath, cfg); err != nil {
			http.Error(w, fmt.Sprintf("Failed to save config: %v", err),
				http.StatusInternalServerError)
			return
		}
	}

	state, verifier, challenge, err := notionPKCE()
	if err != nil {
		http.Error(w, "Failed to generate state", http.StatusInternalServerError)
		return
	}
	registerNotionState(state, verifier)

	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {callbackURL},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	http.Redirect(w, r, notionAuthorizeURL+"?"+q.Encode(), http.StatusFound)
}

// handleNotionCallback: code+verifier → токен → .security.yml + MCP-сервер
// notion в конфиге (вот и весь «в пару кликов» — сервер прописывается сам).
//
//	GET /api/integrations/notion/callback?code=...&state=...
func (h *Handler) handleNotionCallback(w http.ResponseWriter, r *http.Request) {
	verifier, ok := takeNotionState(r.URL.Query().Get("state"))
	if !ok {
		http.Error(w, "Invalid or expired OAuth state — start connect again",
			http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Missing code from Notion", http.StatusBadRequest)
		return
	}

	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	clientID := cfg.Integrations.Notion.ClientID
	if clientID == "" {
		http.Error(w, "Notion not configured (client_id)", http.StatusBadRequest)
		return
	}
	callbackURL := "http://" + r.Host + notionCallbackPath

	token, refreshToken, workspace, err := exchangeNotionCode(
		r.Context(), clientID, code, verifier, callbackURL,
	)
	if err != nil {
		http.Error(w, fmt.Sprintf("Token exchange failed: %v", err), http.StatusBadGateway)
		return
	}

	n := cfg.Integrations.Notion
	n.AccessToken = *config.NewSecureString(token)
	if refreshToken != "" {
		n.RefreshToken = *config.NewSecureString(refreshToken)
	}
	if workspace != "" {
		n.WorkspaceName = workspace
	}
	cfg.Integrations.Notion = n
	upsertNotionMCPServer(cfg)

	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/mcp?notion=connected", http.StatusFound)
}

// upsertNotionMCPServer: сервер notion появляется в конфиге сам при connect.
// Идемпотентно: существующие поля сервера не затираются, кроме нужных.
func upsertNotionMCPServer(cfg *config.Config) {
	cfg.Tools.MCP.Enabled = true
	if cfg.Tools.MCP.Servers == nil {
		cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{}
	}
	srv := cfg.Tools.MCP.Servers["notion"]
	srv.Enabled = true
	srv.Type = "http"
	srv.URL = notionMCPURL
	if srv.Headers == nil {
		srv.Headers = map[string]string{}
	}
	srv.Headers["Authorization"] = "Bearer ${oauth:notion}"
	cfg.Tools.MCP.Servers["notion"] = srv
}

// handleNotionStatus: подключён ли Notion.
//
//	GET /api/integrations/notion/status
func (h *Handler) handleNotionStatus(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	n := cfg.Integrations.Notion
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"connected": n.Connected(),
		"workspace": n.WorkspaceName,
	})
}

// handleNotionDisconnect: стирает токен и выключает MCP-сервер notion.
//
//	POST /api/integrations/notion/disconnect
func (h *Handler) handleNotionDisconnect(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.LoadConfig(h.configPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load config: %v", err), http.StatusInternalServerError)
		return
	}
	cfg.Integrations.Notion.AccessToken = config.SecureString{}
	cfg.Integrations.Notion.RefreshToken = config.SecureString{}
	cfg.Integrations.Notion.WorkspaceName = ""
	if srv, ok := cfg.Tools.MCP.Servers["notion"]; ok {
		srv.Enabled = false
		cfg.Tools.MCP.Servers["notion"] = srv
	}
	if err := config.SaveConfig(h.configPath, cfg); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save config: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func exchangeNotionCode(
	ctx context.Context,
	clientID, code, verifier, redirectURI string,
) (string, string, string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, notionTokenURL, strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", "", "", err
	}
	var out struct {
		AccessToken   string `json:"access_token"`
		RefreshToken  string `json:"refresh_token"`
		WorkspaceName string `json:"workspace_name"`
		Error         string `json:"error"`
		ErrorDesc     string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", "", "", fmt.Errorf("bad token response: %w", err)
	}
	if out.Error != "" {
		return "", "", "", fmt.Errorf("notion: %s (%s)", out.Error, out.ErrorDesc)
	}
	if out.AccessToken == "" {
		return "", "", "", fmt.Errorf("notion: empty access_token (http %d)", resp.StatusCode)
	}
	return out.AccessToken, out.RefreshToken, out.WorkspaceName, nil
}
