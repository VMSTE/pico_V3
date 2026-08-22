package pika

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func newTestAtomizer(
	t *testing.T,
	prov providers.LLMProvider,
	cfgOverride *AtomizerConfig,
) (*Atomizer, *BotMemory, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	db, err := Migrate(dbPath)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	mem, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("botmemory: %v", err)
	}
	atomGen := NewAtomIDGenerator(mem)

	promptPath := filepath.Join(dir, "atomizer.md")
	//nolint:errcheck
	os.WriteFile(
		promptPath,
		[]byte(defaultAtomizerPrompt),
		0o644,
	)

	c := DefaultAtomizerConfig()
	c.PromptFile = promptPath
	if cfgOverride != nil {
		if cfgOverride.TriggerTokens > 0 {
			c.TriggerTokens = cfgOverride.TriggerTokens
		}
		if cfgOverride.ChunkMaxTokens > 0 {
			c.ChunkMaxTokens = cfgOverride.ChunkMaxTokens
		}
		if cfgOverride.MaxRetries > 0 {
			c.MaxRetries = cfgOverride.MaxRetries
		}
	}

	tel := NewTelemetry(
		TelemetryConfig{}, mem, nil,
	)

	atomizer := NewAtomizer(
		mem, atomGen, prov, tel, c,
	)
	cleanup := func() {
		mem.Close()
		db.Close()
	}
	return atomizer, mem, cleanup
}

// seedAtomizerData inserts messages and events for testing.
func seedAtomizerData(
	t *testing.T,
	mem *BotMemory,
	sid string,
	turnCount int,
	tokensPerTurn int,
) {
	t.Helper()
	ctx := context.Background()
	for turn := 1; turn <= turnCount; turn++ {
		_, err := mem.SaveMessage(ctx, MessageRow{
			ChatID:        sid,
			PikaSessionID: strconv.Itoa(turn),
			Role:          "user",
			Content: fmt.Sprintf(
				"user message turn %d", turn,
			),
			Tokens: tokensPerTurn / 2,
		})
		if err != nil {
			t.Fatalf("save user msg: %v", err)
		}
		_, err = mem.SaveMessage(ctx, MessageRow{
			ChatID:        sid,
			PikaSessionID: strconv.Itoa(turn),
			Role:          "assistant",
			Content: fmt.Sprintf(
				"assistant response turn %d", turn,
			),
			Tokens: tokensPerTurn / 2,
		})
		if err != nil {
			t.Fatalf("save asst msg: %v", err)
		}
		tags, _ := json.Marshal(
			[]string{"deploy", "nginx"},
		)
		_, err = mem.SaveEvent(ctx, EventRow{
			Type:          "compose_restart",
			Summary:       fmt.Sprintf("event %d", turn),
			Outcome:       "success",
			ChatID:        sid,
			PikaSessionID: strconv.Itoa(turn),
			Tags:          tags,
		})
		if err != nil {
			t.Fatalf("save event: %v", err)
		}
	}
}

func TestAtomizer_ShouldAtomize_BelowThreshold(
	t *testing.T,
) {
	prov := newMockProvider()
	cfg := &AtomizerConfig{TriggerTokens: 1000}
	a, mem, cleanup := newTestAtomizer(t, prov, cfg)
	defer cleanup()

	ctx := context.Background()
	sid := "test-session"
	seedAtomizerData(t, mem, sid, 1, 500)

	should, err := a.ShouldAtomize(ctx, sid)
	if err != nil {
		t.Fatalf("ShouldAtomize: %v", err)
	}
	if should {
		t.Error("should be false below threshold")
	}
}

func TestAtomizer_ShouldAtomize_AboveThreshold(
	t *testing.T,
) {
	prov := newMockProvider()
	cfg := &AtomizerConfig{TriggerTokens: 100}
	a, mem, cleanup := newTestAtomizer(t, prov, cfg)
	defer cleanup()

	ctx := context.Background()
	sid := "test-session"
	seedAtomizerData(t, mem, sid, 5, 100)

	should, err := a.ShouldAtomize(ctx, sid)
	if err != nil {
		t.Fatalf("ShouldAtomize: %v", err)
	}
	if !should {
		t.Error("should be true above threshold")
	}
}

func TestAtomizer_ShouldAtomize_Disabled(
	t *testing.T,
) {
	prov := newMockProvider()
	a, _, cleanup := newTestAtomizer(t, prov, nil)
	defer cleanup()

	a.cfg.Enabled = false
	should, err := a.ShouldAtomize(
		context.Background(), "s1",
	)
	if err != nil {
		t.Fatalf("ShouldAtomize: %v", err)
	}
	if should {
		t.Error("should be false when disabled")
	}
}

func TestAtomizer_Run_HappyPath(t *testing.T) {
	llmResp := &providers.LLMResponse{
		Content: `{
			"atoms": [
				{
					"category": "summary",
					"summary": "deployed nginx on port 8080",
					"detail": "nginx v1.25",
					"polarity": "positive",
					"confidence": 0.95,
					"source_turns": ["1", "2"]
				},
				{
					"category": "decision",
					"summary": "use port 8080",
					"polarity": "neutral",
					"confidence": 0.8,
					"source_turns": ["2"]
				}
			]
		}`,
	}
	prov := newMockProvider(llmResp)
	cfg := &AtomizerConfig{
		TriggerTokens:  100,
		ChunkMaxTokens: 10000,
	}
	a, mem, cleanup := newTestAtomizer(t, prov, cfg)
	defer cleanup()

	ctx := context.Background()
	sid := "sess-1"
	seedAtomizerData(t, mem, sid, 3, 200)

	err := a.Run(ctx, sid)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify atoms inserted via FTS
	atoms, err := mem.QueryKnowledgeFTS(
		ctx, "nginx", 10,
	)
	if err != nil {
		t.Fatalf("query atoms: %v", err)
	}
	if len(atoms) < 1 {
		t.Error("expected at least 1 atom")
	}

	// Verify hot messages deleted after archive
	msgs, err := mem.GetMessages(ctx, sid)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf(
			"expected 0 hot messages, got %d",
			len(msgs),
		)
	}

	// Verify 1 LLM call
	if prov.callCount() != 1 {
		t.Errorf(
			"LLM calls = %d, want 1",
			prov.callCount(),
		)
	}
}

func TestAtomizer_Run_ValidationRetry(t *testing.T) {
	// First: invalid category -> retry
	badResp := &providers.LLMResponse{
		Content: `{"atoms":[{` +
			`"category":"invalid",` +
			`"summary":"test",` +
			`"polarity":"positive",` +
			`"confidence":0.9,` +
			`"source_turns":["1"]}]}`,
	}
	// Second: valid
	goodResp := &providers.LLMResponse{
		Content: `{"atoms":[{` +
			`"category":"summary",` +
			`"summary":"test",` +
			`"polarity":"positive",` +
			`"confidence":0.9,` +
			`"source_turns":["1"]}]}`,
	}
	prov := newMockProvider(badResp, goodResp)
	cfg := &AtomizerConfig{
		TriggerTokens:  100,
		ChunkMaxTokens: 10000,
	}
	a, mem, cleanup := newTestAtomizer(t, prov, cfg)
	defer cleanup()

	seedAtomizerData(t, mem, "s1", 2, 200)
	err := a.Run(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 2 LLM calls: bad + repair
	if prov.callCount() != 2 {
		t.Errorf(
			"LLM calls = %d, want 2",
			prov.callCount(),
		)
	}
}

func TestAtomizer_Run_AllRetriesExhausted(
	t *testing.T,
) {
	// Empty atoms array = validation error
	badResp := &providers.LLMResponse{
		Content: `{"atoms":[]}`,
	}
	prov := newMockProvider(
		badResp, badResp, badResp,
	)
	cfg := &AtomizerConfig{
		TriggerTokens:  100,
		ChunkMaxTokens: 10000,
		MaxRetries:     2,
	}
	a, mem, cleanup := newTestAtomizer(t, prov, cfg)
	defer cleanup()

	seedAtomizerData(t, mem, "s1", 2, 200)
	err := a.Run(context.Background(), "s1")
	if err == nil {
		t.Fatal("expected error after retries")
	}
	if !strings.Contains(
		err.Error(), "retries exhausted",
	) {
		t.Errorf("error = %q", err)
	}

	// Hot messages NOT deleted on failure
	msgs, err := mem.GetMessages(
		context.Background(), "s1",
	)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(msgs) == 0 {
		t.Error(
			"messages should not be deleted on failure",
		)
	}
}

func TestAtomizer_Run_EmptySession(t *testing.T) {
	prov := newMockProvider()
	a, _, cleanup := newTestAtomizer(t, prov, nil)
	defer cleanup()

	err := a.Run(
		context.Background(), "empty-session",
	)
	if err != nil {
		t.Fatalf("Run on empty: %v", err)
	}
	if prov.callCount() != 0 {
		t.Errorf(
			"LLM calls = %d, want 0",
			prov.callCount(),
		)
	}
}

func TestAtomizer_Run_LLMError(t *testing.T) {
	prov := &mockLLMProvider{
		responses: []*providers.LLMResponse{
			nil, nil, nil,
		},
		errors: []error{
			fmt.Errorf("LLM down"),
			fmt.Errorf("LLM down"),
			fmt.Errorf("LLM down"),
		},
	}
	cfg := &AtomizerConfig{
		TriggerTokens:  100,
		ChunkMaxTokens: 10000,
	}
	a, mem, cleanup := newTestAtomizer(t, prov, cfg)
	defer cleanup()

	seedAtomizerData(t, mem, "s1", 2, 200)
	err := a.Run(context.Background(), "s1")
	if err == nil {
		t.Fatal("expected error on LLM failure")
	}
}

func TestValidateAtoms_Valid(t *testing.T) {
	atoms := []AtomLLMOutput{
		{
			Category:    "summary",
			Summary:     "test summary",
			Polarity:    "positive",
			Confidence:  0.9,
			SourceTurns: TurnIDList{"1", "2"},
		},
		{
			Category:    "decision",
			Summary:     "use port 80",
			Polarity:    "neutral",
			Confidence:  0.7,
			SourceTurns: TurnIDList{"2"},
		},
	}
	err := validateAtoms(atoms, []string{"1", "2", "3"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateAtoms_InvalidCategory(t *testing.T) {
	atoms := []AtomLLMOutput{
		{
			Category:    "invalid",
			Summary:     "test",
			Polarity:    "positive",
			Confidence:  0.9,
			SourceTurns: TurnIDList{"1"},
		},
	}
	err := validateAtoms(atoms, []string{"1"})
	if err == nil {
		t.Error("expected error for invalid category")
	}
}

func TestValidateAtoms_InvalidPolarity(t *testing.T) {
	atoms := []AtomLLMOutput{
		{
			Category:    "summary",
			Summary:     "test",
			Polarity:    "bad",
			Confidence:  0.9,
			SourceTurns: TurnIDList{"1"},
		},
	}
	err := validateAtoms(atoms, []string{"1"})
	if err == nil {
		t.Error("expected error for invalid polarity")
	}
}

func TestValidateAtoms_ConfidenceOutOfRange(
	t *testing.T,
) {
	atoms := []AtomLLMOutput{
		{
			Category:    "summary",
			Summary:     "test",
			Polarity:    "positive",
			Confidence:  1.5,
			SourceTurns: TurnIDList{"1"},
		},
	}
	err := validateAtoms(atoms, []string{"1"})
	if err == nil {
		t.Error("expected error for confidence > 1")
	}
}

func TestValidateAtoms_TurnNotInChunk(t *testing.T) {
	atoms := []AtomLLMOutput{
		{
			Category:    "summary",
			Summary:     "test",
			Polarity:    "positive",
			Confidence:  0.9,
			SourceTurns: TurnIDList{"99"},
		},
	}
	err := validateAtoms(atoms, []string{"1", "2"})
	if err == nil {
		t.Error("expected error for turn not in chunk")
	}
}

func TestValidateAtoms_Empty(t *testing.T) {
	err := validateAtoms(nil, []string{"1"})
	if err == nil {
		t.Error("expected error for empty atoms")
	}
}

func TestValidateAtoms_EmptySummary(t *testing.T) {
	atoms := []AtomLLMOutput{
		{
			Category:    "summary",
			Summary:     "",
			Polarity:    "positive",
			Confidence:  0.9,
			SourceTurns: TurnIDList{"1"},
		},
	}
	err := validateAtoms(atoms, []string{"1"})
	if err == nil {
		t.Error("expected error for empty summary")
	}
}

func TestExtractAtomizerJSON(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{`{"atoms":[]}`, `{"atoms":[]}`},
		{`text {"a":1} more`, `{"a":1}`},
		{`no json here`, ""},
		{`[{"a":1}]`, `[{"a":1}]`},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractAtomizerJSON(tt.in)
		if got != tt.want {
			t.Errorf(
				"extractAtomizerJSON(%q) = %q, want %q",
				tt.in, got, tt.want,
			)
		}
	}
}

func TestCollectTagsByTurn(t *testing.T) {
	tags1, _ := json.Marshal([]string{"a", "b"})
	tags2, _ := json.Marshal([]string{"b", "c"})
	events := []EventRow{
		{PikaSessionID: "1", Tags: tags1},
		{PikaSessionID: "1", Tags: tags2},
		{PikaSessionID: "2", Tags: tags1},
	}
	result := collectTagsByTurn(events)
	if len(result["1"]) != 3 {
		t.Errorf(
			"turn 1 tags = %v, want [a b c]",
			result["1"],
		)
	}
	if len(result["2"]) != 2 {
		t.Errorf(
			"turn 2 tags = %v, want [a b]",
			result["2"],
		)
	}
}

func TestMergeTagsForTurns(t *testing.T) {
	tagsByTurn := map[string][]string{
		"1": {"a", "b"},
		"2": {"b", "c"},
	}
	merged := mergeTagsForTurns(
		[]string{"1", "2"}, tagsByTurn,
	)
	if len(merged) != 3 {
		t.Errorf(
			"merged = %v, want [a b c]", merged,
		)
	}
}

func TestDefaultAtomizerConfig(t *testing.T) {
	cfg := DefaultAtomizerConfig()
	if !cfg.Enabled {
		t.Error("should be enabled by default")
	}
	if cfg.TriggerTokens != 100000 {
		t.Errorf(
			"TriggerTokens = %d, want 100000",
			cfg.TriggerTokens,
		)
	}
	if cfg.ChunkMaxTokens != 200000 {
		t.Errorf(
			"ChunkMaxTokens = %d, want 200000",
			cfg.ChunkMaxTokens,
		)
	}
	if cfg.Model != "background" {
		t.Errorf("Model = %q, want background", cfg.Model)
	}
}

// D-AUDIT-62: метрики считает Go из данных чанка, только свои ходы.
func TestComputeTrajectoryMetrics(t *testing.T) {
	msgs := []MessageRow{
		{PikaSessionID: "1", Tokens: 100, Ts: time.UnixMilli(0)},
		{PikaSessionID: "1", Tokens: 50, Ts: time.UnixMilli(1000)},
		{PikaSessionID: "2", Tokens: 999, Ts: time.UnixMilli(2000)},
	}
	events := []EventRow{
		{
			PikaSessionID: "1", Outcome: "success", Ts: time.UnixMilli(500),
			Tags: json.RawMessage(`["tool:exec","op:restart"]`),
		},
		{
			PikaSessionID: "1", Outcome: "failure", Ts: time.UnixMilli(700),
			Tags: json.RawMessage(`["tool:compose"]`),
		},
		{
			PikaSessionID: "2", Outcome: "success", Ts: time.UnixMilli(1500),
			Tags: json.RawMessage(`["tool:exec"]`),
		},
	}
	tm := computeTrajectoryMetrics(msgs, events, []string{"1"})
	if tm.TokensUsed != 150 {
		t.Errorf("tokens = %d, want 150", tm.TokensUsed)
	}
	if tm.ActualCalls != 2 || tm.FailedCalls != 1 {
		t.Errorf("calls = %d/%d, want 2/1", tm.ActualCalls, tm.FailedCalls)
	}
	if tm.DurationMs != 1000 {
		t.Errorf("duration = %d, want 1000", tm.DurationMs)
	}
	if len(tm.ToolSequence) != 2 ||
		tm.ToolSequence[0] != "exec" || tm.ToolSequence[1] != "compose" {
		t.Errorf("sequence = %v", tm.ToolSequence)
	}
	// Ход без событий инструментов — нулевые вызовы, без паники
	tm2 := computeTrajectoryMetrics(msgs, events, []string{"2"})
	if tm2.ActualCalls != 1 || tm2.TokensUsed != 999 {
		t.Errorf("turn2 = %d calls/%d tokens", tm2.ActualCalls, tm2.TokensUsed)
	}
}

// Волна 86 (бой 20 авг): LLM вернул source_turns числами — strict
// unmarshal убивал прогон, атомы не писались. Принимаем оба вида.
func TestAtomizerResponse_SourceTurnsNumbers(t *testing.T) {
	raw := `{"atoms":[{"category":"summary","summary":"факт","polarity":"neutral","confidence":0.9,"source_turns":[1,2]}]}`
	var resp atomizerLLMResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal numeric source_turns: %v", err)
	}
	got := resp.Atoms[0].SourceTurns
	if len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Fatalf("SourceTurns = %v, want [1 2]", got)
	}
	if err := validateAtoms(resp.Atoms, []string{"1", "2"}); err != nil {
		t.Fatalf("validateAtoms: %v", err)
	}
}

// D-AUDIT-126 (волна 103): атом получает провенанс — ссылку на первое
// сообщение первого source-turn (восстановление D-78/F10-6).
func TestInsertAtom_SetsSourceMessageID(t *testing.T) {
	prov := newMockProvider()
	a, mem, cleanup := newTestAtomizer(t, prov, nil)
	defer cleanup()
	ctx := context.Background()
	msgs := []MessageRow{
		{ID: 42, ChatID: "s1", PikaSessionID: "1", Role: "user", Content: "x"},
		{ID: 43, ChatID: "s1", PikaSessionID: "1", Role: "assistant", Content: "y"},
	}
	atom := AtomLLMOutput{
		Category: "summary", Summary: "provenance test",
		Polarity: "neutral", Confidence: 0.9, SourceTurns: TurnIDList{"1"},
	}
	if err := a.insertAtom(ctx, "s1", atom, map[string][]string{}, msgs, nil); err != nil {
		t.Fatal(err)
	}
	var smid int64
	if err := mem.db.QueryRow(
		`SELECT COALESCE(source_message_id,0) FROM knowledge_atoms`,
	).Scan(&smid); err != nil {
		t.Fatal(err)
	}
	if smid != 42 {
		t.Errorf("source_message_id = %d, want 42 (first msg of turn)", smid)
	}
}
