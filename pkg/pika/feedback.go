// PIKA-V3: Классификатор негативного фидбека пользователя (D-AUDIT-67).
// Детерминированный Go, 0 LLM. Таксономия: Don-Yehiya et al. 2024.
// Сигнал пишется в messages.metadata (D-85) и служит триггером
// фонового расследования Рефлексора.

package pika

import "strings"

// Типы сигналов фидбека.
const (
	FeedbackNone          = ""
	FeedbackRephrase      = "rephrase"      // переформулировал запрос
	FeedbackWrong         = "wrong"         // «неверно» без поправки
	FeedbackCorrection    = "correction"    // «неверно» + как надо
	FeedbackClarification = "clarification" // просит уточнить
)

var feedbackCorrectionMarkers = []string{
	"я имел в виду", "я имела в виду", "делай так", "надо так",
	"правильно так", "по-другому", "по другому",
}

var feedbackWrongMarkers = []string{
	"не то", "неправильно", "не правильно", "неверно", "не так",
	"опять не", "опять ты", "снова не", "не работает", "мимо",
	"зачем ты", "ты чего",
}

var feedbackClarifyMarkers = []string{
	"уточни", "поясни", "что ты имеешь в виду", "не понял",
	"не поняла", "а почему", "объясни",
}

// ClassifyFeedback определяет тип негативного фидбека по текущему
// сообщению пользователя и (опционально) предыдущему его сообщению.
// Порядок: поправка > «неверно» > уточнение > переформулировка.
func ClassifyFeedback(prevUserMsg, newUserMsg string) string {
	cur := strings.ToLower(strings.TrimSpace(newUserMsg))
	if cur == "" {
		return FeedbackNone
	}
	if containsAnyMarker(cur, feedbackCorrectionMarkers) {
		return FeedbackCorrection
	}
	if containsAnyMarker(cur, feedbackWrongMarkers) {
		return FeedbackWrong
	}
	if containsAnyMarker(cur, feedbackClarifyMarkers) {
		return FeedbackClarification
	}
	if prev := strings.ToLower(strings.TrimSpace(prevUserMsg)); prev != "" {
		if wordOverlapRatio(prev, cur) >= 0.6 {
			return FeedbackRephrase
		}
	}
	return FeedbackNone
}

func containsAnyMarker(s string, markers []string) bool {
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// wordOverlapRatio — доля слов предыдущего сообщения, вошедших в новое.
func wordOverlapRatio(prev, cur string) float64 {
	prevWords := strings.Fields(prev)
	if len(prevWords) < 3 {
		return 0
	}
	curSet := make(map[string]bool, len(prevWords))
	for _, w := range strings.Fields(cur) {
		curSet[w] = true
	}
	hit := 0
	for _, w := range prevWords {
		if curSet[w] {
			hit++
		}
	}
	return float64(hit) / float64(len(prevWords))
}
