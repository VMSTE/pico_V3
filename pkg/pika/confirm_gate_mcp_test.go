package pika

import (
	"context"
	"testing"
)

// Волна 110 (ТЗ-110): MCP-записи во внешние системы — через confirm.

// mcp_github_create_or_update_file → спросить (запись на GitHub).
func TestEffectGate_MCPGithubWriteAsks(t *testing.T) {
	sender := &effectMockSender{}
	sender.approved = true
	cg := effectTestGate(sender, StateHealthy)

	_, err := cg.ApproveTool(context.Background(), &ConfirmApprovalRequest{
		Tool: "mcp_github_create_or_update_file",
		Arguments: map[string]any{
			"owner": "VMSTE", "repo": "pico_V3", "path": "README.md",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sender.called {
		t.Error("github write via MCP must require confirmation")
	}
}

// mcp_github_delete_file → спросить (удаление).
func TestEffectGate_MCPGithubDeleteAsks(t *testing.T) {
	sender := &effectMockSender{}
	sender.approved = true
	cg := effectTestGate(sender, StateHealthy)

	_, err := cg.ApproveTool(context.Background(), &ConfirmApprovalRequest{
		Tool:      "mcp_github_delete_file",
		Arguments: map[string]any{"owner": "VMSTE", "repo": "pico_V3"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sender.called {
		t.Error("github delete via MCP must require confirmation")
	}
}

// mcp_github_get_file_contents → молча (чтение).
func TestEffectGate_MCPGithubReadSilent(t *testing.T) {
	sender := &effectMockSender{}
	cg := effectTestGate(sender, StateHealthy)

	decision, err := cg.ApproveTool(context.Background(), &ConfirmApprovalRequest{
		Tool:      "mcp_github_get_file_contents",
		Arguments: map[string]any{"owner": "VMSTE", "repo": "pico_V3"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Approved || sender.called {
		t.Error("github read via MCP must be silent")
	}
}

// Разбор имени: сервер/тул, не-MCP имена, read-тулы.
func TestMCPWriteEffect_Parsing(t *testing.T) {
	eff, ok := mcpWriteEffect("mcp_github_delete_file")
	if !ok || eff.Key != "mcp.github.write" {
		t.Errorf("delete_file: ok=%v key=%q", ok, eff.Key)
	}
	if _, ok := mcpWriteEffect("mcp_github_get_file_contents"); ok {
		t.Error("read tool must not be a write effect")
	}
	if _, ok := mcpWriteEffect("write_file"); ok {
		t.Error("non-MCP tool must not match")
	}
	if _, ok := mcpWriteEffect("mcp_unknownserver_thing"); ok {
		t.Error("unknown write-less tool must not match")
	}
}
