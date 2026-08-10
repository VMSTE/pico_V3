package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/pika"
)

func newTestSkillAuditor(t *testing.T, guardResponse string) *skillGuardAuditor {
	t.Helper()
	promptPath := filepath.Join(t.TempDir(), "mcp_guard.md")
	if err := os.WriteFile(promptPath, []byte("You are MCP Guard."), 0o600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	cfg := pika.DefaultMCPGuardConfig()
	cfg.PromptFile = promptPath
	sec := pika.NewMCPSecurityPipeline(cfg, nil, nil)
	return &skillGuardAuditor{
		sec: sec,
		caller: guardLLMCaller{
			provider: &countingMockProvider{response: guardResponse},
			model:    "test-guard-model",
			timeout:  5 * time.Second,
		},
	}
}

// Malicious вердикт Guard → установка блокируется.
func TestSkillGuardAuditor_BlocksMalicious(t *testing.T) {
	aud := newTestSkillAuditor(
		t,
		`{"mode":"startup_audit","tools":[{"name":"skill:evil","verdict":"malicious","confidence":"high","reason":"exfiltrates system prompt"}]}`,
	)
	blocked, reason := aud.AuditSkill(context.Background(), "evil", "# Evil skill")
	if !blocked {
		t.Fatal("malicious skill should be blocked")
	}
	if reason == "" {
		t.Error("block reason should not be empty")
	}
}

// Safe вердикт → пропускаем.
func TestSkillGuardAuditor_AllowsSafe(t *testing.T) {
	aud := newTestSkillAuditor(
		t,
		`{"mode":"startup_audit","tools":[{"name":"skill:good","verdict":"safe","confidence":"high","reason":"normal docs"}]}`,
	)
	blocked, _ := aud.AuditSkill(context.Background(), "good", "# Good skill")
	if blocked {
		t.Error("safe skill should not be blocked")
	}
}
