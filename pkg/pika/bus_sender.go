package pika

import (
	"context"
	"strings"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

// OutboundPublisher — минимум, который BusSender требует от шины сообщений.
// Удовлетворяют и *bus.MessageBus, и interfaces.MessageBus (Р-3).
type OutboundPublisher interface {
	PublishOutbound(ctx context.Context, msg bus.OutboundMessage) error
}

// BusSender adapts MessageBus to TelegramSender interface.
// PIKA-V3: Universal sender — works with any connected messenger (TZ-v2-8i).
type BusSender struct {
	MB      OutboundPublisher
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

func (s *BusSender) EditMessage(_ context.Context, _, _ string) error { return nil }
func (s *BusSender) DeleteMessage(_ context.Context, _ string) error  { return nil }
func (s *BusSender) SendConfirmation(_ context.Context, _ string) (bool, error) {
	return false, nil
}

// NewManagerSender возвращает отправителя в чат менеджера для системных
// отчётов (диагностика, аналитика, прогресс). Адрес — поля конфига
// health.reporting.manager_channel / manager_chat_id.
// Адрес не настроен или шина отсутствует → nil (отчёты молча отключены,
// все потребители TelegramSender nil-safe).
func NewManagerSender(mb OutboundPublisher, cfg *config.Config) TelegramSender {
	if mb == nil || cfg == nil {
		return nil
	}
	channel := strings.TrimSpace(cfg.Health.Reporting.ManagerChannel)
	chatID := strings.TrimSpace(cfg.Health.Reporting.ManagerChatID)
	if channel == "" || chatID == "" {
		return nil
	}
	return &BusSender{MB: mb, Channel: channel, ChatID: chatID}
}
