package agent

import (
	"context"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/pika"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// D-AUDIT-70: продовая реализация pika.MCPGuardLLMCaller.
// До этого существовал только тестовый мок — Guard-аудит был мёртвым кодом.
type guardLLMCaller struct {
	provider providers.LLMProvider
	model    string
	timeout  time.Duration
}

func (c guardLLMCaller) CallGuardLLM(
	ctx context.Context, systemPrompt, userInput string,
) (string, error) {
	tctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	resp, err := c.provider.Chat(tctx, []providers.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userInput},
	}, nil, c.model, nil)
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// skillGuardAuditor прогоняет SKILL.md через MCP Guard startup_audit
// (скилл подаётся как pseudo-tool: имя + содержимое как описание).
// Блокируем только явные malicious/dangerous вердикты.
// Fail-open: ошибка аудита → warn-лог и пропуск (доступность > ложные блоки).
type skillGuardAuditor struct {
	sec    *pika.MCPSecurityPipeline
	caller pika.MCPGuardLLMCaller
}

func (a *skillGuardAuditor) AuditSkill(
	ctx context.Context, slug, skillMarkdown string,
) (bool, string) {
	defs := []pika.MCPToolDef{{
		Name:        "skill:" + slug,
		Description: skillMarkdown,
	}}
	verdicts, err := a.sec.StartupAudit(ctx, "skill:"+slug, defs, a.caller)
	if err != nil {
		logger.WarnCF("agent", "Skill audit failed, allowing (fail-open)", map[string]any{
			"slug": slug, "error": err.Error(),
		})
		return false, ""
	}
	for _, v := range verdicts {
		switch strings.ToLower(v.Verdict) {
		case "malicious", "dangerous", "block":
			return true, v.Reason
		}
	}
	return false, ""
}

// newSkillGuardAuditor собирает аудитор скиллов. nil = Guard выключен
// в конфиге — тогда установка работает как раньше (модерация реестра).
func newSkillGuardAuditor(al *AgentLoop, provider providers.LLMProvider) *skillGuardAuditor {
	guardCfg := pika.DefaultMCPGuardConfig()
	if !guardCfg.Enabled || !guardCfg.StartupAuditEnabled {
		return nil
	}
	sec := al.mcpSecurity
	if sec == nil {
		sec = pika.NewMCPSecurityPipeline(guardCfg, nil, nil)
	}
	return &skillGuardAuditor{
		sec: sec,
		caller: guardLLMCaller{
			provider: provider,
			model:    guardCfg.Model,
			timeout:  time.Duration(guardCfg.TimeoutMs) * time.Millisecond,
		},
	}
}
