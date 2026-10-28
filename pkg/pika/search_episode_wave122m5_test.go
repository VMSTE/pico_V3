package pika

// ТЗ-122 (срез М5): seen-set эпизода, границы эпизода, augmentQuery.

import (
	"strings"
	"testing"
)

func epFixture() []SearchResult {
	return []SearchResult{
		{Source: "messages", MsgID: 1, Summary: "токен ноушена сохранили в registry"},
		{Source: "messages", MsgID: 2, Summary: "какой мой любимый цвет"},
		{Source: "messages", MsgID: 3, Summary: "новая запись про доступ"},
	}
}

func TestEpisode_ContinuationFiltersShown(t *testing.T) {
	ms := &MemorySearch{}
	out1 := ms.episodeFilter("s1", "что сделали с токеном ноушена", epFixture())
	if len(out1) != 3 {
		t.Fatalf("первый раунд не должен фильтровать: %d", len(out1))
	}
	ms.episodeMark("s1", out1[:2]) // показали первые два
	r2 := ms.episodeFilter("s1", "токеном ноушена куда сохранили", epFixture())
	if len(r2) != 1 || r2[0].MsgID != 3 {
		t.Fatalf("показанное не скрыто / новое потеряно: %+v", r2)
	}
}

func TestEpisode_RepeatQueryResets(t *testing.T) {
	ms := &MemorySearch{}
	q := "какой мой любимый цвет"
	out1 := ms.episodeFilter("s1", q, epFixture())
	ms.episodeMark("s1", out1)
	// Дословный повтор (пользователь переспросил) → НЕ фильтруем.
	r2 := ms.episodeFilter("s1", q, epFixture())
	if len(r2) != 3 {
		t.Fatalf("дословный повтор не должен фильтровать: %d", len(r2))
	}
}

func TestEpisode_NewTopicResets(t *testing.T) {
	ms := &MemorySearch{}
	out1 := ms.episodeFilter("s1", "что сделали с токеном ноушена", epFixture())
	ms.episodeMark("s1", out1)
	r2 := ms.episodeFilter("s1", "когда был деплой лаунчера", epFixture())
	if len(r2) != 3 {
		t.Fatalf("новая тема не должна фильтровать: %d", len(r2))
	}
}

func TestAugmentQuery(t *testing.T) {
	res := []SearchResult{
		{Summary: "сохранили в registry и проверили доступ"},
		{Summary: "registry снова доступ"},
		{Summary: "постороннее"},
	}
	got := augmentQuery("токен ноушена", res)
	if !strings.Contains(got, "registry") {
		t.Fatalf("термин из хитов не дописан: %q", got)
	}
	if strings.Contains(got, "токен ноушена токен") {
		t.Fatalf("токен запроса задублирован: %q", got)
	}
	// детерминизм: повторный вызов — тот же результат
	if augmentQuery("токен ноушена", res) != got {
		t.Fatal("augmentQuery недетерминирован")
	}
}
