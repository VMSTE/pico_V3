// PIKA-V3: active_plan.go — ACTIVE_PLAN extraction from
// reasoning text + thread-safe store.
// Go regex parses <plan>...</plan> blocks from LLM reasoning.
// Last <plan> block wins. Decision: D-NEW-2/D-NEW-3.

package pika

import (
	"regexp"
	"strings"
	"sync"
)

// planRegex matches <plan>...</plan> blocks in reasoning text.
// (?s) enables dotall mode so . matches newlines.
var planRegex = regexp.MustCompile(`(?s)<plan>(.*?)</plan>`)

// ExtractActivePlan returns the content of the last <plan>
// block from reasoningText. Returns "" if no block found.
// Multiple <plan> blocks → last one wins (model refines plan).
func ExtractActivePlan(reasoningText string) string {
	matches := planRegex.FindAllStringSubmatch(
		reasoningText, -1,
	)
	if len(matches) == 0 {
		return ""
	}
	return strings.TrimSpace(
		matches[len(matches)-1][1],
	)
}

// ActivePlanStore is a thread-safe store for the current
// ACTIVE_PLAN text. Updated by PikaContextManager after
// extraction, read by ToolGuard via ActivePlanGetter.
type ActivePlanStore struct {
	mu   sync.RWMutex
	plan string
}

// NewActivePlanStore creates an empty ActivePlanStore.
func NewActivePlanStore() *ActivePlanStore {
	return &ActivePlanStore{}
}

// SetActivePlan updates the stored plan text.
func (s *ActivePlanStore) SetActivePlan(plan string) {
	s.mu.Lock()
	s.plan = plan
	s.mu.Unlock()
}

// GetActivePlan returns the current plan text.
// Implements ActivePlanGetter (toolguard.go).
func (s *ActivePlanStore) GetActivePlan() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.plan
}

// Compile-time check: ActivePlanStore implements
// ActivePlanGetter.
var _ ActivePlanGetter = (*ActivePlanStore)(nil)
