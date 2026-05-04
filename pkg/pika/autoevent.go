package pika

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
)

// PIKA-V3: AutoEventHandler — deterministic event generator after tool calls.
// Zero LLM. Go maps tool action → event type → tags → INSERT into events table.

// EventClasses defines three tiers of event persistence behavior.
type EventClasses struct {
	Critical   map[string]bool // always persist + full atomization
	Diagnostic map[string]bool // persist + aggregate atomization
	Heartbeat  map[string]bool // in-memory counter, flush on rotate
}

// AutoEventHandler записывает events после tool calls.
type AutoEventHandler struct {
	bm            *BotMemory
	toolTypeMap   map[string]string   // "sandbox.run" → "tool_exec"
	toolTagMap    map[string][]string // "sandbox.run" → ["tool:sandbox"]
	eventClasses  EventClasses
	validTypes    map[string]bool // startup-built set for runtime guard
	heartbeatCtrs sync.Map        // eventType → *int64 (atomic counter)

	// PIKA-V3: entropy filter — consecutive dedup ring buffer
	mu             sync.Mutex
	recentTypes    []string
	dedupThreshold int // drop_if_consecutive_same (default 3)

	// PIKA-V3: optional — registered write-ops for coverage check (F7-4)
	registeredWriteOps []string
}

// brainAutoEventMap contains hardcoded BRAIN tool → event type mappings.
var brainAutoEventMap = map[string]string{
	"search_memory.search":      "memory_search",
	"registry_write.write":      "registry_write",
	"registry_write.write_fail": "registry_write_fail",
	"clarify.ask":               "clarify_ask",
	"clarify.ask_manager":       "clarify_ask_manager",
}

// brainAutoTagMap contains hardcoded BRAIN tool → tags mappings.
var brainAutoTagMap = map[string][]string{
	"search_memory.search": {
		"tool:search_memory", "op:search",
	},
	"registry_write.write": {
		"tool:registry_write", "op:write",
	},
	"registry_write.write_fail": {
		"tool:registry_write", "op:write", "result:fail",
	},
	"clarify.ask":         {"tool:clarify", "op:ask"},
	"clarify.ask_manager": {"tool:clarify", "op:ask_manager"},
}

// NewAutoEventHandler creates an AutoEventHandler with merged mappings.
// toolTypeMap and toolTagMap come from filesystem loader (autoEvent.json).
// BRAIN mappings are merged automatically.
func NewAutoEventHandler(
	bm *BotMemory,
	toolTypeMap map[string]string,
	toolTagMap map[string][]string,
	eventClasses EventClasses,
) *AutoEventHandler {
	// Merge BRAIN hardcoded mappings into copies
	merged := make(
		map[string]string,
		len(toolTypeMap)+len(brainAutoEventMap),
	)
	for k, v := range toolTypeMap {
		merged[k] = v
	}
	for k, v := range brainAutoEventMap {
		merged[k] = v
	}

	mergedTags := make(
		map[string][]string,
		len(toolTagMap)+len(brainAutoTagMap),
	)
	for k, v := range toolTagMap {
		mergedTags[k] = v
	}
	for k, v := range brainAutoTagMap {
		mergedTags[k] = v
	}

	// Build validTypes set from all event type values
	valid := make(map[string]bool)
	for _, et := range merged {
		valid[et] = true
	}

	return &AutoEventHandler{
		bm:             bm,
		toolTypeMap:    merged,
		toolTagMap:     mergedTags,
		eventClasses:   eventClasses,
		validTypes:     valid,
		dedupThreshold: 3,
	}
}

// SetRegisteredWriteOps sets the list of registered write-ops
// for coverage validation (F7-4). Called by wiring code at startup.
func (h *AutoEventHandler) SetRegisteredWriteOps(ops []string) {
	h.registeredWriteOps = ops
}

// HandleToolResult is called after each tool call from loop.go.
// It deterministically maps tool actions to events and persists them.
func (h *AutoEventHandler) HandleToolResult(
	ctx context.Context,
	toolName string,
	operation string,
	isError bool,
	sessionID string,
	turnID int,
) error {
	// 1. Build key: toolName.operation (+ _fail suffix if isError)
	key := toolName + "." + operation
	if isError {
		failKey := key + "_fail"
		if _, ok := h.toolTypeMap[failKey]; ok {
			key = failKey
		}
		// If fail key not found, use base key
		// (heartbeat escalation case)
	}

	// 2. Lookup eventType in toolTypeMap
	eventType, ok := h.toolTypeMap[key]
	if !ok {
		// Read-only operations not mapped → skip silently
		return nil
	}

	// 3. Runtime guard: reject invalid types
	if !h.validTypes[eventType] {
		log.Printf(
			"WARN pika/autoevent: invalid event type %q "+
				"for key %q, dropping",
			eventType, key,
		)
		return nil
	}

	// 4. Entropy filter — consecutive dedup
	if !h.checkAndRecordEvent(eventType) {
		return nil
	}

	// 5. Determine outcome
	outcome := "success"
	if isError {
		outcome = "fail"
	}
	summary := toolName + "." + operation

	// 6. Event class routing
	if h.eventClasses.Heartbeat[eventType] {
		if isError {
			// Fail escalates heartbeat to critical INSERT
			return h.insertEvent(
				ctx, sessionID, turnID,
				eventType, summary, outcome, key,
			)
		}
		h.incrementHeartbeat(eventType)
		return nil
	}

	// Critical, Diagnostic, or unknown class → INSERT
	return h.insertEvent(
		ctx, sessionID, turnID,
		eventType, summary, outcome, key,
	)
}

// FlushHeartbeats writes summary heartbeat events on session rotation.
// Called by loop.go when rotating sessions.
func (h *AutoEventHandler) FlushHeartbeats(
	ctx context.Context,
	sessionID string,
	turnID int,
) error {
	var flushErr error
	h.heartbeatCtrs.Range(func(key, value any) bool {
		eventType := key.(string)
		counter := value.(*int64)
		count := atomic.SwapInt64(counter, 0)
		if count <= 0 {
			return true
		}
		summary := fmt.Sprintf(
			"heartbeat: %s x%d", eventType, count,
		)
		tagsJSON, _ := json.Marshal(
			[]string{"heartbeat", "aggregated"},
		)
		_, err := h.bm.SaveEvent(ctx, EventRow{
			Type:      eventType,
			Summary:   summary,
			Outcome:   "success",
			Tags:      tagsJSON,
			SessionID: sessionID,
			TurnID:    turnID,
		})
		if err != nil {
			flushErr = fmt.Errorf(
				"pika/autoevent: flush heartbeat %q: %w",
				eventType, err,
			)
			return false
		}
		return true
	})

	// Reset entropy filter on rotation
	h.mu.Lock()
	h.recentTypes = nil
	h.mu.Unlock()

	return flushErr
}

// ValidateStartup checks mapping consistency at startup.
// Returns a list of warning strings. Empty = all ok.
func (h *AutoEventHandler) ValidateStartup() []string {
	var warnings []string

	// 1. validTypes already built in constructor

	// 2. Each eventType in toolTypeMap should be in an event class
	for key, eventType := range h.toolTypeMap {
		if !h.eventClasses.Critical[eventType] &&
			!h.eventClasses.Diagnostic[eventType] &&
			!h.eventClasses.Heartbeat[eventType] {
			warnings = append(warnings, fmt.Sprintf(
				"event type %q (from %q) not in any event class",
				eventType, key,
			))
		}
	}

	// 3. Each eventType in eventClasses should be in toolTypeMap
	allValues := make(map[string]bool)
	for _, et := range h.toolTypeMap {
		allValues[et] = true
	}
	for et := range h.eventClasses.Critical {
		if !allValues[et] {
			warnings = append(warnings, fmt.Sprintf(
				"orphan critical class: %q not in "+
					"toolTypeMap values",
				et,
			))
		}
	}
	for et := range h.eventClasses.Diagnostic {
		if !allValues[et] {
			warnings = append(warnings, fmt.Sprintf(
				"orphan diagnostic class: %q not in "+
					"toolTypeMap values",
				et,
			))
		}
	}
	for et := range h.eventClasses.Heartbeat {
		if !allValues[et] {
			warnings = append(warnings, fmt.Sprintf(
				"orphan heartbeat class: %q not in "+
					"toolTypeMap values",
				et,
			))
		}
	}

	// 4. Coverage check (F7-4): unmapped write-ops
	if h.registeredWriteOps != nil {
		for _, op := range h.registeredWriteOps {
			if _, ok := h.toolTypeMap[op]; !ok {
				warnings = append(warnings, fmt.Sprintf(
					"unmapped write-op: %q not in toolTypeMap",
					op,
				))
			}
		}
	}

	return warnings
}

// checkAndRecordEvent checks consecutive dedup and records the event
// in the ring buffer. Returns true if the event should be persisted,
// false if it should be dropped (consecutive dup).
func (h *AutoEventHandler) checkAndRecordEvent(
	eventType string,
) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	n := len(h.recentTypes)
	if n >= h.dedupThreshold {
		allSame := true
		for i := n - h.dedupThreshold; i < n; i++ {
			if h.recentTypes[i] != eventType {
				allSame = false
				break
			}
		}
		if allSame {
			return false // drop — consecutive dup
		}
	}

	// Record this event type
	h.recentTypes = append(h.recentTypes, eventType)
	// Trim to keep memory bounded
	if len(h.recentTypes) > 100 {
		copy(h.recentTypes, h.recentTypes[50:])
		h.recentTypes = h.recentTypes[:50]
	}
	return true // allow
}

// incrementHeartbeat atomically increments the counter for eventType.
func (h *AutoEventHandler) incrementHeartbeat(eventType string) {
	val, _ := h.heartbeatCtrs.LoadOrStore(
		eventType, new(int64),
	)
	atomic.AddInt64(val.(*int64), 1)
}

// insertEvent persists an event via BotMemory.SaveEvent.
func (h *AutoEventHandler) insertEvent(
	ctx context.Context,
	sessionID string,
	turnID int,
	eventType string,
	summary string,
	outcome string,
	key string,
) error {
	var tagsJSON json.RawMessage
	if tags, ok := h.toolTagMap[key]; ok && len(tags) > 0 {
		tagsJSON, _ = json.Marshal(tags)
	}
	_, err := h.bm.SaveEvent(ctx, EventRow{
		Type:      eventType,
		Summary:   summary,
		Outcome:   outcome,
		Tags:      tagsJSON,
		SessionID: sessionID,
		TurnID:    turnID,
	})
	if err != nil {
		return fmt.Errorf("pika/autoevent: save event: %w", err)
	}
	return nil
}
