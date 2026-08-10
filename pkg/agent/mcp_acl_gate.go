package agent

import (
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/pika"
)

// gatedMCPToolNames применяет per-server ACL к списку имён тулов
// (D-AUDIT-69, единые ворота аудита). Возвращает множество разрешённых имён.
//
// deny-by-default (D-SEC-v3): сервер без политики или без allowed_tools
// (и без trust_level: "internal") не пропускает ни одного тула.
// Если security pipeline не подключён — строится из конфига на месте,
// так что ворота работают независимо от порядка wiring.
func (al *AgentLoop) gatedMCPToolNames(serverName string, names []string) map[string]bool {
	sec := al.mcpSecurityPipeline()
	defs := make([]pika.MCPToolDef, 0, len(names))
	for _, n := range names {
		defs = append(defs, pika.MCPToolDef{Name: n})
	}
	allowed := sec.FilterAllowedTools(serverName, defs)
	set := make(map[string]bool, len(allowed))
	for _, d := range allowed {
		set[d.Name] = true
	}
	if len(set) != len(names) {
		logger.WarnCF("agent", "MCP ACL blocked tools at registration (deny-by-default)", map[string]any{
			"server":  serverName,
			"allowed": len(set),
			"total":   len(names),
			"hint": "add security.mcp.servers." + serverName +
				".allowed_tools or trust_level: \"internal\"",
		})
	}
	return set
}
