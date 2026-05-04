// PIKA-V3: Interfaces for dependency injection in PikaContextManager.
// Stubs provided for components not yet implemented.
// SystemStateProvider → real impl in wave 4 (telemetry.go).
// ArchivistCaller → real impl in wave 3 (archivist.go).

package pika

import "context"

// SystemStateProvider reports current system health.
// Implemented by telemetry.go in wave 4; stub in wave 2.
type SystemStateProvider interface {
	GetSystemState() SystemState
}

// SystemState holds the current system health status.
type SystemState struct {
	Status             string   // "healthy" | "degraded" | "offline"
	DegradedComponents []string // e.g. ["archivist", "mcp_guard"]
}

// alwaysHealthyProvider is a wave-2 stub: always returns healthy.
type alwaysHealthyProvider struct{}

func (p *alwaysHealthyProvider) GetSystemState() SystemState {
	return SystemState{Status: "healthy"}
}

// NewAlwaysHealthyProvider returns a stub SystemStateProvider
// that always reports healthy status (wave 2 default).
func NewAlwaysHealthyProvider() SystemStateProvider {
	return &alwaysHealthyProvider{}
}

// ArchivistCaller abstracts the Archivist sub-agent call.
// Implemented by archivist.go in wave 3; stub in wave 2.
type ArchivistCaller interface {
	// BuildPrompt calls the Archivist to produce MEMORY BRIEF.
	// Returns empty string on error or unavailable (degraded mode).
	BuildPrompt(
		ctx context.Context, sessionKey string,
	) (string, error)
}

// noopArchivistCaller is a wave-2 stub: returns empty MEMORY BRIEF.
type noopArchivistCaller struct{}

func (a *noopArchivistCaller) BuildPrompt(
	_ context.Context, _ string,
) (string, error) {
	return "", nil
}

// NewNoopArchivistCaller returns a stub ArchivistCaller
// that always returns empty MEMORY BRIEF (wave 2 default).
func NewNoopArchivistCaller() ArchivistCaller {
	return &noopArchivistCaller{}
}
