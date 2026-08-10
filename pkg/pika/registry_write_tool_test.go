package pika

import (
	"context"
	"strings"
	"testing"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

func newTestRegistryWriteTool(t *testing.T) (*RegistryWriteTool, *RegistryHandler) {
	t.Helper()
	h := NewRegistryHandler(setupTestDB(t))
	return NewRegistryWriteTool(h), h
}

// 12. Реализует toolshared.Tool.
var _ toolshared.Tool = (*RegistryWriteTool)(nil)

// 1.
func TestRegistryWriteTool_Name(t *testing.T) {
	tool, _ := newTestRegistryWriteTool(t)
	if tool.Name() != "registry_write" {
		t.Errorf("Name() = %q, want registry_write", tool.Name())
	}
}

// 2.
func TestRegistryWriteTool_CreateRunbook(t *testing.T) {
	tool, _ := newTestRegistryWriteTool(t)
	res := tool.Execute(context.Background(), map[string]any{
		"kind":    "runbook",
		"key":     "deploy",
		"summary": "deploy procedure",
		"data":    map[string]any{"steps": []string{"build", "push"}},
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, `"created"`) {
		t.Errorf("expected created, got %s", res.ForLLM)
	}
}

// 3.
func TestRegistryWriteTool_UpdateExisting(t *testing.T) {
	tool, _ := newTestRegistryWriteTool(t)
	args := map[string]any{"kind": "runbook", "key": "deploy", "summary": "v1"}
	tool.Execute(context.Background(), args)
	args["summary"] = "v2"
	res := tool.Execute(context.Background(), args)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, `"updated"`) {
		t.Errorf("expected updated, got %s", res.ForLLM)
	}
}

// 4.
func TestRegistryWriteTool_CreateScript(t *testing.T) {
	tool, _ := newTestRegistryWriteTool(t)
	res := tool.Execute(context.Background(), map[string]any{
		"kind": "script",
		"key":  "backup_db",
		"data": map[string]any{"commands": []string{"pg_dump"}},
	})
	if res.IsError || !strings.Contains(res.ForLLM, `"created"`) {
		t.Errorf("expected created script, got %s", res.ForLLM)
	}
}

// 5.
func TestRegistryWriteTool_CreateSnapshot(t *testing.T) {
	tool, _ := newTestRegistryWriteTool(t)
	res := tool.Execute(context.Background(), map[string]any{
		"kind": "snapshot", "key": "nginx-config", "summary": "config snapshot",
	})
	if res.IsError || !strings.Contains(res.ForLLM, `"created"`) {
		t.Errorf("expected created snapshot, got %s", res.ForLLM)
	}
}

// 6.
func TestRegistryWriteTool_CreateCorrectionRule(t *testing.T) {
	tool, _ := newTestRegistryWriteTool(t)
	res := tool.Execute(context.Background(), map[string]any{
		"kind": "correction_rule", "key": "no-force-push", "summary": "never force push",
	})
	if res.IsError || !strings.Contains(res.ForLLM, `"created"`) {
		t.Errorf("expected created correction_rule, got %s", res.ForLLM)
	}
}

// 7.
func TestRegistryWriteTool_InvalidKind(t *testing.T) {
	tool, _ := newTestRegistryWriteTool(t)
	res := tool.Execute(context.Background(), map[string]any{
		"kind": "invalid", "key": "x",
	})
	if !res.IsError {
		t.Error("expected error for invalid kind")
	}
}

// 8.
func TestRegistryWriteTool_EmptyKey(t *testing.T) {
	tool, _ := newTestRegistryWriteTool(t)
	res := tool.Execute(context.Background(), map[string]any{
		"kind": "runbook", "key": "",
	})
	if !res.IsError {
		t.Error("expected error for empty key")
	}
}

// 9.
func TestRegistryWriteTool_MissingKind(t *testing.T) {
	tool, _ := newTestRegistryWriteTool(t)
	res := tool.Execute(context.Background(), map[string]any{"key": "x"})
	if !res.IsError {
		t.Error("expected error for missing kind")
	}
}

// 10.
func TestRegistryWriteTool_InvalidData(t *testing.T) {
	tool, _ := newTestRegistryWriteTool(t)
	res := tool.Execute(context.Background(), map[string]any{
		"kind": "runbook", "key": "x", "data": "{broken",
	})
	if !res.IsError {
		t.Error("expected error for broken JSON data")
	}
}

// 11.
func TestRegistryWriteTool_WithTags(t *testing.T) {
	tool, h := newTestRegistryWriteTool(t)
	res := tool.Execute(context.Background(), map[string]any{
		"kind": "runbook", "key": "deploy_x",
		"summary": "deploy x", "tags": []any{"deploy", "docker"},
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.ForLLM)
	}
	found, err := h.HandleSearch(context.Background(), "runbook", "deploy%")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Errorf("expected 1 search result, got %d", len(found))
	}
}
