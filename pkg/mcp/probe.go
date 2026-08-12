package mcp

import (
	"context"
	"fmt"

	"github.com/sipeed/picoclaw/pkg/config"
)

// ProbeResult describes a successful server probe (D-AUDIT-84).
type ProbeResult struct {
	ToolCount int
}

// ProbeServer connects to a single MCP server entry and reports how many
// tools it exposes. Shared by the CLI (`picoclaw mcp test`) and the web UI.
func ProbeServer(
	ctx context.Context,
	name string,
	server config.MCPServerConfig,
	workspacePath string,
) (ProbeResult, error) {
	mgr := NewManager()
	defer func() { _ = mgr.Close() }()

	server.Enabled = true
	mcpCfg := config.MCPConfig{
		ToolConfig: config.ToolConfig{Enabled: true},
		Servers: map[string]config.MCPServerConfig{
			name: server,
		},
	}

	if err := mgr.LoadFromMCPConfig(ctx, mcpCfg, workspacePath); err != nil {
		return ProbeResult{}, err
	}

	conn, ok := mgr.GetServer(name)
	if !ok {
		return ProbeResult{}, fmt.Errorf("server %q did not register a connection", name)
	}

	return ProbeResult{ToolCount: len(conn.Tools)}, nil
}
