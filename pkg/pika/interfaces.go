package pika

import "context"

// SystemState represents the system health state for META.HEALTH.
// PIKA-V3: struct instead of plain string — extensible for future fields.
type SystemState struct {
	Status             string   // "healthy" | "degraded" | "offline"
	DegradedComponents []string // names of degraded components (empty when healthy)
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
	GetSystemState() SystemState
}

// alwaysHealthyProvider is a stub that always returns StateHealthy.
// Used as the default until a real provider is wired (wave 3+).
type alwaysHealthyProvider struct{}

func (alwaysHealthyProvider) GetSystemState() SystemState {
	return StateHealthy
}

// NewAlwaysHealthyProvider returns a SystemStateProvider stub
// that always reports healthy.
func NewAlwaysHealthyProvider() SystemStateProvider {
	return alwaysHealthyProvider{}
}

// ArchivistCaller abstracts the Archivist service for building
// the MEMORY BRIEF section of the system prompt.
// Used by PikaContextManager in BuildSystemPrompt.
type ArchivistCaller interface {
	BuildPrompt(ctx context.Context, sessionKey string) (string, error)
}

// noopArchivistCaller is an ArchivistCaller that always returns
// an empty brief. Used as stub in wave 2 until real Archivist
// is wired (wave 3+).
type noopArchivistCaller struct{}

func (noopArchivistCaller) BuildPrompt(
	_ context.Context, _ string,
) (string, error) {
	return "", nil
}

// NewNoopArchivistCaller returns an ArchivistCaller stub
// that always returns an empty MEMORY BRIEF.
func NewNoopArchivistCaller() ArchivistCaller {
	return noopArchivistCaller{}
}
