package agent

import (
	"context"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// D-AUDIT-69: approver, отклоняющий всё.
type denyAllApprover struct{}

func (denyAllApprover) ApproveTool(
	_ context.Context, _ *ToolApprovalRequest,
) (ApprovalDecision, error) {
	return ApprovalDecision{Approved: false, Reason: "deny-all test approver"}, nil
}

// respond на ЗАРЕГИСТРИРОВАННЫЙ тул требует одобрения: при отказе тул
// не выполняется, результат хука отбрасывается.
func TestAgentLoop_HookRespond_RegisteredToolRequiresApproval(t *testing.T) {
	provider := &multiToolProvider{
		toolCalls: []providers.ToolCall{
			{ID: "call-1", Name: "tool_one", Arguments: map[string]any{}},
		},
		finalContent: "done",
	}
	al, _, cleanup := newHookTestLoop(t, provider)
	defer cleanup()

	executed := make(chan struct{}, 1)
	al.RegisterTool(&slowTool{name: "tool_one", duration: time.Millisecond, execCh: executed})

	if err := al.MountHook(HookRegistration{
		Name: "respond", Source: HookSourceInProcess,
		Hook: &respondHook{respondTools: map[string]bool{"tool_one": true}},
	}); err != nil {
		t.Fatalf("mount respond hook: %v", err)
	}
	if err := al.MountHook(HookRegistration{
		Name: "deny-approver", Source: HookSourceInProcess, Hook: denyAllApprover{},
	}); err != nil {
		t.Fatalf("mount approver: %v", err)
	}

	resp, err := al.ProcessDirectWithChannel(
		context.Background(), "run tools", "sess-gate", "cli", "chat1",
	)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if resp != "done" {
		t.Errorf("resp = %q, want done", resp)
	}
	select {
	case <-executed:
		t.Error("registered tool must NOT execute when hook respond is denied")
	default:
	}
}
