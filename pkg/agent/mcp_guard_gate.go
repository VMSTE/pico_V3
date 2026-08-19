package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/pika"
)

// mcpToolSummary — минимальное описание MCP-тула для аудита.
type mcpToolSummary struct {
	Name        string
	Description string
}

// mcpSecurityPipeline возвращает живой pipeline, создавая и сохраняя при
// первом вызове (D-AUDIT-72): хэши Rug Pull должны переживать вызовы.
func (al *AgentLoop) mcpSecurityPipeline() *pika.MCPSecurityPipeline {
	al.mu.RLock()
	sec := al.mcpSecurity
	al.mu.RUnlock()
	if sec != nil {
		return sec
	}
	sec = pika.NewMCPSecurityPipeline(
		pika.DefaultMCPGuardConfig(), mapMCPServerPolicies(al.cfg), nil,
	)
	al.mu.Lock()
	al.mcpSecurity = sec
	al.mu.Unlock()
	return sec
}

// auditToolDefsWithGuard — ядро: пакетный StartupAudit тулов сервера.
// Возвращает name → reason для заблокированных. Чистое ядро:
// override и уведомления живут в guardAuditMCPTools.
func auditToolDefsWithGuard(
	ctx context.Context,
	sec *pika.MCPSecurityPipeline,
	caller pika.MCPGuardLLMCaller,
	serverName string,
	tools []mcpToolSummary,
) (map[string]string, error) {
	blocked := map[string]string{}
	if len(tools) == 0 {
		return blocked, nil
	}
	defs := make([]pika.MCPToolDef, 0, len(tools))
	for _, t := range tools {
		defs = append(defs, pika.MCPToolDef{Name: t.Name, Description: t.Description})
	}
	verdicts, err := sec.StartupAudit(ctx, serverName, defs, caller)
	if err != nil {
		return blocked, err
	}
	for _, v := range verdicts {
		switch strings.ToLower(v.Verdict) {
		case "malicious", "dangerous", "block":
			blocked[v.Name] = v.Reason
		}
	}
	return blocked, nil
}

// applyGuardExcept убирает из блокировок тулы, вручную разрешённые
// founder'ом (security.mcp.servers.<имя>.guard_except).
func applyGuardExcept(blocked map[string]string, except []string) map[string]bool {
	out := map[string]bool{}
	ex := map[string]bool{}
	for _, n := range except {
		ex[n] = true
	}
	for name := range blocked {
		if ex[name] {
			continue
		}
		out[name] = true
	}
	return out
}

// guardAuditMCPTools — сборка аудита + override + уведомление founder'а.
// Guard выключен / нет провайдера / ошибка Guard → nil (fail-open, warn-лог).
func (al *AgentLoop) guardAuditMCPTools(
	ctx context.Context, serverName string, tools []mcpToolSummary,
) map[string]bool {
	guardCfg := pika.DefaultMCPGuardConfig()
	// Волна 82 (бой 19 авг): честный конфиг mcp_guard из agents.list
	// (модель, startup_audit_enabled и пр.) вместо дефолтного —
	// настройки со страницы /subagents раньше игнорировались.
	if al.cfg != nil {
		guardCfg = mapMCPGuardConfig(al.cfg.ResolveAgentConfig("mcp_guard"))
		guardCfg.Model = resolveSatelliteModelID(al.cfg, guardCfg.Model)
	}
	if !guardCfg.Enabled || !guardCfg.StartupAuditEnabled {
		return nil
	}
	agent := al.registry.GetDefaultAgent()
	if agent == nil || agent.Provider == nil {
		return nil
	}
	sec := al.mcpSecurityPipeline()
	caller := guardLLMCaller{
		provider: agent.Provider,
		model:    guardCfg.Model,
		timeout:  time.Duration(guardCfg.TimeoutMs) * time.Millisecond,
	}
	blocked, err := auditToolDefsWithGuard(ctx, sec, caller, serverName, tools)
	if err != nil {
		logger.WarnCF("agent", "MCP Guard audit failed, allowing (fail-open)", map[string]any{
			"server": serverName, "error": err.Error(),
		})
		return nil
	}
	if len(blocked) == 0 {
		return nil
	}

	var except []string
	if al.cfg != nil {
		except = al.cfg.Security.MCP.Servers[serverName].GuardExcept
	}
	out := applyGuardExcept(blocked, except)
	if len(out) == 0 {
		return nil
	}

	var lines []string
	for name := range out {
		lines = append(lines, fmt.Sprintf("• %s — %s", name, blocked[name]))
	}
	logger.WarnCF("agent", "MCP Guard blocked tools at registration", map[string]any{
		"server":  serverName,
		"blocked": len(out),
	})
	al.notifyManagerGuardBlock(serverName, lines)
	return out
}

// notifyManagerGuardBlock шлёт founder'у список заблокированных тулов
// и инструкцию для ручного разрешения. Решение — за пользователем.
func (al *AgentLoop) notifyManagerGuardBlock(serverName string, lines []string) {
	if al.bus == nil || al.cfg == nil {
		return
	}
	sender := pika.NewManagerSender(al.bus, al.cfg)
	if sender == nil {
		return
	}
	text := fmt.Sprintf(
		"🛡 MCP Guard заблокировал тулы сервера «%s»:\n%s\n\n"+
			"Они НЕ зарегистрированы, модель их не видит. "+
			"Если доверяешь — добавь в config:\n"+
			"security.mcp.servers.%s.guard_except: [<имена тулов>]\n"+
			"и перезагрузи — при следующем старте они зарегистрируются.",
		serverName, strings.Join(lines, "\n"), serverName,
	)
	nctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := sender.SendMessage(nctx, text); err != nil {
		logger.WarnCF("agent", "failed to notify manager about guard block", map[string]any{
			"server": serverName, "error": err.Error(),
		})
	}
}
