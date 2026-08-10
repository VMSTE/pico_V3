package agent

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

// D-AUDIT-69: per-server ACL на входе в реестр — deny-by-default.
func TestGatedMCPToolNames_ACL(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.MCP.Servers = map[string]config.MCPServerConfig{
		"srv": {Enabled: true, Command: "x"},
	}
	cfg.Security.MCP.Servers = map[string]config.MCPServerACLConfig{
		"srv": {AllowedTools: []string{"ok_tool"}},
	}
	al := &AgentLoop{cfg: cfg}

	allow := al.gatedMCPToolNames("srv", []string{"ok_tool", "bad_tool"})
	if !allow["ok_tool"] {
		t.Error("ok_tool should pass ACL")
	}
	if allow["bad_tool"] {
		t.Error("bad_tool should be blocked by ACL")
	}

	// Сервер без политики — deny all.
	allowUnknown := al.gatedMCPToolNames("unknown", []string{"anything"})
	if len(allowUnknown) != 0 {
		t.Errorf("unknown server must be denied, got %v", allowUnknown)
	}
}
