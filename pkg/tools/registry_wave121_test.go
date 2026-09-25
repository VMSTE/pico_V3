package tools

// Волна 121 (срез Б): честный Tool not found со списком доступных.

import (
	"strings"
	"testing"
)

func TestToolNotFoundMessage_MCPListsRegistered(t *testing.T) {
	msg := toolNotFoundMessage("mcp_notion_fetch", []string{"exec", "mcp_notion_search", "mcp_github_get_me"})
	if !strings.Contains(msg, "mcp_notion_search") || !strings.Contains(msg, "mcp_github_get_me") {
		t.Errorf("message must list registered mcp tools: %s", msg)
	}
	if !strings.Contains(msg, "ACL") {
		t.Errorf("message must mention ACL: %s", msg)
	}
	if strings.Contains(msg, `"exec"`) {
		t.Errorf("builtin tools must not be listed: %s", msg)
	}
}

func TestToolNotFoundMessage_NonMCPUnchanged(t *testing.T) {
	msg := toolNotFoundMessage("no_such_tool", []string{"exec"})
	if msg != `tool "no_such_tool" not found` {
		t.Errorf("non-mcp message changed: %s", msg)
	}
}
