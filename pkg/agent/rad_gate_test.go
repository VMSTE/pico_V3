package agent

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/pika"
)

func TestRadPreActionGate_NilRAD(t *testing.T) {
	// nil AgentLoop — should pass through.
	blocked, reason := radPreActionGate(context.Background(), nil, "", "test_tool")
	if blocked {
		t.Errorf("expected pass-through with nil al, got blocked: %s", reason)
	}

	// AgentLoop with nil rad — should pass through.
	al := &AgentLoop{}
	blocked, reason = radPreActionGate(context.Background(), al, "", "test_tool")
	if blocked {
		t.Errorf("expected pass-through with nil rad, got blocked: %s", reason)
	}
}

func TestRadPreActionGate_SafeTool(t *testing.T) {
	cfg := pika.DefaultRADConfig()
	rad := pika.NewRAD(cfg)
	al := &AgentLoop{rad: rad}

	blocked, _ := radPreActionGate(context.Background(), al, "sess1", "get_weather")
	if blocked {
		t.Error("safe tool 'get_weather' should not be blocked")
	}
}

func TestRadPreActionGate_WithBotmem(t *testing.T) {
	cfg := pika.DefaultRADConfig()
	rad := pika.NewRAD(cfg)
	al := &AgentLoop{rad: rad, botmem: nil}

	// nil botmem — should not panic, just skip reasoning.
	blocked, _ := radPreActionGate(context.Background(), al, "sess1", "read_file")
	if blocked {
		t.Error("read_file should not be blocked without reasoning")
	}
}
