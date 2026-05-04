package pika

import "context"

// SystemState represents the system health state for META.HEALTH.
// PIKA-V3: struct instead of plain string — extensible for future fields.
type SystemState struct {
	Status string // "healthy" | "degraded" | "offline"
}

// Pre-defined system states.
var (
	StateHealthy  = SystemState{Status: "healthy"}
	StateDegraded = SystemState{Status: "degraded"}
	StateOffline  = SystemState{Status: "offline"}
)

// SystemStateProvider returns the current system health state.
// Used by PikaContextManager to embed health info into system prompt.
type SystemStateProvider interface {
	GetSystemState(ctx context.Context) SystemState
}

// alwaysHealthyProvider is a stub that always returns StateHealthy.
// Used as the default until a real provider is wired (wave 3+).
type alwaysHealthyProvider struct{}

func (alwaysHealthyProvider) GetSystemState(_ context.Context) SystemState {
	return StateHealthy
}

// NewAlwaysHealthyProvider returns a SystemStateProvider stub
// that always reports healthy.
func NewAlwaysHealthyProvider() SystemStateProvider {
	return alwaysHealthyProvider{}
}

// ArchivistCaller abstracts the Archivist summarization service.
// Used by PikaContextManager for context compaction.
type ArchivistCaller interface {
	Summarize(ctx context.Context, text string) (string, error)
}
