// PIKA-V3: PikaContextManager — builds the full system prompt for
// Pika v3. Replaces upstream ContextBuilder's PromptStack with simple
// concatenation:
//   CORE.md + CONTEXT.md + MEMORY_BRIEF + TRAIL/META + PLAN + DEGRADATION.
//
// Registered as "pika" ContextManager via init() in
// pkg/agent/context_pika.go.

package pika

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
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

	// Bootstrap file cache (mtime-based invalidation)
	mu             sync.RWMutex
	cachedCore     string
	cachedContext  string
	coreModTime    time.Time
	contextModTime time.Time
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

// BuildSystemPrompt assembles the full system prompt string.
// Order: CORE + CONTEXT + BRIEF + TRAIL + META + PLAN + DEGRADATION.
func (cm *PikaContextManager) BuildSystemPrompt(
	ctx context.Context,
	sessionKey string,
) (string, error) {
	var sb strings.Builder

	// 1. CORE.md (error only on I/O failure, missing file = ok)
	core, err := cm.loadBootstrapFile("CORE.md")
	if err != nil {
		return "", fmt.Errorf(
			"pika/context_manager: load CORE.md: %w", err,
		)
	}
	if core != "" {
		sb.WriteString(core)
		sb.WriteString("\n\n")
	}

	// 2. CONTEXT.md (optional — skip on error)
	ctxContent, _ := cm.loadBootstrapFile("CONTEXT.md")
	if ctxContent != "" {
		sb.WriteString(ctxContent)
		sb.WriteString("\n\n")
	}

	// 3. MEMORY BRIEF (Archivist, wave 3)
	archResult, _ := cm.archivist.BuildPrompt(
		ctx, ArchivistInput{SessionKey: sessionKey},
	)
	brief := ""
	if archResult != nil {
		brief = archResult.BriefText
	}
	if brief != "" {
		sb.WriteString("--- MEMORY BRIEF ---\n")
		sb.WriteString(brief)
		sb.WriteString("\n\n")
	}

	// 4. TRAIL (ring buffer of last N tool calls)
	if cm.trail != nil {
		trailText := cm.trail.Serialize()
		if trailText != "" {
			sb.WriteString(trailText)
			sb.WriteString("\n\n")
		}
	}

	// 5. META (system metrics)
	if cm.meta != nil {
		metaText := cm.meta.Serialize()
		if metaText != "" {
			sb.WriteString(metaText)
			sb.WriteString("\n\n")
		}
	}

	// 6. ACTIVE_PLAN — extract from last reasoning (wave 4)
	planText := cm.extractActivePlan(ctx, sessionKey)
	if planText != "" {
		sb.WriteString("--- ACTIVE_PLAN ---\n")
		sb.WriteString(planText)
		sb.WriteString("\n\n")
		if cm.planStore != nil {
			cm.planStore.SetActivePlan(planText)
		}
	} else if cm.planStore != nil {
		cm.planStore.SetActivePlan("")
	}

	// 7. DEGRADATION block (if system is not healthy)
	cm.injectDegradation(&sb)

	return strings.TrimRight(sb.String(), "\n"), nil
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

// loadBootstrapFile reads a file from workspace with mtime caching.
func (cm *PikaContextManager) loadBootstrapFile(
	name string,
) (string, error) {
	path := filepath.Join(cm.workspace, name)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // missing file is not an error
		}
		return "", fmt.Errorf(
			"pika/context_manager: stat %s: %w", name, err,
		)
	}

	cm.mu.RLock()
	cached, modTime := cm.getCached(name)
	cm.mu.RUnlock()

	if cached != "" && !info.ModTime().After(modTime) {
		return cached, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf(
			"pika/context_manager: read %s: %w", name, err,
		)
	}
	content := string(data)

	cm.mu.Lock()
	cm.setCached(name, content, info.ModTime())
	cm.mu.Unlock()

	return content, nil
}

// getCached returns cached content and modtime for a bootstrap file.
// Must be called with cm.mu held (at least RLock).
func (cm *PikaContextManager) getCached(
	name string,
) (string, time.Time) {
	switch name {
	case "CORE.md":
		return cm.cachedCore, cm.coreModTime
	case "CONTEXT.md":
		return cm.cachedContext, cm.contextModTime
	default:
		return "", time.Time{}
	}
}

// setCached stores cached content and modtime.
// Must be called with cm.mu held (Lock).
func (cm *PikaContextManager) setCached(
	name, content string, modTime time.Time,
) {
	switch name {
	case "CORE.md":
		cm.cachedCore = content
		cm.coreModTime = modTime
	case "CONTEXT.md":
		cm.cachedContext = content
		cm.contextModTime = modTime
	}
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

// InvalidateCache clears all cached bootstrap files.
// Called on session rotation or config reload.
func (cm *PikaContextManager) InvalidateCache() {
	cm.mu.Lock()
	cm.cachedCore = ""
	cm.cachedContext = ""
	cm.coreModTime = time.Time{}
	cm.contextModTime = time.Time{}
	cm.mu.Unlock()
}

// GetTrail returns the Trail instance for external use.
func (cm *PikaContextManager) GetTrail() *Trail {
	return cm.trail
}

// GetMeta returns the Meta instance for external use.
func (cm *PikaContextManager) GetMeta() *Meta {
	return cm.meta
}
