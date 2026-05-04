package pika

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
)

// PIKA-V3: AutoEventHandler — deterministic event generator
// after tool calls.
// Zero LLM. Go maps tool action -> event type -> tags -> INSERT.

// EventClasses defines three tiers of event persistence.
type EventClasses struct {
	Critical   map[string]bool // always persist
	Diagnostic map[string]bool // persist + aggregate
	Heartbeat  map[string]bool // in-memory counter
}

// AutoEventHandler records events after tool calls.
type AutoEventHandler struct {
	bm            *BotMemory
	toolTypeMap   map[string]string
	toolTagMap    map[string][]string
	eventClasses  EventClasses
	validTypes    map[string]bool
	heartbeatCtrs sync.Map // eventType -> *int64

	// PIKA-V3: entropy filter — consecutive dedup
	mu             sync.Mutex
	recentTypes    []string
	dedupThreshold int

	// optional — registered write-ops for coverage (F7-4)
	registeredWriteOps []string
}

// brainAutoEventMap: hardcoded BRAIN tool -> event type.
var brainAutoEventMap = map[string]string{
	"search_memory.search":      "memory_search",
	"registry_write.write":      "registry_write",
	"registry_write.write_fail": "registry_write_fail",
	"clarify.ask":               "clarify_ask",
	"clarify.ask_manager":       "clarify_ask_manager",
}

// brainAutoTagMap: hardcoded BRAIN tool -> tags.
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

// NewAutoEventHandler creates handler with merged mappings.
func NewAutoEventHandler(
	bm *BotMemory,
	toolTypeMap map[string]string,
	toolTagMap map[string][]string,
	eventClasses EventClasses,
) *AutoEventHandler {
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

// SetRegisteredWriteOps sets write-ops for coverage (F7-4).
func (h *AutoEventHandler) SetRegisteredWriteOps(
	ops []string,
) {
	h.registeredWriteOps = ops
}

// HandleToolResult is called after each tool call from loop.go.
func (h *AutoEventHandler) HandleToolResult(
	ctx context.Context,
	toolName string,
	operation string,
	isError bool,
	sessionID string,
	turnID int,
) error {
	// 1. Build key
	key := toolName + "." + operation
	if isError {
		failKey := key + "_fail"
		if _, ok := h.toolTypeMap[failKey]; ok {
			key = failKey
		}
	}

	// 2. Lookup eventType
	eventType, ok := h.toolTypeMap[key]
	if !ok {
		return nil
	}

	// 3. Runtime guard
	if !h.validTypes[eventType] {
		log.Printf(
			"WARN pika/autoevent: invalid event type %q "+
				"for key %q, dropping",
			eventType, key,
		)
		return nil
	}

	outcome := "success"
	if isError {
		outcome = "fail"
	}
	summary := toolName + "." + operation

	// 4. Heartbeat routing BEFORE entropy filter.
	// Heartbeats don't write to DB — dedup not needed.
	if h.eventClasses.Heartbeat[eventType] {
		if isError {
			// Fail escalates heartbeat to critical INSERT
			if !h.checkAndRecordEvent(eventType) {
				return nil
			}
			return h.insertEvent(
				ctx, sessionID, turnID,
				eventType, summary, outcome, key,
			)
		}
		h.incrementHeartbeat(eventType)
		return nil
	}

	// 5. Entropy filter for DB writes
	if !h.checkAndRecordEvent(eventType) {
		return nil
	}

	// 6. Critical / Diagnostic / default -> INSERT
	return h.insertEvent(
		ctx, sessionID, turnID,
		eventType, summary, outcome, key,
	)
}

// FlushHeartbeats writes summary events on session rotation.
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
func (h *AutoEventHandler) ValidateStartup() []string {
	var warnings []string

	for key, eventType := range h.toolTypeMap {
		if !h.eventClasses.Critical[eventType] &&
			!h.eventClasses.Diagnostic[eventType] &&
			!h.eventClasses.Heartbeat[eventType] {
			warnings = append(warnings, fmt.Sprintf(
				"event type %q (from %q) "+
					"not in any event class",
				eventType, key,
			))
		}
	}

	allValues := make(map[string]bool)
	for _, et := range h.toolTypeMap {
		allValues[et] = true
	}
	for et := range h.eventClasses.Critical {
		if !allValues[et] {
			warnings = append(warnings, fmt.Sprintf(
				"orphan critical class: %q "+
					"not in toolTypeMap values",
				et,
			))
		}
	}
	for et := range h.eventClasses.Diagnostic {
		if !allValues[et] {
			warnings = append(warnings, fmt.Sprintf(
				"orphan diagnostic class: %q "+
					"not in toolTypeMap values",
				et,
			))
		}
	}
	for et := range h.eventClasses.Heartbeat {
		if !allValues[et] {
			warnings = append(warnings, fmt.Sprintf(
				"orphan heartbeat class: %q "+
					"not in toolTypeMap values",
				et,
			))
		}
	}

	if h.registeredWriteOps != nil {
		for _, op := range h.registeredWriteOps {
			if _, ok := h.toolTypeMap[op]; !ok {
				warnings = append(warnings, fmt.Sprintf(
					"unmapped write-op: %q "+
						"not in toolTypeMap",
					op,
				))
			}
		}
	}

	return warnings
}

// checkAndRecordEvent checks consecutive dedup.
// Returns true if event should be persisted.
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
			return false
		}
	}

	h.recentTypes = append(h.recentTypes, eventType)
	if len(h.recentTypes) > 100 {
		copy(
			h.recentTypes,
			h.recentTypes[50:],
		)
		h.recentTypes = h.recentTypes[:50]
	}
	return true
}

func (h *AutoEventHandler) incrementHeartbeat(
	eventType string,
) {
	val, _ := h.heartbeatCtrs.LoadOrStore(
		eventType, new(int64),
	)
	atomic.AddInt64(val.(*int64), 1)
}

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
		return fmt.Errorf(
			"pika/autoevent: save event: %w", err,
		)
	}
	return nil
}
