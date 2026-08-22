package pika

import (
	"context"
	"strings"
	"testing"
)

func TestSearchFeedbackMarks_FindsMarkWithSubject(t *testing.T) {
	bm := setupTestDB(t)
	defer bm.Close()
	ctx := context.Background()
	msgs := []MessageRow{
		{ChatID: "s1", PikaSessionID: "1", Role: "user", Content: "что на скрине?"},
		{
			ChatID:        "s1",
			PikaSessionID: "1",
			Role:          "assistant",
			Content:       "на скрине tmux с docker ps",
		},
		{ChatID: "s1", PikaSessionID: "1", Role: "user", Content: "ЗАЧЕМ ТЫ ВРЕШЬ"},
	}
	for _, m := range msgs {
		if _, err := bm.SaveMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := bm.MarkFeedbackSignal(ctx, "s1", "wrong"); err != nil {
		t.Fatal(err)
	}

	ms := NewMemorySearch(bm)
	res, err := ms.searchFeedbackMarks(ctx, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("got %d results, want 1", len(res))
	}
	if !strings.Contains(res[0].Summary, "tmux с docker ps") {
		t.Fatalf("subject missing: %q", res[0].Summary)
	}
	if !strings.Contains(res[0].Summary, "feedback:wrong") {
		t.Fatalf("signal missing: %q", res[0].Summary)
	}
}

func TestExpandAround_AddsNeighbors(t *testing.T) {
	bm := setupTestDB(t)
	defer bm.Close()
	ctx := context.Background()
	for _, c := range []string{"m1", "m2", "m3-hit", "m4", "m5"} {
		if _, err := bm.SaveMessage(ctx, MessageRow{
			ChatID: "s1", PikaSessionID: "1", Role: "user", Content: c,
		}); err != nil {
			t.Fatal(err)
		}
	}
	ms := NewMemorySearch(bm)
	res := []rawResult{{MsgID: 3, ChatID: "s1", Summary: "hit"}}
	out := ms.expandAround(ctx, res, 1)
	if !strings.Contains(out[0].Summary, "m2") ||
		!strings.Contains(out[0].Summary, "m4") {
		t.Fatalf("neighbors missing: %q", out[0].Summary)
	}
}
