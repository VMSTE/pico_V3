package pika

import (
	"context"

	"github.com/sipeed/picoclaw/pkg/bus"
)

// BusSender adapts MessageBus to TelegramSender interface.
// PIKA-V3: Universal sender — works with any connected messenger (TZ-v2-8i).
type BusSender struct {
	MB      *bus.MessageBus
	Channel string // target channel name (e.g. "telegram", "discord")
	ChatID  string // target chat/conversation ID
}

func (s *BusSender) SendMessage(ctx context.Context, text string) (string, error) {
	err := s.MB.PublishOutbound(ctx, bus.OutboundMessage{
		Channel: s.Channel,
		ChatID:  s.ChatID,
		Content: text,
	})
	return "", err
}

func (s *BusSender) EditMessage(_ context.Context, _, _ string) error   { return nil }
func (s *BusSender) DeleteMessage(_ context.Context, _ string) error    { return nil }
func (s *BusSender) SendConfirmation(_ context.Context, _ string) (bool, error) {
	return false, nil
}
