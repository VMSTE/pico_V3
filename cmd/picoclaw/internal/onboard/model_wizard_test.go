package onboard

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchModelIDs_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Errorf("path = %q, want /models", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("missing bearer auth, got %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"gpt-a"},{"id":"gpt-b"}]}`)
	}))
	defer srv.Close()

	p := &wizardProvider{Key: "test", APIBase: srv.URL}
	ids, err := fetchModelIDs(p, "test-key")
	if err != nil {
		t.Fatalf("fetchModelIDs: %v", err)
	}
	if len(ids) != 2 || ids[0] != "gpt-a" {
		t.Errorf("ids = %v, want [gpt-a gpt-b]", ids)
	}
}

func TestFetchModelIDs_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"bad key"}`)
	}))
	defer srv.Close()

	p := &wizardProvider{Key: "test", APIBase: srv.URL}
	if _, err := fetchModelIDs(p, "bad"); err == nil {
		t.Fatal("expected error for HTTP 401")
	}
}

func TestFetchModelIDs_AnthropicHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "sk-ant" {
			t.Errorf("x-api-key = %q, want sk-ant", r.Header.Get("X-Api-Key"))
		}
		if r.Header.Get("Anthropic-Version") == "" {
			t.Error("Anthropic-Version header missing")
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected Authorization header: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":[{"id":"claude-x"}]}`)
	}))
	defer srv.Close()

	p := &wizardProvider{Key: "anthropic", APIBase: srv.URL, Anthropic: true}
	ids, err := fetchModelIDs(p, "sk-ant")
	if err != nil {
		t.Fatalf("fetchModelIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != "claude-x" {
		t.Errorf("ids = %v, want [claude-x]", ids)
	}
}
