package pika

import (
	"context"
	"strings"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

// OutboundPublisher — минимум, который BusSender требует от шины сообщений.
// Р-5: добавлен InboundChan для живого SendConfirmation.
// Удовлетворяют и *bus.MessageBus, и interfaces.MessageBus (паттерн
// ClarifyBus, волна 3b).
type OutboundPublisher interface {
	PublishOutbound(ctx context.Context, msg bus.OutboundMessage) error
	InboundChan() <-chan bus.InboundMessage
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

// SendConfirmation отправляет вопрос менеджеру и ждёт ответа из того же
// чата (Р-5 шаг 2b, паттерн clarify.go waitForReply — проверен по коду).
// «да» → true; «нет», таймаут, закрытая шина → false (fail-closed).
// Пока гейт ждёт ответа, цикл агента заблокирован на ApproveTool —
// конкурентного читателя InboundChan нет.
func (s *BusSender) SendConfirmation(ctx context.Context, text string) (bool, error) {
	if _, err := s.SendMessage(ctx, text); err != nil {
		return false, err
	}
	inbound := s.MB.InboundChan()
	for {
		select {
		case <-ctx.Done():
			return false, nil // таймаут → отказ (fail-closed)
		case msg, ok := <-inbound:
			if !ok {
				return false, nil // шина закрыта → отказ
			}
			if msg.ChatID != s.ChatID {
				continue // чужой чат — не наш ответ
			}
			switch normalizeAnswer(msg.Content) {
			case answerYes:
				return true, nil
			case answerNo:
				return false, nil
			default:
				// сообщение не является ответом — ждём до таймаута
			}
		}
	}
}

type confirmAnswer int

const (
	answerUnknown confirmAnswer = iota
	answerYes
	answerNo
)

// normalizeAnswer распознаёт ответ менеджера (да/нет, ru/en).
func normalizeAnswer(text string) confirmAnswer {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "да", "да.", "yes", "y", "ok", "ок", "подтверждаю", "д":
		return answerYes
	case "нет", "нет.", "no", "n", "н", "отмена", "отклоняю":
		return answerNo
	}
	return answerUnknown
}

// NewManagerSender возвращает отправителя в чат менеджера для системных
// отчётов (диагностика, аналитика, прогресс) и запросов подтверждения.
// Адрес — поля конфига health.reporting.manager_channel / manager_chat_id.
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
