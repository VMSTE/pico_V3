package pika

import (
	"strings"
	"testing"
)

// Волна 124-fix (бой 29 сен): handoff-FOCUS обязан сериализоваться —
// до фикса он собирался архивариусом и выбрасывался до промпта.
func TestSerializeFocus(t *testing.T) {
	blocked := "ждём ответ API"
	f := Focus{
		Task:        "миграция Notion → GitHub",
		Step:        "перенесены 3 из 5 проектов",
		Mode:        "work",
		Blocked:     &blocked,
		Constraints: []string{"не трогать main напрямую"},
		Decisions:   []string{"целевой репо — pico_V3"},
	}
	text := SerializeFocus(f)
	for _, want := range []string{
		"TASK: миграция Notion → GitHub",
		"STEP: перенесены 3 из 5 проектов",
		"MODE: work",
		"BLOCKED: ждём ответ API",
		"CONSTRAINT: не трогать main напрямую",
		"DECISION: целевой репо — pico_V3",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}

	// Пустой Focus → пусто (секции в промпте нет).
	if got := SerializeFocus(Focus{}); got != "" {
		t.Errorf("empty focus = %q, want empty", got)
	}
	// Режим без задачи/шага не создаёт секцию.
	if got := SerializeFocus(Focus{Mode: "routine"}); got != "" {
		t.Errorf("mode-only focus = %q, want empty", got)
	}
}
