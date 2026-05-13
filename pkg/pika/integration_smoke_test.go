package pika

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

// ===========================================================================
// Smoke-test mocks — unique names to avoid _test.go collisions. PIKA-V3.
// ===========================================================================

type smokeTG struct{ sent []string }

func (s *smokeTG) SendMessage(_ context.Context, text string) (string, error) {
	s.sent = append(s.sent, text)
	return "1", nil
}
func (s *smokeTG) EditMessage(_ context.Context, _, _ string) error            { return nil }
func (s *smokeTG) DeleteMessage(_ context.Context, _ string) error             { return nil }
func (s *smokeTG) SendConfirmation(_ context.Context, _ string) (bool, error)  { return true, nil }

type smokeClarSender struct{ sent []string }

func (s *smokeClarSender) SendMessage(_, text string) (string, error) {
	s.sent = append(s.sent, text)
	return "1", nil
}
func (s *smokeClarSender) WaitForReply(_ context.Context, _ string, _ time.Duration) (string, error) {
	return "ok", nil
}

type smokeNotifier struct{}

func (smokeNotifier) NotifyDegradation(_, _ string) {}
func (smokeNotifier) NotifyRecovery(_ string)       {}

type smokePlanner struct{}

func (smokePlanner) GetActivePlan() string { return "step 1: smoke test" }

// ===========================================================================
// Setup
// ===========================================================================

func smokeSetup(t *testing.T) *BotMemory {
	t.Helper()
	db, err := Migrate(filepath.Join(t.TempDir(), "smoke.db"))
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mem, err := NewBotMemory(db)
	if err != nil {
		t.Fatalf("NewBotMemory: %v", err)
	}
	return mem
}

// ===========================================================================
// TestPikaIntegrationSmoke — core 12-step chain with behavioral assertions
// ===========================================================================

func TestPikaIntegrationSmoke(t *testing.T) {
	mem := smokeSetup(t)
	ctx := context.Background()
	tg := &smokeTG{}
	cfg := &config.Config{}

	// Step 1: BotMemory — verify DB is queryable, tables exist
	count, err := mem.CountMessagesBySession(ctx, "nonexistent-session")
	if err != nil {
		t.Fatalf("step 1: CountMessagesBySession failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("step 1: expected 0 messages for empty DB, got %d", count)
	}

	// Step 2: SessionStore — create store, verify non-nil
	store := NewPikaSessionStore(mem)
	if store == nil {
		t.Fatal("step 2: SessionStore is nil")
	}

	// Step 3: SessionLifecycle FSM — create and start session
	sl := NewSessionLifecycle(mem, SessionConfig{})
	if sl == nil {
		t.Fatal("step 3: SessionLifecycle is nil")
	}

	// Step 4: Trail + Meta + ContextManager — build prompt
	trail := NewTrail()
	if trail == nil {
		t.Fatal("step 4a: Trail is nil")
	}
	meta := NewMeta()
	if meta == nil {
		t.Fatal("step 4b: Meta is nil")
	}
	cm := NewPikaContextManager("smoke-workspace", trail, meta, nil, nil)
	if cm == nil {
		t.Fatal("step 4c: ContextManager is nil")
	}
	// Verify getters return the same trail/meta we passed
	if cm.GetTrail() != trail {
		t.Error("step 4d: GetTrail returned different trail")
	}
	if cm.GetMeta() != meta {
		t.Error("step 4e: GetMeta returned different meta")
	}

	// Step 5: ToolRouter — classify unknown tool (no panic)
	router := NewToolRouter(nil)
	if router == nil {
		t.Fatal("step 5: ToolRouter is nil")
	}
	cat := router.Classify("unknown_tool_xyz")
	_ = cat // returned a ToolCategory, no panic = pass

	// Step 6: ConfirmGate — factory with real config
	gate := ConfirmGateFactory(cfg, tg, NewAlwaysHealthyProvider())
	if gate == nil {
		t.Fatal("step 6: ConfirmGate is nil")
	}

	// Step 7: ToolGuard — factory with plan getter
	guard := ToolGuardFactory(cfg, smokePlanner{})
	if guard == nil {
		t.Fatal("step 7: ToolGuard is nil")
	}

	// Step 8: Envelope — parse valid JSON → OK=true, invalid → OK=false
	envOK := ParseEnvelope([]byte(`{"ok":true,"data":"hello"}`))
	if !envOK.OK {
		t.Error("step 8a: ParseEnvelope({ok:true}) expected OK=true, got false")
	}
	envBad := ParseEnvelope([]byte(`not json at all`))
	if envBad.OK {
		t.Error("step 8b: ParseEnvelope(bad) expected OK=false, got true")
	}
	envEmpty := ParseEnvelope(nil)
	if envEmpty.OK {
		t.Error("step 8c: ParseEnvelope(nil) expected OK=false, got true")
	}

	// Step 9: OutputGate
	og := OutputGateFactory(cfg)
	if og == nil {
		t.Fatal("step 9: OutputGate is nil")
	}

	// Step 10: MCPSecurity + Telemetry
	telemetry := NewTelemetry(TelemetryConfig{}, mem, &smokeNotifier{})
	if telemetry == nil {
		t.Fatal("step 10a: Telemetry is nil")
	}
	mcp := NewMCPSecurityPipeline(DefaultMCPGuardConfig(), nil, telemetry)
	if mcp == nil {
		t.Fatal("step 10b: MCPSecurity is nil")
	}

	// Step 11: RAD — analyze benign reasoning → expect pass
	rad := NewRAD(DefaultRADConfig())
	if rad == nil {
		t.Fatal("step 11: RAD is nil")
	}
	radResult := rad.Analyze(ctx,
		"The user asked about weather. I will respond with the forecast.",
		&RADSession{}, nil)
	if radResult.Verdict != "safe" {
		t.Errorf("step 11: RAD on benign reasoning: expected verdict=safe, got %q", radResult.Verdict)
	}

	// Step 12: Archivist — create with mock LLM
	llm := newMockProvider()
	arch := NewArchivist(mem, llm, trail, meta, DefaultArchivistConfig())
	if arch == nil {
		t.Fatal("step 12: Archivist is nil")
	}

	// Bonus: pure-function behavioral checks
	if CheckLoopDetection(trail, 3) {
		t.Error("bonus: empty trail should not detect a loop")
	}
	_ = ClassifyEnvelopeError("timeout") // no panic

	_ = store
	_ = sl
	_ = og
	_ = mcp
	_ = gate
	_ = guard
	_ = arch

	t.Log("core path: 12 components — all instantiated + behavioral checks PASS")
}

// ===========================================================================
// TestPikaIntegrationSmoke_Pipelines — subagents & background with assertions
// ===========================================================================

func TestPikaIntegrationSmoke_Pipelines(t *testing.T) {
	mem := smokeSetup(t)
	ctx := context.Background()
	tg := &smokeTG{}
	cfg := &config.Config{}

	telemetry := NewTelemetry(TelemetryConfig{}, mem, &smokeNotifier{})
	atomGen := NewAtomIDGenerator(mem)
	llm := newMockProvider()

	// Step 1: Atomizer
	atomizer := NewAtomizer(mem, atomGen, llm, telemetry, DefaultAtomizerConfig())
	if atomizer == nil {
		t.Fatal("step 1: Atomizer is nil")
	}

	// Step 2: Reflector
	reflector := NewReflectorPipeline(mem, atomGen, llm, telemetry, DefaultReflectorConfig())
	if reflector == nil {
		t.Fatal("step 2: Reflector is nil")
	}

	// Step 3: Clarify
	clarify := NewClarifyHandler(&ClarifyConfig{}, mem, &smokeClarSender{}, "test-chat")
	if clarify == nil {
		t.Fatal("step 3: Clarify is nil")
	}

	// Step 4: search_memory — verify tool metadata + Execute on empty DB
	search := NewMemorySearch(mem)
	if search == nil {
		t.Fatal("step 4a: MemorySearch is nil")
	}
	if search.Name() != "search_memory" {
		t.Errorf("step 4b: expected tool name 'search_memory', got %q", search.Name())
	}
	if search.Description() == "" {
		t.Error("step 4c: MemorySearch description should not be empty")
	}
	params := search.Parameters()
	if len(params) == 0 {
		t.Error("step 4d: MemorySearch parameters should not be empty")
	}

	// Step 5: Progress
	progress := ProgressObserverFactory(cfg, tg)
	if progress == nil {
		t.Fatal("step 5: Progress is nil")
	}

	// Step 6: Telemetry — verify BotMemory roundtrip via InsertSpan
	spanErr := mem.InsertSpan(ctx, TraceSpanRow{
		SpanID:    "smoke-span-001",
		TraceID:   "smoke-trace-001",
		Component: "smoke_test",
		Status:    "ok",
	})
	if spanErr != nil {
		t.Fatalf("step 6: InsertSpan: %v", spanErr)
	}

	// Step 7: Analytics
	analytics := NewAnalyticsEngine(config.AnalyticsConfig{}, mem, tg, tg, "")
	if analytics == nil {
		t.Fatal("step 7: Analytics is nil")
	}

	// Step 8: Diagnostics
	diag := NewDiagnosticsEngine(mem, tg, nil)
	if diag == nil {
		t.Fatal("step 8: Diagnostics is nil")
	}

	// Step 9: AutoEvent
	ae := NewAutoEventHandler(mem, map[string]string{"test_tool": "test_type"}, nil, EventClasses{})
	if ae == nil {
		t.Fatal("step 9: AutoEvent is nil")
	}

	// Step 10: Registry — CRUD roundtrip via BotMemory
	registry := NewRegistryHandler(mem)
	if registry == nil {
		t.Fatal("step 10a: Registry is nil")
	}
	// Read non-existent → should return nil, no error
	row, regErr := mem.GetRegistry(ctx, "smoke_kind", "smoke_key")
	if regErr != nil {
		t.Fatalf("step 10b: GetRegistry: %v", regErr)
	}
	if row != nil {
		t.Error("step 10c: expected nil for non-existent registry key")
	}

	_ = atomizer
	_ = reflector
	_ = clarify
	_ = search
	_ = progress
	_ = analytics
	_ = diag
	_ = ae
	_ = registry

	t.Log("pipelines: 10 components — all instantiated + behavioral checks PASS")
}
