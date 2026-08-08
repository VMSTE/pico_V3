package pika

import (
	"context"
	"testing"
)

// Р-2, критерий 1: текст с фразой из списка даёт 3 балла.
func TestRAD_PhraseScoresThree(t *testing.T) {
	rad := NewRAD(DefaultRADConfig())
	result := rad.Analyze(
		context.Background(),
		"Хм, tool вернул ошибку, попробую иначе",
		nil, nil,
	)
	if result.Score != 3 {
		t.Errorf("phrase must score exactly 3, got %d", result.Score)
	}
	if len(result.Detectors) != 1 || result.Detectors[0] != "pattern" {
		t.Errorf("detectors: %v", result.Detectors)
	}
}

// Р-2, критерий 3: «ядовитая» фраза из конфига не роняет процесс.
// Фразы проходят regexp.QuoteMeta перед компиляцией — паника
// недостижима по построению; тест фиксирует это свойство.
func TestRAD_NastyKeywordNoPanic(t *testing.T) {
	cfg := DefaultRADConfig()
	cfg.PatternKeywordsRU = []string{"((([", `\C`, "(?P<broken", "[a-"}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("NewRAD panicked on nasty keyword: %v", r)
		}
	}()
	rad := NewRAD(cfg)
	result := rad.Analyze(
		context.Background(), "обычный текст без фраз", nil, nil,
	)
	if result.Verdict != RADSafe {
		t.Errorf("clean text must be safe, got %s", result.Verdict)
	}
}
