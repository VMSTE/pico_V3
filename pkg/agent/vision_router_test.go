package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestCollectImageDataURLs(t *testing.T) {
	msgs := []providers.Message{
		{
			Role: "user",
			Media: []string{
				"data:image/png;base64,AAA",
				"media://abc",
				"data:image/jpeg;base64,BBB",
			},
		},
		{Role: "user", Media: nil},
	}
	got := collectImageDataURLs(msgs)
	if len(got) != 2 {
		t.Fatalf("got %d items, want 2: %#v", len(got), got)
	}
}

func TestAmendMessagesWithDistillate(t *testing.T) {
	msgs := []providers.Message{
		{Role: "user", Content: "что на скрине?", Media: []string{"data:image/png;base64,AAA"}},
		{Role: "user", Content: "просто текст"},
	}
	out := amendMessagesWithDistillate(msgs, "скриншот страницы PR")
	if out[0].Media != nil {
		t.Fatal("media must be stripped")
	}
	if !strings.Contains(out[0].Content, "vision-спутником") ||
		!strings.Contains(out[0].Content, "скриншот страницы PR") {
		t.Fatalf("content = %q", out[0].Content)
	}
	if out[1].Content != "просто текст" {
		t.Fatalf("untouched message changed: %q", out[1].Content)
	}
}

func TestAgentModelSupportsVision(t *testing.T) {
	cfg := &config.Config{}
	if agentModelSupportsVision(cfg, "missing") {
		t.Fatal("unknown model must not report vision")
	}
	vision := true
	cfg.ModelList = []*config.ModelConfig{{ModelName: "m1", Vision: &vision}}
	if !agentModelSupportsVision(cfg, "m1") {
		t.Fatal("vision=true must report vision")
	}
	cfg.ModelList = []*config.ModelConfig{{ModelName: "m2"}}
	if agentModelSupportsVision(cfg, "m2") {
		t.Fatal("nil vision must default to false")
	}
}

type visionMockProvider struct{ resp *providers.LLMResponse }

func (m *visionMockProvider) Chat(
	_ context.Context, _ []providers.Message, _ []providers.ToolDefinition,
	_ string, _ map[string]any,
) (*providers.LLMResponse, error) {
	return m.resp, nil
}

func (m *visionMockProvider) GetDefaultModel() string { return "mock" }

// Волна 102: resp не теряется — токены нужны телеметрии спутника.
func TestDistillateImages_ReturnsResponseForTelemetry(t *testing.T) {
	mock := &visionMockProvider{resp: &providers.LLMResponse{
		Content: "описание",
		Usage:   &providers.UsageInfo{PromptTokens: 10, CompletionTokens: 5},
	}}
	text, resp, err := distillateImages(
		context.Background(), mock, "m", []string{"data:image/png;base64,AAA"},
	)
	if err != nil || text != "описание" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	if resp == nil || resp.Usage == nil || resp.Usage.PromptTokens != 10 {
		t.Fatalf("resp lost: %#v", resp)
	}
}
