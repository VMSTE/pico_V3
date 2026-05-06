package pika

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
)

func TestBusSender_ImplementsTelegramSender(t *testing.T) {
	mb := bus.NewMessageBus()
	defer mb.Close()

	var sender TelegramSender = &BusSender{
		MB:      mb,
		Channel: "test",
		ChatID:  "123",
	}

	ctx := context.Background()
	if _, err := sender.SendMessage(ctx, "hello analytics report"); err != nil {
		t.Errorf("SendMessage: %v", err)
	}
	if err := sender.EditMessage(ctx, "mid", "new text"); err != nil {
		t.Errorf("EditMessage: %v", err)
	}
	if err := sender.DeleteMessage(ctx, "mid"); err != nil {
		t.Errorf("DeleteMessage: %v", err)
	}
	ok, err := sender.SendConfirmation(ctx, "confirm?")
	if err != nil {
		t.Errorf("SendConfirmation: %v", err)
	}
	if ok {
		t.Error("SendConfirmation should return false (no-op)")
	}
}
