package pika

import (
	"strings"
	"testing"
)

func newTestOutputGate(maxChars, maxRetries int) *OutputGate {
	return &OutputGate{
		maxChars:   maxChars,
		maxRetries: maxRetries,
	}
}

func TestOutputGate_NilResponse(t *testing.T) {
	og := newTestOutputGate(100, 3)
	d := og.AfterLLM(nil)
	if d.Action != HookActionContinue {
		t.Errorf("expected continue, got %s", d.Action)
	}
}

func TestOutputGate_Disabled(t *testing.T) {
	og := newTestOutputGate(0, 3)
	resp := &OutputGateLLMResponse{Content: strings.Repeat("x", 5000)}
	d := og.AfterLLM(resp)
	if d.Action != HookActionContinue {
		t.Errorf("expected continue when disabled, got %s", d.Action)
	}
}

func TestOutputGate_WithinLimit(t *testing.T) {
	og := newTestOutputGate(100, 3)
	resp := &OutputGateLLMResponse{Content: "short"}
	d := og.AfterLLM(resp)
	if d.Action != HookActionContinue {
		t.Errorf("expected continue, got %s", d.Action)
	}
}

func TestOutputGate_ExceedsLimit_Modify(t *testing.T) {
	og := newTestOutputGate(10, 3)
	resp := &OutputGateLLMResponse{Content: strings.Repeat("x", 50)}
	d := og.AfterLLM(resp)
	if d.Action != HookActionModify {
		t.Errorf("expected modify, got %s", d.Action)
	}
	if d.Reason == "" {
		t.Error("expected non-empty reason")
	}
}

func TestOutputGate_RetryExhausted(t *testing.T) {
	og := newTestOutputGate(10, 2)
	resp := &OutputGateLLMResponse{Content: strings.Repeat("x", 50)}

	// First retry
	d := og.AfterLLM(resp)
	if d.Action != HookActionModify {
		t.Fatalf("retry 1: expected modify, got %s", d.Action)
	}

	// Second retry
	d = og.AfterLLM(resp)
	if d.Action != HookActionModify {
		t.Fatalf("retry 2: expected modify, got %s", d.Action)
	}

	// Third call: exhausted, should continue
	d = og.AfterLLM(resp)
	if d.Action != HookActionContinue {
		t.Errorf("retry exhausted: expected continue, got %s", d.Action)
	}
}

func TestOutputGate_ResetTurn(t *testing.T) {
	og := newTestOutputGate(10, 1)
	resp := &OutputGateLLMResponse{Content: strings.Repeat("x", 50)}

	// Use up the retry
	og.AfterLLM(resp)

	// Now exhausted
	d := og.AfterLLM(resp)
	if d.Action != HookActionContinue {
		t.Fatalf("expected continue after exhaustion")
	}

	// Reset and try again
	og.ResetTurn()
	d = og.AfterLLM(resp)
	if d.Action != HookActionModify {
		t.Errorf("expected modify after reset, got %s", d.Action)
	}
}
