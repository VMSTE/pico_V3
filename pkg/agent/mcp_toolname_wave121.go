package agent

// Волна 121 (срез Б): единый разбор имён MCP-тулов и живой статус серверов.

import (
	"github.com/sipeed/picoclaw/pkg/mcp"
	integrationtools "github.com/sipeed/picoclaw/pkg/tools/integration"
)

// parseMCPTool: сервер/тул из имени — по реестру сконфигурированных серверов
// (integrationtools.ParseMCPToolName). Один источник конвенции на всех
// потребителей: sanitize-гарда, журнал событий, подсказки not-found.
func (al *AgentLoop) parseMCPTool(toolName string) (server, tool string, ok bool) {
	if al.cfg == nil {
		return "", "", false
	}
	names := make([]string, 0, len(al.cfg.Tools.MCP.Servers))
	for name := range al.cfg.Tools.MCP.Servers {
		names = append(names, name)
	}
	return integrationtools.ParseMCPToolName(toolName, names)
}

// mcpStatusFn: живой статус сервера из менеджера (для prompt-контрибьютора;
// снапшот на момент регистрации не годится — статус меняется на 401/reconnect).
func mcpStatusFn(mgr *mcp.Manager) func(serverName string) (connected bool, tools int, lastError string, ok bool) {
	return func(serverName string) (bool, int, string, bool) {
		if mgr == nil {
			return false, 0, "", false
		}
		for _, s := range mgr.ServerStatuses() {
			if s.Name == serverName {
				return s.Connected, s.Tools, s.LastError, true
			}
		}
		return false, 0, "", false
	}
}
