package pika

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

func TestNewManagerSender_NotConfigured(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()
	if s := NewManagerSender(mb, &config.Config{}); s != nil {
		t.Error("expected nil sender when manager address not configured")
	}
	cfg := &config.Config{}
	cfg.Health.Reporting.ManagerChannel = "telegram"
	cfg.Health.Reporting.ManagerChatID = "42"
	if s := NewManagerSender(nil, cfg); s != nil {
		t.Error("expected nil sender when bus is nil")
	}
}

func TestNewManagerSender_Configured(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()
	cfg := &config.Config{}
	cfg.Health.Reporting.ManagerChannel = "telegram"
	cfg.Health.Reporting.ManagerChatID = "42"
	s := NewManagerSender(mb, cfg)
	if s == nil {
		t.Fatal("expected sender")
	}
	bs, ok := s.(*BusSender)
	if !ok {
		t.Fatalf("expected *BusSender, got %T", s)
	}
	if bs.Channel != "telegram" || bs.ChatID != "42" {
		t.Errorf("wrong target: %s/%s", bs.Channel, bs.ChatID)
	}
}
