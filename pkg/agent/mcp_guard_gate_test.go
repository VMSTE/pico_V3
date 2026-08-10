package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/pika"
)

func newTestGuardSec(t *testing.T) *pika.MCPSecurityPipeline {
	t.Helper()
	promptPath := filepath.Join(t.TempDir(), "mcp_guard.md")
	if err := os.WriteFile(promptPath, []byte("You are MCP Guard."), 0o600); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	cfg := pika.DefaultMCPGuardConfig()
	cfg.PromptFile = promptPath
	return pika.NewMCPSecurityPipeline(cfg, nil, nil)
}

// D-AUDIT-71: malicious блокируется с причиной, safe проходит.
func TestAuditToolDefsWithGuard_BlocksMalicious(t *testing.T) {
	sec := newTestGuardSec(t)
	caller := guardLLMCaller{
		provider: &countingMockProvider{
			response: `{"mode":"startup_audit","tools":[{"name":"evil","verdict":"malicious","reason":"injection in description"},{"name":"good","verdict":"safe"}]}`,
		},
		model:   "test-guard",
		timeout: 5 * time.Second,
	}
	blocked, err := auditToolDefsWithGuard(
		context.Background(), sec, caller, "srv",
		[]mcpToolSummary{{Name: "evil"}, {Name: "good"}},
	)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if _, ok := blocked["evil"]; !ok {
		t.Error("evil tool should be blocked")
	}
	if blocked["evil"] == "" {
		t.Error("block should carry a reason")
	}
	if _, ok := blocked["good"]; ok {
		t.Error("good tool should not be blocked")
	}
}

// Ошибка парсинга ответа Guard → fail-open: ничего не блокируем.
func TestAuditToolDefsWithGuard_FailOpen(t *testing.T) {
	sec := newTestGuardSec(t)
	caller := guardLLMCaller{
		provider: &countingMockProvider{response: "not json at all"},
		model:    "test-guard",
		timeout:  5 * time.Second,
	}
	blocked, _ := auditToolDefsWithGuard(
		context.Background(), sec, caller, "srv",
		[]mcpToolSummary{{Name: "any"}},
	)
	if len(blocked) != 0 {
		t.Errorf("guard failure should fail open, got %v", blocked)
	}
}

// guard_except снимает блокировку по решению founder'а.
func TestApplyGuardExcept_Override(t *testing.T) {
	blocked := map[string]string{"evil": "injection", "evil2": "exfil"}
	out := applyGuardExcept(blocked, []string{"evil"})
	if out["evil"] {
		t.Error("guard_except should unblock evil")
	}
	if !out["evil2"] {
		t.Error("evil2 should stay blocked")
	}
}

// Блокировка → уведомление founder'а через ManagerSender.
func TestNotifyManagerGuardBlock(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()
	cfg := &config.Config{}
	cfg.Health.Reporting.ManagerChannel = "telegram"
	cfg.Health.Reporting.ManagerChatID = "42"
	al := &AgentLoop{bus: mb, cfg: cfg}

	al.notifyManagerGuardBlock("github", []string{"• evil — injection"})

	select {
	case msg := <-mb.OutboundChan():
		if !strings.Contains(msg.Content, "github") ||
			!strings.Contains(msg.Content, "guard_except") ||
			!strings.Contains(msg.Content, "evil") {
			t.Errorf("unexpected notification: %q", msg.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no notification sent to manager")
	}
}
