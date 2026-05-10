// PIKA-V3: PikaContextManager — builds the full system prompt for
// Pika v3. Replaces upstream ContextBuilder's PromptStack with simple
// concatenation:
//   MEMORY_BRIEF + TRAIL + ACTIVE_PLAN + DEGRADATION via PromptContributors.
//
// Registered as "pika" ContextManager via init() in
// pkg/agent/context_pika.go.

package pika

import (
	"context"
	"fmt"
	"strings"
)

// PikaContextManager assembles the system prompt from bootstrap
// files, TRAIL/META, MEMORY BRIEF, and health status.
// Thread-safe: all public methods are safe for concurrent use.
type PikaContextManager struct {
	workspace     string
	trail         *Trail
	meta          *Meta
	stateProvider SystemStateProvider
	archivist     ArchivistCaller

	// PIKA-V3 wave 4: BotMemory for ACTIVE_PLAN queries
	botmem    *BotMemory
	planStore *ActivePlanStore
}

// NewPikaContextManager creates a PikaContextManager.
// All dependencies are injected; nil-safe (stubs used if nil).
func NewPikaContextManager(
	workspace string,
	trail *Trail,
	meta *Meta,
	stateProvider SystemStateProvider,
	archivist ArchivistCaller,
) *PikaContextManager {
	if stateProvider == nil {
		stateProvider = NewAlwaysHealthyProvider()
	}
	if archivist == nil {
		archivist = NewNoopArchivistCaller()
	}
	return &PikaContextManager{
		workspace:     workspace,
		trail:         trail,
		meta:          meta,
		stateProvider: stateProvider,
		archivist:     archivist,
	}
}

// SetBotMemory sets the BotMemory for ACTIVE_PLAN queries.
// Called from context_pika.go after construction.
func (cm *PikaContextManager) SetBotMemory(bm *BotMemory) {
	cm.botmem = bm
}

// SetPlanStore sets the ActivePlanStore for ACTIVE_PLAN.
// Called from context_pika.go after construction.
func (cm *PikaContextManager) SetPlanStore(ps *ActivePlanStore) {
	cm.planStore = ps
}

// BuildSystemPrompt is a legacy stub kept for API compatibility.
// Phase V (TZ-v2-8j): prompt assembly moved to 4 PromptContributors
// registered in pkg/agent/context_pika.go.
func (cm *PikaContextManager) BuildSystemPrompt(
	_ context.Context, _ string,
) (string, error) {
	return "", nil
}

// extractActivePlan queries reasoning_log for the last
// reasoning_text and extracts <plan> block from it.
// Returns "" if BotMemory is nil, no reasoning found, or
// no <plan> block present.
func (cm *PikaContextManager) extractActivePlan(
	ctx context.Context,
	sessionKey string,
) string {
	if cm.botmem == nil {
		return ""
	}
	text, err := cm.botmem.GetLastReasoningText(
		ctx, sessionKey,
	)
	if err != nil || text == "" {
		return ""
	}
	return ExtractActivePlan(text)
}

// injectDegradation appends a DEGRADATION block if not healthy.
func (cm *PikaContextManager) injectDegradation(
	sb *strings.Builder,
) {
	if cm.stateProvider == nil {
		return
	}
	state := cm.stateProvider.GetSystemState()
	if state.Status == "healthy" {
		return
	}

	sb.WriteString("--- DEGRADATION ---\n")
	fmt.Fprintf(sb, "System status: %s\n", state.Status)
	for _, comp := range state.DegradedComponents {
		instruction := degradationInstruction(comp)
		if instruction != "" {
			fmt.Fprintf(sb, "- %s: %s\n", comp, instruction)
		}
	}
	sb.WriteString("\n")
}

// degradationInstruction returns an LLM instruction for a
// degraded component.
func degradationInstruction(component string) string {
	switch component {
	case "archivist":
		return "Memory brief unavailable. Use search_memory."
	case "mcp_guard":
		return "MCP guard offline. DO NOT call MCP tools."
	case "toolguard":
		return "Tool guard offline. Only use read-only tools."
	case "registry":
		return "Registry may be stale. Configs may be outdated."
	case "telemetry":
		return "Telemetry offline. Metrics not being recorded."
	case "atomizer":
		return "Atomizer offline. Archival is paused."
	default:
		return fmt.Sprintf(
			"Component %s is degraded.", component,
		)
	}
}

// Compact is called after turn completes. In wave 2 this is a no-op.
// Future: trigger Atomizer threshold check (wave 5).
func (cm *PikaContextManager) Compact(
	sessionKey, reason string,
) error {
	// PIKA-V3: wave 2 stub. Atomizer (wave 5) checks thresholds.
	return nil
}

// Ingest is called after each message is persisted.
// In wave 2: no-op (messages already in SQLite via SessionStore).
func (cm *PikaContextManager) Ingest(
	sessionKey string,
) error {
	// PIKA-V3: no-op — messages in SQLite via session_store.
	return nil
}

// Clear removes all context for a session.
// In wave 2: session store handles deletion; CM has nothing extra.
func (cm *PikaContextManager) Clear(
	sessionKey string,
) error {
	// PIKA-V3: wave 2 stub. Session store handles deletion.
	return nil
}

// GetTrail returns the Trail instance for external use.
func (cm *PikaContextManager) GetTrail() *Trail {
	return cm.trail
}

// GetMeta returns the Meta instance for external use.
func (cm *PikaContextManager) GetMeta() *Meta {
	return cm.meta
}

// --- Phase В (ТЗ-v2-8j): exported getters for PromptContributor access ---

// GetArchivist returns the ArchivistCaller for MEMORY BRIEF contributor.
func (cm *PikaContextManager) GetArchivist() ArchivistCaller {
	return cm.archivist
}

// GetStateProvider returns the SystemStateProvider for DEGRADATION contributor.
func (cm *PikaContextManager) GetStateProvider() SystemStateProvider {
	return cm.stateProvider
}

// GetPlanStore returns the ActivePlanStore for ACTIVE_PLAN contributor.
func (cm *PikaContextManager) GetPlanStore() *ActivePlanStore {
	return cm.planStore
}

// ExtractActivePlan extracts <plan> block from last reasoning.
// Exported wrapper for PromptContributor access.
func (cm *PikaContextManager) ExtractActivePlan(
	ctx context.Context, sessionKey string,
) string {
	return cm.extractActivePlan(ctx, sessionKey)
}

// BuildDegradationBlock returns the DEGRADATION text (empty if healthy).
// Exported wrapper for PromptContributor access.
func (cm *PikaContextManager) BuildDegradationBlock() string {
	var sb strings.Builder
	cm.injectDegradation(&sb)
	return sb.String()
}
