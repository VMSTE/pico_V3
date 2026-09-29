package pika

import (
	"context"
	"strings"
	"testing"
)

// Волна 124: дельта по watermark, кап срезает голову, пустая дельта — пусто.
func TestWorkSinceBrief_WatermarkAndCap(t *testing.T) {
	bm := setupTestDB(t)
	defer bm.Close()
	ctx := context.Background()
	for _, m := range []MessageRow{
		{ChatID: "c1", PikaSessionID: "1", Role: "user", Content: "первое"},
		{ChatID: "c1", PikaSessionID: "1", Role: "assistant", Content: "второе"},
		{ChatID: "c1", PikaSessionID: "1", Role: "user", Content: "третье"},
	} {
		if _, err := bm.SaveMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	maxID := bm.GetMaxMessageID(ctx, "c1")
	if maxID != 3 {
		t.Fatalf("maxID = %d, want 3", maxID)
	}
	got := bm.GetWorkSince(ctx, "c1", 2, 1000)
	if !strings.Contains(got, "третье") || strings.Contains(got, "первое") {
		t.Fatalf("delta wrong: %q", got)
	}
	got = bm.GetWorkSince(ctx, "c1", 0, 3)
	if strings.Contains(got, "первое") || !strings.Contains(got, "третье") {
		t.Fatalf("cap must drop oldest first: %q", got)
	}
	if got := bm.GetWorkSince(ctx, "c1", maxID, 1000); got != "" {
		t.Fatalf("expected empty delta, got %q", got)
	}
}

// Волна 124: дедуп секций (бой 28 сен — ×3 дубль в AVOID).
func TestDedupeMemoryBrief_RemovesDupes(t *testing.T) {
	b := MemoryBrief{
		Avoid:   []string{"x", "x", " y ", "y", ""},
		Context: []string{"a", "a"},
	}
	dedupeMemoryBrief(&b)
	if len(b.Avoid) != 2 {
		t.Fatalf("avoid = %#v", b.Avoid)
	}
	if len(b.Context) != 1 || b.Context[0] != "a" {
		t.Fatalf("context = %#v", b.Context)
	}
}

// Волна 124: тёплые секции появляются только при данных.
func TestBuildUserMessage_WarmInput(t *testing.T) {
	a := &Archivist{cfg: DefaultArchivistConfig()}
	msg := a.buildUserMessage(context.Background(), ArchivistInput{
		SessionKey:     "s1",
		Message:        "продолжай",
		PreviousBrief:  "OLD BRIEF",
		WorkSinceBrief: "[user] делали перенос",
	})
	for _, want := range []string{
		"## PREVIOUS_BRIEF", "OLD BRIEF",
		"## WORK_SINCE_BRIEF", "делали перенос",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in warm input", want)
		}
	}
	msg2 := a.buildUserMessage(
		context.Background(), ArchivistInput{SessionKey: "s1", Message: "hi"},
	)
	if strings.Contains(msg2, "PREVIOUS_BRIEF") ||
		strings.Contains(msg2, "WORK_SINCE_BRIEF") {
		t.Error("warm sections must be omitted when empty")
	}
}
