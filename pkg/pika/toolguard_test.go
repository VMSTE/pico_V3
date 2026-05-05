package pika

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// mockPlanGetter implements ActivePlanGetter for tests.
type mockPlanGetter struct {
	plan string
}

func (m *mockPlanGetter) GetActivePlan() string {
	return m.plan
}

// --- Test: activePlan + text without tools → modify ---
func TestToolGuard_ActivePlanTextNoTools_Modify(t *testing.T) {
	pg := &mockPlanGetter{plan: "Step 1: run diagnostics"}
	tg := ToolGuardFactory(nil, pg)

	resp := &agent.LLMHookResponse{
		Response: &providers.LLMResponse{
			Content: "I'll help you with that.",
		},
	}

	_, decision, err := tg.AfterLLM(
		context.Background(), resp,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Action != agent.HookActionModify {
		t.Errorf(
			"action = %q, want %q",
			decision.Action, agent.HookActionModify,
		)
	}
	if decision.Reason == "" {
		t.Error("reason should not be empty on modify")
	}
}

// --- Test: activePlan + tool calls → continue ---
func TestToolGuard_ActivePlanWithToolCalls_Continue(
	t *testing.T,
) {
	pg := &mockPlanGetter{plan: "Step 1: run diagnostics"}
	tg := ToolGuardFactory(nil, pg)

	resp := &agent.LLMHookResponse{
		Response: &providers.LLMResponse{
			Content: "",
			ToolCalls: []providers.ToolCall{
				{ID: "call_1", Name: "exec"},
			},
		},
	}

	_, decision, err := tg.AfterLLM(
		context.Background(), resp,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Action != agent.HookActionContinue {
		t.Errorf("action = %q, want continue", decision.Action)
	}
}

// --- Test: no plan + text without tools → continue ---
func TestToolGuard_NoPlan_Continue(t *testing.T) {
	pg := &mockPlanGetter{plan: ""}
	tg := ToolGuardFactory(nil, pg)

	resp := &agent.LLMHookResponse{
		Response: &providers.LLMResponse{
			Content: "I'll help you with that.",
		},
	}

	_, decision, err := tg.AfterLLM(
		context.Background(), resp,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Action != agent.HookActionContinue {
		t.Errorf("action = %q, want continue", decision.Action)
	}
}

// --- Test: retry exhausted → continue ---
func TestToolGuard_RetryExhausted_Continue(t *testing.T) {
	pg := &mockPlanGetter{plan: "Step 1: run diagnostics"}
	tg := ToolGuardFactory(nil, pg)

	resp := &agent.LLMHookResponse{
		Response: &providers.LLMResponse{
			Content: "Here is the result.",
		},
	}

	// First call — should modify (retry #1)
	_, d1, err := tg.AfterLLM(
		context.Background(), resp,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d1.Action != agent.HookActionModify {
		t.Fatalf(
			"first call: action = %q, want modify",
			d1.Action,
		)
	}

	// Second call — retry exhausted, should continue
	_, d2, err := tg.AfterLLM(
		context.Background(), resp,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d2.Action != agent.HookActionContinue {
		t.Errorf(
			"second call: action = %q, want continue",
			d2.Action,
		)
	}
}

// --- Test: empty response content → continue ---
func TestToolGuard_EmptyResponse_Continue(t *testing.T) {
	pg := &mockPlanGetter{plan: "Step 1: run diagnostics"}
	tg := ToolGuardFactory(nil, pg)

	resp := &agent.LLMHookResponse{
		Response: &providers.LLMResponse{
			Content: "",
		},
	}

	_, decision, err := tg.AfterLLM(
		context.Background(), resp,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Action != agent.HookActionContinue {
		t.Errorf("action = %q, want continue", decision.Action)
	}
}

// --- Test: nil planGetter → continue (guard) ---
func TestToolGuard_NilPlanGetter_Continue(t *testing.T) {
	tg := ToolGuardFactory(nil, nil)

	resp := &agent.LLMHookResponse{
		Response: &providers.LLMResponse{
			Content: "Some text without tools.",
		},
	}

	_, decision, err := tg.AfterLLM(
		context.Background(), resp,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Action != agent.HookActionContinue {
		t.Errorf("action = %q, want continue", decision.Action)
	}
}

// --- Test: BeforeLLM is a no-op ---
func TestToolGuard_BeforeLLM_NoOp(t *testing.T) {
	pg := &mockPlanGetter{plan: "some plan"}
	tg := ToolGuardFactory(nil, pg)

	req := &agent.LLMHookRequest{}
	out, decision, err := tg.BeforeLLM(
		context.Background(), req,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if decision.Action != agent.HookActionContinue {
		t.Errorf("action = %q, want continue", decision.Action)
	}
	if out != req {
		t.Error("BeforeLLM should return the same request")
	}
}

// --- Test: ResetTurn re-enables retry ---
func TestToolGuard_ResetTurn(t *testing.T) {
	pg := &mockPlanGetter{plan: "Step 1: do something"}
	tg := ToolGuardFactory(nil, pg)

	resp := &agent.LLMHookResponse{
		Response: &providers.LLMResponse{
			Content: "text without tools",
		},
	}

	// First call — modify
	_, d1, _ := tg.AfterLLM(
		context.Background(), resp,
	)
	if d1.Action != agent.HookActionModify {
		t.Fatalf(
			"first: action = %q, want modify", d1.Action,
		)
	}

	// Second call — exhausted → continue
	_, d2, _ := tg.AfterLLM(
		context.Background(), resp,
	)
	if d2.Action != agent.HookActionContinue {
		t.Fatalf(
			"exhausted: action = %q, want continue",
			d2.Action,
		)
	}

	// Reset turn
	tg.ResetTurn()

	// After reset — should modify again
	_, d3, _ := tg.AfterLLM(
		context.Background(), resp,
	)
	if d3.Action != agent.HookActionModify {
		t.Errorf(
			"after reset: action = %q, want modify",
			d3.Action,
		)
	}
}
