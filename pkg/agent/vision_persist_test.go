package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

// Волна 105 (ТЗ-105): персист user-сообщения идёт ПОСЛЕ vision-роутинга —
// в БД ложится контент с дистиллятом (ищется поиском), Media в metadata
// сохраняется. История едет к модели без media: спутник дёргается один
// раз на картинку, а не на каждом ходе.
//
// background-модель указывает на httptest-сервер. Осторожно: тот же
// background провайдер использует Архивариус (memory brief при сборке
// промпта), поэтому vision-вызовы считаем по маркеру "Опиши изображение."
// в теле запроса, остальным отвечаем нейтральным пустым JSON.
// visionTestMedia — тестовая картинка (data URL), общая для кейсов волны 105.
var visionTestMedia = []string{"data:image/png;base64,abc123"}

func newVisionPersistTestLoop(
	t *testing.T, visionHits *atomic.Int32, failSatellite bool,
) (*AgentLoop, *visionUnsupportedMediaProvider) {
	t.Helper()

	satellite := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), "Опиши изображение") {
				visionHits.Add(1)
				if failSatellite {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(
					`{"choices":[{"message":{"role":"assistant","content":"скриншот настроек модели"}}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`,
				))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(
				`{"choices":[{"message":{"role":"assistant","content":"{}"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`,
			))
		},
	))
	t.Cleanup(satellite.Close)

	workspace := t.TempDir()
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspace,
				MemoryDBPath:      filepath.Join(workspace, "memory", "bot_memory.db"),
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 3,
			},
		},
		ModelList: []*config.ModelConfig{
			// vision не задан → false: картинки уходят спутнику
			{ModelName: "test-model", Model: "test/test-model"},
			{
				ModelName: "background",
				Model:     "openai/test-vision",
				APIBase:   satellite.URL,
				APIKeys:   config.SimpleSecureStrings("test-key"),
			},
		},
	}
	provider := &visionUnsupportedMediaProvider{}
	al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	t.Cleanup(func() { al.Close() })
	return al, provider
}

func visionTestInbound(sessionKey, messageID, content string, media []string) bus.InboundMessage {
	return testInboundMessage(bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:   "telegram",
			ChatID:    "chat1",
			ChatType:  "direct",
			SenderID:  "user1",
			MessageID: messageID,
		},
		Content:    content,
		Media:      media,
		SessionKey: sessionKey,
	})
}

func TestAgentLoop_VisionDistillatePersistedAndSatelliteOnce(t *testing.T) {
	var visionHits atomic.Int32
	al, provider := newVisionPersistTestLoop(t, &visionHits, false)

	sessionKey := "agent:main:telegram:direct:user105"
	ctx, cancel := context.WithTimeout(context.Background(), responseTimeout)
	defer cancel()

	resp, err := al.processMessage(ctx, visionTestInbound(
		sessionKey, "m1", "что на скрине?", visionTestMedia,
	))
	if err != nil {
		t.Fatalf("turn1 processMessage() error = %v", err)
	}
	if resp != "ok" {
		t.Fatalf("turn1 response = %q, want %q", resp, "ok")
	}

	// main получил дистиллят БЕЗ media с первого раза — реактивный ретрай не нужен
	if provider.calls != 1 {
		t.Fatalf("turn1 calls = %d, want 1 (distillate, no retry)", provider.calls)
	}
	if !slices.Equal(provider.mediaSeen, []bool{false}) {
		t.Fatalf("turn1 mediaSeen = %v, want [false]", provider.mediaSeen)
	}
	if visionHits.Load() != 1 {
		t.Fatalf("turn1 satellite hits = %d, want 1", visionHits.Load())
	}

	// БД: контент С дистиллятом (ищется поиском), Media на месте (архив)
	history := al.registry.GetDefaultAgent().Sessions.GetHistory(sessionKey)
	if len(history) < 2 {
		t.Fatalf("turn1 history len = %d, want >= 2", len(history))
	}
	if !strings.Contains(history[0].Content, "распознанное vision-спутником") ||
		!strings.Contains(history[0].Content, "скриншот настроек модели") {
		t.Fatalf("persisted content has no distillate: %q", history[0].Content)
	}
	if !slices.Equal(history[0].Media, []string{"data:image/png;base64,abc123"}) {
		t.Fatalf("persisted Media = %v, want preserved in DB", history[0].Media)
	}

	// Ход 2: старая картинка из истории НЕ перераспознаётся и не едет в main
	// Ход 2 намеренно БЕЗ новой картинки: если старая из истории долетит
	// до роутера — спутник дёрнется повторно и тест упадёт.
	resp2, err := al.processMessage(ctx, visionTestInbound(sessionKey, "m2", "а что там слева?", nil))
	if err != nil {
		t.Fatalf("turn2 processMessage() error = %v", err)
	}
	if resp2 != "ok" {
		t.Fatalf("turn2 response = %q, want %q", resp2, "ok")
	}
	if provider.calls != 2 {
		t.Fatalf("turn2 total calls = %d, want 2", provider.calls)
	}
	if !slices.Equal(provider.mediaSeen, []bool{false, false}) {
		t.Fatalf("turn2 mediaSeen = %v, want [false false] (history stripped)", provider.mediaSeen)
	}
	if visionHits.Load() != 1 {
		t.Fatalf("satellite re-called on turn2: hits = %d, want 1", visionHits.Load())
	}
}

func TestAgentLoop_VisionSatelliteFailureMarkerPersisted(t *testing.T) {
	var visionHits atomic.Int32
	al, provider := newVisionPersistTestLoop(t, &visionHits, true)

	sessionKey := "agent:main:telegram:direct:user106"
	ctx, cancel := context.WithTimeout(context.Background(), responseTimeout)
	defer cancel()

	resp, err := al.processMessage(ctx, visionTestInbound(
		sessionKey, "m1", "что на скрине?", visionTestMedia,
	))
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if resp != "ok" {
		t.Fatalf("response = %q, want %q", resp, "ok")
	}
	if provider.calls != 1 {
		t.Fatalf("calls = %d, want 1", provider.calls)
	}
	if visionHits.Load() != 1 {
		t.Fatalf("satellite hits = %d, want 1", visionHits.Load())
	}

	// Честный маркер — в БД, а не только в памяти хода
	history := al.registry.GetDefaultAgent().Sessions.GetHistory(sessionKey)
	if len(history) < 2 {
		t.Fatalf("history len = %d, want >= 2", len(history))
	}
	if !strings.Contains(history[0].Content, "Изображение не распознано") {
		t.Fatalf("persisted content has no failure marker: %q", history[0].Content)
	}
	if !slices.Equal(history[0].Media, []string{"data:image/png;base64,abc123"}) {
		t.Fatalf("persisted Media = %v, want preserved in DB", history[0].Media)
	}
}
