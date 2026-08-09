package pika

import "testing"

func TestClassifyFeedback(t *testing.T) {
	cases := []struct {
		name, prev, cur, want string
	}{
		{"correction", "", "нет, я имел в виду рестарт только auth", FeedbackCorrection},
		{"wrong", "", "это не то, опять неправильно", FeedbackWrong},
		{"clarify", "", "уточни, что ты сделал?", FeedbackClarification},
		{
			"rephrase",
			"обнови зависимости в auth сервисе",
			"обнови зависимости в auth сервисе пожалуйста",
			FeedbackRephrase,
		},
		{"none", "", "привет, как дела", FeedbackNone},
		{"empty", "", "", FeedbackNone},
		{"short prev no rephrase", "ок", "ок", FeedbackNone},
		{"correction beats wrong", "", "не то, делай так: сначала тесты", FeedbackCorrection},
	}
	for _, c := range cases {
		if got := ClassifyFeedback(c.prev, c.cur); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
