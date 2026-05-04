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

// PIKA-V3: Archivist input/output types (wave 3).

// ArchivistInput holds the parameters for an Archivist
// BuildPrompt call.
type ArchivistInput struct {
	SessionKey string
	Message    string
	IsRotation bool
}

// Focus represents the current task focus — 6 fields (D-55).
type Focus struct {
	Task        string   `json:"task"`
	Step        string   `json:"step"`
	Mode        string   `json:"mode"`
	Blocked     *string  `json:"blocked"`
	Constraints []string `json:"constraints"`
	Decisions   []string `json:"decisions"`
}

// MemoryBrief represents the 4-section memory brief (D-65).
// Priority: AVOID > CONSTRAINTS > PREFER > CONTEXT.
type MemoryBrief struct {
	Avoid       []string `json:"avoid"`
	Constraints []string `json:"constraints"`
	Prefer      []string `json:"prefer"`
	Context     []string `json:"context"`
}

// ArchivistResult holds the structured output from the Archivist.
type ArchivistResult struct {
	Focus     Focus
	Brief     MemoryBrief
	BriefText string   // serialized text for system prompt
	ToolSet   []string
}

// ArchivistCaller abstracts the Archivist service for building
// the MEMORY BRIEF section of the system prompt.
// Used by PikaContextManager in BuildSystemPrompt.
type ArchivistCaller interface {
	BuildPrompt(
		ctx context.Context, input ArchivistInput,
	) (*ArchivistResult, error)
}

// noopArchivistCaller is an ArchivistCaller that always returns
// an empty brief. Used as stub when real Archivist is not wired.
type noopArchivistCaller struct{}

func (noopArchivistCaller) BuildPrompt(
	_ context.Context, _ ArchivistInput,
) (*ArchivistResult, error) {
	return &ArchivistResult{}, nil
}

// NewNoopArchivistCaller returns an ArchivistCaller stub
// that always returns an empty MEMORY BRIEF.
func NewNoopArchivistCaller() ArchivistCaller {
	return noopArchivistCaller{}
}
