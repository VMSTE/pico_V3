package pika

import (
	"context"
	"strings"
	"testing"
)

// D-AUDIT-60: честный вход Архивариуса — сообщение, сессия, план,
// лимиты и каталог тулов с описаниями доезжают до user-сообщения.
func TestBuildUserMessage_HonestInput(t *testing.T) {
	a := &Archivist{cfg: DefaultArchivistConfig()}
	msg := a.buildUserMessage(context.Background(), ArchivistInput{
		SessionKey:           "s1",
		Message:              "перезапусти nginx",
		ActivePlan:           "шаг 1: проверить конфиг",
		ToolCatalog:          []string{"- `compose` - docker compose ops"},
		MaxRecommendedTools:  8,
		MaxRecommendedSkills: 3,
	})
	for _, want := range []string{
		"перезапусти nginx",
		"session_id: s1",
		"active_plan: шаг 1: проверить конфиг",
		"max_recommended_tools: 8",
		"max_recommended_skills: 3",
		"compose` - docker compose ops",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("user message missing %q", want)
		}
	}
	// Без плана — строки active_plan нет вообще
	msg2 := a.buildUserMessage(context.Background(), ArchivistInput{
		SessionKey: "s1", Message: "привет",
	})
	if strings.Contains(msg2, "active_plan") {
		t.Error("active_plan should be omitted when empty")
	}
}
