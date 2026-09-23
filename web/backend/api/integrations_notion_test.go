package api

// Волна 117 (ТЗ-117): Notion OAuth flow — DCR + PKCE + автозапись MCP-сервера.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func setupNotionMock(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/register":
			fmt.Fprint(w, `{"client_id":"notion-client-1"}`)
		case "/token":
			_ = r.ParseForm()
			if r.PostForm.Get("code_verifier") == "" {
				t.Error("token request without code_verifier (PKCE обязателен)")
			}
			fmt.Fprint(w, `{"access_token":"ntn_live_token","refresh_token":"rt_1","workspace_name":"VECTR"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	oldAuth, oldToken, oldReg := notionAuthorizeURL, notionTokenURL, notionRegisterURL
	notionAuthorizeURL = srv.URL + "/authorize"
	notionTokenURL = srv.URL + "/token"
	notionRegisterURL = srv.URL + "/register"
	t.Cleanup(func() {
		srv.Close()
		notionAuthorizeURL, notionTokenURL, notionRegisterURL = oldAuth, oldToken, oldReg
	})
	return srv
}

func TestNotionConnectCallbackFlow(t *testing.T) {
	path, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	setupNotionMock(t)

	h := NewHandler(path)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// connect → DCR + редирект на authorize с PKCE
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/integrations/notion/connect", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("connect status = %d, body=%s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "/authorize?") {
		t.Fatalf("redirect = %q", loc)
	}
	for _, want := range []string{"client_id=notion-client-1", "code_challenge=", "state="} {
		if !strings.Contains(loc, want) {
			t.Fatalf("redirect missing %q: %s", want, loc)
		}
	}
	state := strings.Split(loc, "state=")[1]
	if i := strings.Index(state, "&"); i >= 0 {
		state = state[:i]
	}

	// callback → токен сохранён, MCP-сервер записан
	cbRec := httptest.NewRecorder()
	cbReq := httptest.NewRequest(
		http.MethodGet,
		"/api/integrations/notion/callback?code=abc&state="+state,
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
	n := cfg.Integrations.Notion
	if n.AccessToken.String() != "ntn_live_token" {
		t.Fatal("access token not stored")
	}
	if n.WorkspaceName != "VECTR" {
		t.Fatalf("workspace = %q", n.WorkspaceName)
	}
	if n.ClientID != "notion-client-1" {
		t.Fatalf("client_id = %q (DCR не сохранился)", n.ClientID)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "ntn_live_token") {
		t.Fatal("config.json leaks access token")
	}
	// автозапись MCP-сервера
	if !cfg.Tools.MCP.Enabled {
		t.Fatal("tools.mcp не включён")
	}
	srv, ok := cfg.Tools.MCP.Servers["notion"]
	if !ok {
		t.Fatal("MCP server notion не записан в конфиг")
	}
	if !srv.Enabled || srv.Type != "http" || srv.URL != notionMCPURL {
		t.Fatalf("server = %+v", srv)
	}
	if srv.Headers["Authorization"] != "Bearer ${oauth:notion}" {
		t.Fatalf("headers = %v", srv.Headers)
	}
}

func TestNotionStatusAndDisconnect(t *testing.T) {
	path, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Integrations.Notion.AccessToken = *config.NewSecureString("ntn_x")
	cfg.Integrations.Notion.WorkspaceName = "VECTR"
	upsertNotionMCPServer(cfg)
	if saveErr := config.SaveConfig(path, cfg); saveErr != nil {
		t.Fatal(saveErr)
	}

	h := NewHandler(path)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/integrations/notion/status", nil))
	if !strings.Contains(rec.Body.String(), `"connected":true`) ||
		!strings.Contains(rec.Body.String(), `"workspace":"VECTR"`) {
		t.Fatalf("status body = %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	mux.ServeHTTP(
		rec,
		httptest.NewRequest(http.MethodPost, "/api/integrations/notion/disconnect", nil),
	)
	cfg, err = config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Integrations.Notion.Connected() {
		t.Fatal("still connected after disconnect")
	}
	if cfg.Tools.MCP.Servers["notion"].Enabled {
		t.Fatal("MCP server notion должен быть выключен после disconnect")
	}
}
