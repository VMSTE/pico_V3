// PIKA-V3: ProgressObserver — side-channel progress in Telegram + degradation/recovery alerts.
// Implements EventObserver hook for pipeline events and ProgressNotifier for direct alerts.
// 0 LLM tokens.
//
// NOTE: Event types (EventKind, Event, payloads) are defined locally to avoid
// an import cycle with pkg/agent. The registration adapter in instance.go
// (ТЗ-4a) converts agent.Event → pika.Event before calling OnEvent.

package pika

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// ---------------------------------------------------------------------------
// Local event types (mirror pkg/agent event types to avoid import cycle)
// ---------------------------------------------------------------------------

// ProgressEventKind identifies a pipeline event relevant to progress reporting.
type ProgressEventKind uint8

const (
	ProgressEventToolExecStart ProgressEventKind = iota
	ProgressEventToolExecEnd
	ProgressEventTurnEnd
)

// ProgressEvent is the envelope passed to ProgressObserver.OnEvent.
type ProgressEvent struct {
	Kind    ProgressEventKind
	Payload any
}

// ToolExecStartPayload mirrors agent.ToolExecStartPayload.
type ToolExecStartPayload struct {
	Tool      string
	Arguments map[string]any
}

// ToolExecEndPayload mirrors agent.ToolExecEndPayload.
type ToolExecEndPayload struct {
	Tool     string
	Duration time.Duration
}

// TurnEndPayload mirrors agent.TurnEndPayload.
type TurnEndPayload struct{}

// ProgressEventObserver is the local interface for observing pipeline events.
// Mirrors agent.EventObserver signature with local ProgressEvent type.
type ProgressEventObserver interface {
	OnEvent(ctx context.Context, evt ProgressEvent) error
}

// ---------------------------------------------------------------------------
// TelegramSender — shared interface for progress, clarify, confirm_gate
// ---------------------------------------------------------------------------

// TelegramSender abstracts Telegram message operations.
type TelegramSender interface {
	SendMessage(ctx context.Context, text string) (messageID string, err error)
	EditMessage(ctx context.Context, messageID string, text string) error
	DeleteMessage(ctx context.Context, messageID string) error
	SendConfirmation(ctx context.Context, text string) (approved bool, err error)
}

// ---------------------------------------------------------------------------
// Compile-time interface checks
// ---------------------------------------------------------------------------

var _ ProgressEventObserver = (*ProgressObserver)(nil)
var _ ProgressNotifier = (*ProgressObserver)(nil)

// ---------------------------------------------------------------------------
// ProgressObserver
// ---------------------------------------------------------------------------

// ProgressObserver serves as both an EventObserver hook for pipeline events
// (tool started/completed → Telegram progress) and a direct caller for
// degradation/recovery alerts.
type ProgressObserver struct {
	mu              sync.Mutex
	sender          TelegramSender
	throttleSec     int
	deleteOnDone    bool
	showStepText    bool
	lastSendAt      time.Time
	activeMessageID string
	lastAlertAt     map[string]time.Time
}

// ProgressObserverFactory creates a ProgressObserver from config.
func ProgressObserverFactory(cfg *config.Config, sender TelegramSender) *ProgressObserver {
	return &ProgressObserver{
		sender:       sender,
		throttleSec:  cfg.Health.Progress.ThrottleSec,
		deleteOnDone: cfg.Health.Progress.DeleteOnComplete,
		showStepText: cfg.Health.Progress.ShowStepText,
		lastAlertAt:  make(map[string]time.Time),
	}
}

// ---------------------------------------------------------------------------
// ProgressEventObserver implementation
// ---------------------------------------------------------------------------

// OnEvent handles pipeline events for progress reporting.
func (po *ProgressObserver) OnEvent(ctx context.Context, evt ProgressEvent) error {
	switch evt.Kind {
	case ProgressEventToolExecStart:
		p, ok := evt.Payload.(ToolExecStartPayload)
		if !ok {
			return nil
		}
		text := fmt.Sprintf("⏳ %s...", p.Tool)
		po.sendOrUpdate(ctx, text)

	case ProgressEventToolExecEnd:
		p, ok := evt.Payload.(ToolExecEndPayload)
		if !ok {
			return nil
		}
		ms := p.Duration.Milliseconds()
		text := fmt.Sprintf("✅ %s (%dms)", p.Tool, ms)
		po.sendOrUpdate(ctx, text)

	case ProgressEventTurnEnd:
		po.mu.Lock()
		defer po.mu.Unlock()
		if po.deleteOnDone && po.activeMessageID != "" {
			if err := po.sender.DeleteMessage(ctx, po.activeMessageID); err != nil {
				logger.WarnCF("pika/progress", "DeleteMessage failed", map[string]any{
					"error": err.Error(),
				})
			}
			po.activeMessageID = ""
		}
	}

	return nil
}

// sendOrUpdate sends a new progress message or edits the active one.
// Respects throttle_sec — skips update if last send was too recent.
func (po *ProgressObserver) sendOrUpdate(ctx context.Context, text string) {
	po.mu.Lock()
	defer po.mu.Unlock()

	now := time.Now()
	if po.isThrottledLocked(now) {
		return
	}

	if po.activeMessageID == "" {
		// First message: send new.
		msgID, err := po.sender.SendMessage(ctx, text)
		if err != nil {
			logger.WarnCF("pika/progress", "SendMessage failed", map[string]any{
				"error": err.Error(),
			})
			return
		}
		po.activeMessageID = msgID
	} else {
		// Subsequent: edit existing.
		if err := po.sender.EditMessage(ctx, po.activeMessageID, text); err != nil {
			logger.WarnCF("pika/progress", "EditMessage failed", map[string]any{
				"error": err.Error(),
			})
			return
		}
	}

	po.lastSendAt = now
}

// isThrottledLocked checks if enough time has passed since lastSendAt.
// Caller must hold po.mu.
func (po *ProgressObserver) isThrottledLocked(now time.Time) bool {
	if po.throttleSec <= 0 {
		return false
	}
	return now.Sub(po.lastSendAt) < time.Duration(po.throttleSec)*time.Second
}

// ---------------------------------------------------------------------------
// ProgressNotifier implementation (degradation/recovery alerts)
// ---------------------------------------------------------------------------

// NotifyDegradation sends a degradation alert to Telegram.
// Throttled: max 1 alert per component per 5 minutes.
func (po *ProgressObserver) NotifyDegradation(component, status string) {
	po.mu.Lock()
	defer po.mu.Unlock()

	now := time.Now()
	if last, ok := po.lastAlertAt[component]; ok {
		if now.Sub(last) < 5*time.Minute {
			return // throttled
		}
	}

	var msg string
	if status == "degraded" {
		msg = fmt.Sprintf("⚠️ %s деградирован. Работаю с ограничениями.", component)
	} else {
		msg = fmt.Sprintf("🔴 %s недоступен. Функциональность отключена.", component)
	}

	// Fire-and-forget with background context.
	if _, err := po.sender.SendMessage(context.Background(), msg); err != nil {
		logger.WarnCF("pika/progress", "NotifyDegradation SendMessage failed", map[string]any{
			"component": component,
			"error":     err.Error(),
		})
		return
	}

	po.lastAlertAt[component] = now
}

// NotifyRecovery sends a recovery notification to Telegram.
// No throttling — important to see immediately.
func (po *ProgressObserver) NotifyRecovery(component string) {
	po.mu.Lock()
	defer po.mu.Unlock()

	msg := fmt.Sprintf("✅ %s восстановлен. Работаю в нормальном режиме.", component)

	if _, err := po.sender.SendMessage(context.Background(), msg); err != nil {
		logger.WarnCF("pika/progress", "NotifyRecovery SendMessage failed", map[string]any{
			"component": component,
			"error":     err.Error(),
		})
		return
	}

	delete(po.lastAlertAt, component)
}
