package agent

import (
	"context"
	"fmt"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/pika"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// D-AUDIT-72: Rug Pull Guard — event-driven (D-SEC-v2). Сервер сам сообщает
// notifications/tools/list_changed → перечитываем tools/list → hash-diff →
// re-audit изменённых/новых через Guard → malicious → Unregister из реестров
// + уведомление founder'а. Никаких тикеров и опросов.

// rugPullMaxListChangedPerHour — flood-guard (D-SEC-v2): сервер, спамящий
// list_changed чаще лимита, сам по себе suspicious.
const rugPullMaxListChangedPerHour = 2

// recordListChanged регистрирует событие; false = превышен лимит (flood).
func (rt *mcpRuntime) recordListChanged(serverName string, maxPerHour int) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.listChangedLog == nil {
		rt.listChangedLog = map[string][]int64{}
	}
	cutoff := time.Now().Add(-time.Hour).Unix()
	log := rt.listChangedLog[serverName][:0]
	for _, ts := range rt.listChangedLog[serverName] {
		if ts > cutoff {
			log = append(log, ts)
		}
	}
	if len(log) >= maxPerHour {
		rt.listChangedLog[serverName] = log
		return false
	}
	rt.listChangedLog[serverName] = append(log, time.Now().Unix())
	return true
}

// notifyManager отправляет founder'у произвольное security-сообщение (D-AUDIT-72).
func (al *AgentLoop) notifyManager(text string) {
	if al.bus == nil || al.cfg == nil {
		return
	}
	sender := pika.NewManagerSender(al.bus, al.cfg)
	if sender == nil {
		return
	}
	nctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := sender.SendMessage(nctx, text); err != nil {
		logger.WarnCF("agent", "failed to notify manager", map[string]any{"error": err.Error()})
	}
}

// handleToolsListChanged — обработчик notifications/tools/list_changed.
func (al *AgentLoop) handleToolsListChanged(serverName string) {
	guardCfg := pika.DefaultMCPGuardConfig()
	if !guardCfg.Enabled || !guardCfg.ReauditOnListChanged || al.cfg == nil {
		return
	}
	// Flood-guard до любой работы: частые list_changed → alert, игнорируем.
	if !al.mcp.recordListChanged(serverName, rugPullMaxListChangedPerHour) {
		logger.WarnCF("agent", "Rug Pull: list_changed flood", map[string]any{"server": serverName})
		al.notifyManager(fmt.Sprintf(
			"⚠️ MCP-сервер «%s» подозрительно часто меняет список тулов "+
				"(>%d/час) — события игнорируются. Проверь сервер.",
			serverName, rugPullMaxListChangedPerHour,
		))
		return
	}
	mgr := al.mcp.getManager()
	if mgr == nil {
		return
	}
	al.rugPullRecheck(serverName, mgr)
}

// rugPullRecheck — ядро: перечитать, сверить хэши, re-audit, применить.
func (al *AgentLoop) rugPullRecheck(serverName string, mgr mcpToolManager) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	current, err := mgr.RefreshServerTools(ctx, serverName)
	if err != nil {
		logger.WarnCF("agent", "Rug Pull: refresh failed", map[string]any{
			"server": serverName, "error": err.Error(),
		})
		return
	}
	defs := make([]pika.MCPToolDef, 0, len(current))
	for _, t := range current {
		if t != nil {
			defs = append(defs, pika.MCPToolDef{Name: t.Name, Description: t.Description})
		}
	}
	sec := al.mcpSecurityPipeline()
	if !sec.HasToolHashes(serverName) {
		// Эталона нет (Guard был выключен при регистрации) — записываем.
		sec.UpdateToolHashes(serverName, defs)
		return
	}
	changed, added := sec.CheckRugPull(serverName, defs)
	if len(changed) == 0 && len(added) == 0 {
		return
	}
	logger.WarnCF("agent", "Rug Pull: tool definitions changed", map[string]any{
		"server": serverName, "changed": changed, "added": added,
	})

	// Re-audit только изменившихся/новых.
	auditSet := map[string]bool{}
	for _, n := range changed {
		auditSet[n] = true
	}
	addedSet := map[string]bool{}
	for _, n := range added {
		auditSet[n] = true
		addedSet[n] = true
	}
	var summaries []mcpToolSummary
	for _, t := range current {
		if t != nil && auditSet[t.Name] {
			summaries = append(summaries, mcpToolSummary{Name: t.Name, Description: t.Description})
		}
	}
	blocked := al.guardAuditMCPTools(ctx, serverName, summaries)
	// Новые тулы обязаны пройти ACL, как при старте (D-AUDIT-69).
	aclAllow := al.gatedMCPToolNames(serverName, added)

	serverCfg := al.cfg.Tools.MCP.Servers[serverName]
	registerAsHidden := serverIsDeferred(al.cfg.Tools.MCP.Discovery.Enabled, serverCfg)

	for _, t := range current {
		if t == nil || !auditSet[t.Name] {
			continue
		}
		regName := tools.MCPToolName(serverName, t.Name)
		if blocked[t.Name] {
			// Malicious: вырываем из реестров всех агентов.
			for _, agentID := range al.registry.ListAgentIDs() {
				if a, ok := al.registry.GetAgent(agentID); ok {
					a.Tools.Unregister(regName)
				}
			}
			logger.WarnCF("agent", "Rug Pull: tool deactivated", map[string]any{
				"server": serverName, "tool": t.Name, "registered_name": regName,
			})
			continue
		}
		// Новый тул, не прошедший ACL — не регистрируем.
		if addedSet[t.Name] && !aclAllow[t.Name] {
			continue
		}
		// Прошёл: (пере)регистрируем — описание обновляется / новый появляется.
		for _, agentID := range al.registry.ListAgentIDs() {
			agent, ok := al.registry.GetAgent(agentID)
			if !ok {
				continue
			}
			mcpTool := tools.NewMCPTool(mgr, serverName, t)
			mcpTool.SetWorkspace(agent.Workspace)
			mcpTool.SetMaxInlineTextRunes(al.cfg.Tools.MCP.GetMaxInlineTextChars())
			if registerAsHidden {
				agent.Tools.RegisterHidden(mcpTool)
			} else {
				agent.Tools.Register(mcpTool)
			}
		}
	}
	// Эталон = текущее состояние минус заблокированные.
	kept := defs[:0]
	for _, d := range defs {
		if !blocked[d.Name] {
			kept = append(kept, d)
		}
	}
	sec.UpdateToolHashes(serverName, kept)
}

// mcpToolsRefresher — перечитывание списка тулов с живого сервера.
type mcpToolsRefresher interface {
	RefreshServerTools(ctx context.Context, name string) ([]*sdkmcp.Tool, error)
}

// mcpToolManager — refresh + вызов тулов (нужен NewMCPTool при re-register).
type mcpToolManager interface {
	mcpToolsRefresher
	CallTool(
		ctx context.Context, serverName, toolName string, arguments map[string]any,
	) (*sdkmcp.CallToolResult, error)
}
