package pika

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

// --- Test mocks ---

// mockConfirmSender implements TelegramSender for testing.
// Implements all 4 methods; only SendConfirmation is exercised
// by ConfirmGate. The other 3 are stubs to satisfy the interface.
type mockConfirmSender struct {
	approved bool
	err      error
	called   bool
	lastMsg  string
}

func (m *mockConfirmSender) SendMessage(
	_ context.Context, text string,
) (string, error) {
	return "mock-msg-id", nil
}

func (m *mockConfirmSender) EditMessage(
	_ context.Context, _ string, _ string,
) error {
	return nil
}

func (m *mockConfirmSender) DeleteMessage(
	_ context.Context, _ string,
) error {
	return nil
}

func (m *mockConfirmSender) SendConfirmation(
	ctx context.Context, msg string,
) (bool, error) {
	m.called = true
	m.lastMsg = msg

	// Respect context cancellation
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}

	if m.err != nil {
		return false, m.err
	}
	return m.approved, nil
}

// mockHealthState implements SystemStateProvider for testing.
type mockHealthState struct {
	state SystemState
}

func (m *mockHealthState) GetSystemState() SystemState {
	return m.state
}

// --- Helpers ---

func testDangerousOpsConfig() *config.Config {
	return &config.Config{
		Security: config.SecurityConfig{
			DangerousOps: config.DangerousOpsConfig{
				Ops: map[string]config.DangerousOpEntry{
					"deploy.request": {
						Level:   "critical",
						Confirm: config.ConfirmAlways,
					},
					"deploy.rollback": {
						Level:   "critical",
						Confirm: config.ConfirmAlways,
					},
					"compose.up": {
						Level:   "high",
						Confirm: config.ConfirmAlways,
					},
					"compose.down": {
						Level:   "high",
						Confirm: config.ConfirmAlways,
					},
					"compose.restart": {
						Level:   "medium",
						Confirm: config.ConfirmIfHealthy,
					},
					"git.commit_and_push": {
						Level:   "high",
						Confirm: config.ConfirmAlways,
					},
					"files.write": {
						Level:   "medium",
						Confirm: config.ConfirmIfCritical,
					},
				},
				ConfirmTimeoutMin: 30,
				CriticalPaths: []string{
					"/workspace/prompt/*",
					"/opt/infra/*",
				},
			},
		},
	}
}

// --- Tests (matching ТЗ-v2-4d criteria) ---

// deploy.request → confirm запрошен, approved=true → ApprovalDecision{Approved: true}
func TestConfirmGate_DeployRequest_Approved(t *testing.T) {
	sender := &mockConfirmSender{approved: true}
	health := &mockHealthState{state: StateHealthy}
	cg := ConfirmGateFactory(testDangerousOpsConfig(), sender, health)

	decision, err := cg.ApproveTool(
		context.Background(),
		&ConfirmApprovalRequest{
			Tool:      "deploy",
			Arguments: map[string]any{"operation": "request"},
		},
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Approved {
		t.Error("expected Approved=true when manager approves deploy.request")
	}
	if !sender.called {
		t.Error("expected Telegram confirmation to be sent")
	}
}

// deploy.request → approved=false → ApprovalDecision{Approved: false}
func TestConfirmGate_DeployRequest_Denied(t *testing.T) {
	sender := &mockConfirmSender{approved: false}
	health := &mockHealthState{state: StateHealthy}
	cg := ConfirmGateFactory(testDangerousOpsConfig(), sender, health)

	decision, err := cg.ApproveTool(
		context.Background(),
		&ConfirmApprovalRequest{
			Tool:      "deploy",
			Arguments: map[string]any{"operation": "request"},
		},
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Approved {
		t.Error("expected Approved=false when manager denies deploy.request")
	}
	if decision.Reason != "менеджер отклонил" {
		t.Errorf("expected reason 'менеджер отклонил', got %q", decision.Reason)
	}
}

// compose.restart + exited → allow без Telegram
func TestConfirmGate_ComposeRestart_Exited(t *testing.T) {
	sender := &mockConfirmSender{approved: false}
	health := &mockHealthState{state: StateHealthy}
	cg := ConfirmGateFactory(testDangerousOpsConfig(), sender, health)

	decision, err := cg.ApproveTool(
		context.Background(),
		&ConfirmApprovalRequest{
			Tool: "compose",
			Arguments: map[string]any{
				"operation": "restart",
				"state":     "exited",
			},
		},
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Approved {
		t.Error("expected Approved=true for compose.restart + exited (reflex)")
	}
	if sender.called {
		t.Error("expected NO Telegram call for exited reflex")
	}
}

// compose.restart + healthy → confirm
func TestConfirmGate_ComposeRestart_Healthy(t *testing.T) {
	sender := &mockConfirmSender{approved: true}
	health := &mockHealthState{state: StateHealthy}
	cg := ConfirmGateFactory(testDangerousOpsConfig(), sender, health)

	decision, err := cg.ApproveTool(
		context.Background(),
		&ConfirmApprovalRequest{
			Tool:      "compose",
			Arguments: map[string]any{"operation": "restart"},
		},
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Approved {
		t.Error("expected Approved=true for compose.restart + healthy + approved")
	}
	if !sender.called {
		t.Error("expected Telegram confirmation for healthy system")
	}
}

// compose.restart + degraded → allow
func TestConfirmGate_ComposeRestart_Degraded(t *testing.T) {
	sender := &mockConfirmSender{approved: false}
	health := &mockHealthState{state: StateDegraded}
	cg := ConfirmGateFactory(testDangerousOpsConfig(), sender, health)

	decision, err := cg.ApproveTool(
		context.Background(),
		&ConfirmApprovalRequest{
			Tool:      "compose",
			Arguments: map[string]any{"operation": "restart"},
		},
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Approved {
		t.Error("expected Approved=true for compose.restart + degraded (emergency fix)")
	}
	if sender.called {
		t.Error("expected NO Telegram call for degraded system")
	}
}

// files.write critical path → confirm
func TestConfirmGate_FilesWrite_CriticalPath(t *testing.T) {
	sender := &mockConfirmSender{approved: true}
	health := &mockHealthState{state: StateHealthy}
	cg := ConfirmGateFactory(testDangerousOpsConfig(), sender, health)

	decision, err := cg.ApproveTool(
		context.Background(),
		&ConfirmApprovalRequest{
			Tool: "files",
			Arguments: map[string]any{
				"operation": "write",
				"path":      "/workspace/prompt/CORE.md",
			},
		},
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Approved {
		t.Error("expected Approved=true for files.write critical path + approved")
	}
	if !sender.called {
		t.Error("expected Telegram confirmation for critical path")
	}
}

// files.write non-critical → allow
func TestConfirmGate_FilesWrite_NonCritical(t *testing.T) {
	sender := &mockConfirmSender{approved: false}
	health := &mockHealthState{state: StateHealthy}
	cg := ConfirmGateFactory(testDangerousOpsConfig(), sender, health)

	decision, err := cg.ApproveTool(
		context.Background(),
		&ConfirmApprovalRequest{
			Tool: "files",
			Arguments: map[string]any{
				"operation": "write",
				"path":      "/tmp/test.txt",
			},
		},
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Approved {
		t.Error("expected Approved=true for files.write non-critical path")
	}
	if sender.called {
		t.Error("expected NO Telegram call for non-critical path")
	}
}

// sandbox.run (не в таблице) → allow
func TestConfirmGate_NotInTable(t *testing.T) {
	sender := &mockConfirmSender{approved: false}
	health := &mockHealthState{state: StateHealthy}
	cg := ConfirmGateFactory(testDangerousOpsConfig(), sender, health)

	decision, err := cg.ApproveTool(
		context.Background(),
		&ConfirmApprovalRequest{
			Tool:      "sandbox",
			Arguments: map[string]any{"operation": "run"},
		},
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !decision.Approved {
		t.Error("expected Approved=true for tool not in dangerous_ops table")
	}
	if sender.called {
		t.Error("expected NO Telegram call for unknown tool")
	}
}

// timeout → deny (fail-closed)
func TestConfirmGate_Timeout_Deny(t *testing.T) {
	sender := &mockConfirmSender{
		err: errors.New("timeout waiting for reply (30 min)"),
	}
	health := &mockHealthState{state: StateHealthy}
	cg := ConfirmGateFactory(testDangerousOpsConfig(), sender, health)

	decision, err := cg.ApproveTool(
		context.Background(),
		&ConfirmApprovalRequest{
			Tool:      "deploy",
			Arguments: map[string]any{"operation": "request"},
		},
	)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Approved {
		t.Error("expected Approved=false on timeout (fail-closed)")
	}
	if !strings.Contains(decision.Reason, "confirmation error") {
		t.Errorf("expected reason with 'confirmation error', got %q",
			decision.Reason)
	}
}
