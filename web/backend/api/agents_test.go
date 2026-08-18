package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// D-AUDIT-97: named agents CRUD.

func doAgentsRequest(
	t *testing.T, mux *http.ServeMux, method, path string, body any,
) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAgentsCRUD(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	workspace := filepath.Join(t.TempDir(), "researcher")

	rec := doAgentsRequest(t, mux, http.MethodPost, "/api/agents", map[string]any{
		"id": "researcher", "name": "Researcher",
		"description": "Deep research", "workspace": workspace, "model": "gpt-4",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	agentMD, err := os.ReadFile(filepath.Join(workspace, "AGENT.md"))
	if err != nil {
		t.Fatalf("AGENT.md not scaffolded: %v", err)
	}
	if !strings.Contains(string(agentMD), "name: Researcher") ||
		!strings.Contains(string(agentMD), "description: Deep research") {
		t.Fatalf("AGENT.md frontmatter mismatch:\n%s", string(agentMD))
	}

	rec = doAgentsRequest(t, mux, http.MethodPost, "/api/agents", map[string]any{
		"id": "researcher", "name": "Dup",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want 409", rec.Code)
	}

	rec = doAgentsRequest(t, mux, http.MethodPost, "/api/agents", map[string]any{
		"id": "atomizer", "name": "Nope",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("pipeline id status = %d, want 400", rec.Code)
	}

	rec = doAgentsRequest(t, mux, http.MethodGet, "/api/agents", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "researcher") ||
		!strings.Contains(rec.Body.String(), "Deep research") {
		t.Fatalf("list body missing agent: %s", rec.Body.String())
	}

	rec = doAgentsRequest(t, mux, http.MethodPut, "/api/agents/researcher", map[string]any{
		"name": "Research", "description": "Updated desc", "workspace": workspace,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	agentMD, err = os.ReadFile(filepath.Join(workspace, "AGENT.md"))
	if err != nil {
		t.Fatalf("AGENT.md read after update: %v", err)
	}
	if !strings.Contains(string(agentMD), "description: Updated desc") {
		t.Fatalf("AGENT.md description not updated:\n%s", string(agentMD))
	}

	rec = doAgentsRequest(t, mux, http.MethodDelete, "/api/agents/researcher", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200", rec.Code)
	}
	rec = doAgentsRequest(t, mux, http.MethodGet, "/api/agents", nil)
	if strings.Contains(rec.Body.String(), `"id":"researcher"`) {
		t.Fatalf("agent still listed after delete: %s", rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(workspace, "AGENT.md")); err != nil {
		t.Fatalf("AGENT.md must be preserved after delete: %v", err)
	}

	rec = doAgentsRequest(t, mux, http.MethodDelete, "/api/agents/researcher", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", rec.Code)
	}
}
