package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// D-AUDIT-76: /api/update отклоняет чужой источник до всякой загрузки.
func TestHandleUpdateRejectsForeignURL(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/update",
		strings.NewReader(`{"url":"https://evil.com/pwn","binary":"picoclaw-launcher"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("foreign URL: status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}

// D-AUDIT-76: traversal в имени бинаря отклоняется.
func TestHandleUpdateRejectsTraversalBinary(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()
	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/update",
		strings.NewReader(`{"binary":"../../etc/pwn"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("traversal binary: status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
}
