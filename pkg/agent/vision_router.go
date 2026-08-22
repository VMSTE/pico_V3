package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/pika"
	"github.com/sipeed/picoclaw/pkg/providers"
)

const visionDistillateSystemPrompt = `Ты — vision-модуль системы. Твоя задача: описать изображение максимально точно и плотно для текстовой модели, которая продолжит диалог с пользователем. Переписывай весь читаемый текст, перечисляй элементы интерфейса, таблицы, числа, состояния. Не фантазируй: если что-то не читается — прямо скажи «не читается». Ответ — только описание, без приветствий.`

// routeMediaToVision implements D-AUDIT-124 slice 4: when the main model has
// no vision, inbound images are described by the background (vision-capable)
// satellite and the distillate replaces media in the request. On any failure
// an explicit marker is inserted so the main model states the limitation
// instead of inventing image content.
func (p *Pipeline) routeMediaToVision(ctx context.Context, ts *turnState, exec *turnExecution) {
	if !hasMediaRefs(exec.messages) {
		return
	}
	if agentModelSupportsVision(p.Cfg, ts.agent.Model) {
		return
	}

	items := collectImageDataURLs(exec.messages)
	if len(items) == 0 {
		logger.WarnCF(
			"agent",
			"non-image media dropped: main model has no vision and satellite handles images only",
			map[string]any{"agent_id": ts.agent.ID},
		)
		exec.messages = stripMessageMedia(exec.messages)
		exec.callMessages = exec.messages
		return
	}

	var text string
	provider := resolveArchivistProvider(p.Cfg)
	if provider == nil {
		text = "[Изображение не распознано: vision-спутник не настроен (нет модели background)]"
	} else {
		model := resolveSatelliteModelID(p.Cfg, "background")
		// D-AUDIT-125 (волна 102): телеметрия vision-спутника в request_log
		// (component=vision) — паттерн archivarius/atomizer/reflexor.
		llmStart := time.Now()
		d, resp, err := distillateImages(ctx, provider, model, items)
		if err != nil {
			pika.RecordSatelliteLLMFailure(
				ctx, p.al.botmem, "vision", "describe",
				ts.sessionKey, model, err, llmStart,
			)
			logger.WarnCF(
				"agent",
				"vision satellite failed",
				map[string]any{"error": err.Error(), "agent_id": ts.agent.ID},
			)
			text = "[Изображение не распознано: ошибка vision-спутника]"
		} else {
			pika.RecordSatelliteLLM(
				ctx, p.al.botmem, "vision", "describe",
				ts.sessionKey, model, resp, llmStart,
			)
			text = d
		}
	}

	exec.messages = amendMessagesWithDistillate(exec.messages, text)
	exec.callMessages = exec.messages
	logger.InfoCF(
		"agent",
		"media routed to vision satellite",
		map[string]any{"agent_id": ts.agent.ID, "images": len(items)},
	)
}

func agentModelSupportsVision(cfg *config.Config, modelName string) bool {
	if cfg == nil {
		return false
	}
	mc, err := cfg.GetModelConfig(modelName)
	if err != nil || mc == nil {
		return false
	}
	return mc.SupportsVision()
}

// collectImageDataURLs returns inline image data URLs across messages.
// media:// refs are out of scope for slice 4 (pico inbound sends data URLs).
func collectImageDataURLs(messages []providers.Message) []string {
	var out []string
	for _, m := range messages {
		for _, ref := range m.Media {
			if strings.HasPrefix(ref, "data:image/") {
				out = append(out, ref)
			}
		}
	}
	return out
}

// amendMessagesWithDistillate appends the satellite description to every
// message that carried media and drops the media payloads.
func amendMessagesWithDistillate(messages []providers.Message, text string) []providers.Message {
	out := make([]providers.Message, len(messages))
	for i, m := range messages {
		out[i] = m
		if len(m.Media) == 0 {
			continue
		}
		m.Content = strings.TrimSpace(
			m.Content,
		) + "\n[Изображение, распознанное vision-спутником: " + text + "]"
		m.Media = nil
		out[i] = m
	}
	return out
}

// Волна 102: resp возвращаем для телеметрии (токены + latency в request_log).
func distillateImages(
	ctx context.Context,
	provider providers.LLMProvider,
	model string,
	items []string,
) (string, *providers.LLMResponse, error) {
	msgs := []providers.Message{
		{Role: "system", Content: visionDistillateSystemPrompt},
		{Role: "user", Content: "Опиши изображение.", Media: items},
	}
	resp, err := provider.Chat(ctx, msgs, nil, model, nil)
	if err != nil {
		return "", nil, err
	}
	out := strings.TrimSpace(resp.Content)
	if out == "" {
		return "", resp, fmt.Errorf("empty distillate")
	}
	return out, resp, nil
}
