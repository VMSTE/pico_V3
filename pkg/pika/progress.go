// PIKA-V3: Progress Notifications — side-channel progress in Telegram
// + degradation/recovery alerts. 0 LLM tokens.
// EventObserver hook for pipeline events + direct caller for telemetry.
// ТЗ-v2-4e.

package pika

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// TelegramSender is the interface for sending/editing/deleting
// Telegram messages. Shared across progress.go, clarify.go (ТЗ-3d),
// and confirm_gate.go (ТЗ-4d).
type TelegramSender interface {
	SendMessage(ctx context.Context, text string) (messageID string, err error)
	EditMessage(ctx context.Context, messageID string, text string) error
	DeleteMessage(ctx context.Context, messageID string) error
	SendConfirmation(ctx context.Context, text string) (approved bool, err error)
}

// ProgressObserver implements agent.EventObserver for pipeline
// progress notifications and ProgressNotifier (telemetry.go) for
// degradation/recovery alerts. 0 LLM tokens.
type ProgressObserver struct {
	sender       TelegramSender
	throttleSec  int
	deleteOnDone bool
	showStepText bool

	mu              sync.Mutex
	lastSendAt      time.Time
	activeMessageID string
	lastAlertAt     map[string]time.Time
}

// Compile-time checks.
var (
	_ agent.EventObserver = (*ProgressObserver)(nil)
	_ ProgressNotifier    = (*ProgressObserver)(nil)
)

// ProgressObserverFactory creates a ProgressObserver from config.
func ProgressObserverFactory(cfg *config.Config, sender TelegramSender) *ProgressObserver {
	throttle := cfg.Health.Progress.ThrottleSec
	if throttle <= 0 {
		throttle = 2
	}
	return &ProgressObserver{
		sender:       sender,
		throttleSec:  throttle,
		deleteOnDone: cfg.Health.Progress.DeleteOnComplete,
		showStepText: cfg.Health.Progress.ShowStepText,
		lastAlertAt:  make(map[string]time.Time),
	}
}

// OnEvent handles pipeline events for Telegram progress updates.
// Implements agent.EventObserver.
func (po *ProgressObserver) OnEvent(ctx context.Context, evt agent.Event) error {
	switch evt.Kind {
	case agent.EventKindToolExecStart:
		p, ok := evt.Payload.(agent.ToolExecStartPayload)
		if !ok {
			return nil
		}
		if po.showStepText {
			po.sendOrUpdate(ctx, fmt.Sprintf("⏳ %s...", p.Tool))
		}

	case agent.EventKindToolExecEnd:
		p, ok := evt.Payload.(agent.ToolExecEndPayload)
		if !ok {
			return nil
		}
		if po.showStepText {
			po.sendOrUpdate(ctx, fmt.Sprintf("✅ %s (%dms)", p.Tool, p.Duration.Milliseconds()))
		}

	case agent.EventKindTurnEnd:
		po.mu.Lock()
		mid := po.activeMessageID
		shouldDelete := po.deleteOnDone && mid != ""
		if shouldDelete {
			po.activeMessageID = ""
		}
		po.mu.Unlock()

		if shouldDelete {
			if err := po.sender.DeleteMessage(ctx, mid); err != nil {
				logger.WarnCF("pika/progress", "DeleteMessage failed", map[string]any{
					"error": err.Error(),
				})
			}
		}
	}
	return nil
}

// sendOrUpdate sends a new message or edits the active one.
// Applies throttling: skips if less than throttleSec since last send.
func (po *ProgressObserver) sendOrUpdate(ctx context.Context, text string) {
	po.mu.Lock()
	now := time.Now()
	if !po.lastSendAt.IsZero() && now.Sub(po.lastSendAt) < time.Duration(po.throttleSec)*time.Second {
		po.mu.Unlock()
		return
	}
	mid := po.activeMessageID
	po.lastSendAt = now
	po.mu.Unlock()

	if mid == "" {
		newID, err := po.sender.SendMessage(ctx, text)
		if err != nil {
			logger.WarnCF("pika/progress", "SendMessage failed", map[string]any{
				"error": err.Error(),
			})
			return
		}
		po.mu.Lock()
		po.activeMessageID = newID
		po.mu.Unlock()
	} else {
		if err := po.sender.EditMessage(ctx, mid, text); err != nil {
			logger.WarnCF("pika/progress", "EditMessage failed", map[string]any{
				"error": err.Error(),
			})
		}
	}
}

// NotifyDegradation sends a degradation alert to Telegram.
// Throttled: at most once per component per 5 minutes.
// Implements ProgressNotifier (telemetry.go).
func (po *ProgressObserver) NotifyDegradation(component, status string) {
	po.mu.Lock()
	if po.isThrottledLocked(component, 5*time.Minute) {
		po.mu.Unlock()
		return
	}
	po.lastAlertAt[component] = time.Now()
	po.mu.Unlock()

	var msg string
	if status == "degraded" {
		msg = fmt.Sprintf("⚠️ %s деградирован. Работаю с ограничениями.", component)
	} else {
		msg = fmt.Sprintf("🔴 %s недоступен. Функциональность отключена.", component)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := po.sender.SendMessage(ctx, msg); err != nil {
		logger.WarnCF("pika/progress", "NotifyDegradation SendMessage failed", map[string]any{
			"component": component,
			"error":     err.Error(),
		})
	}
}

// NotifyRecovery sends a recovery notification to Telegram.
// No throttling — recovery is important to see immediately.
// Implements ProgressNotifier (telemetry.go).
func (po *ProgressObserver) NotifyRecovery(component string) {
	po.mu.Lock()
	delete(po.lastAlertAt, component)
	po.mu.Unlock()

	msg := fmt.Sprintf("✅ %s восстановлен. Работаю в нормальном режиме.", component)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := po.sender.SendMessage(ctx, msg); err != nil {
		logger.WarnCF("pika/progress", "NotifyRecovery SendMessage failed", map[string]any{
			"component": component,
			"error":     err.Error(),
		})
	}
}

// isThrottledLocked checks if a component alert was sent recently.
// Must be called with po.mu held.
func (po *ProgressObserver) isThrottledLocked(component string, window time.Duration) bool {
	last, ok := po.lastAlertAt[component]
	if !ok {
		return false
	}
	return time.Since(last) < window
}
