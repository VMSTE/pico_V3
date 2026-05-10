// PIKA-V3: PikaContextManager adapter and factory registration.
// Bridges pkg/pika.PikaContextManager to pkg/agent.ContextManager
// interface without circular imports.
//
// Phase В (ТЗ-v2-8j): registers 4 PromptContributors (MEMORY BRIEF,
// TRAIL, ACTIVE_PLAN, DEGRADATION) and returns empty SystemPrompt
// so pipeline falls through to upstream ContextBuilder path.

package agent

import (
  "context"
  "encoding/json"
  "fmt"
  "strings"

  "github.com/sipeed/picoclaw/pkg/config"
  "github.com/sipeed/picoclaw/pkg/logger"
  "github.com/sipeed/picoclaw/pkg/pika"
  "github.com/sipeed/picoclaw/pkg/providers"
)

func init() {
  if err := RegisterContextManager(
    "pika", pikaContextManagerFactory,
  ); err != nil {
    logger.ErrorCF("agent",
      "Failed to register pika context manager",
      map[string]any{"error": err.Error()},
    )
  }
}

// pikaContextManagerFactory creates a PikaContextManager wrapped
// as agent.ContextManager. Signature matches ContextManagerFactory.
func pikaContextManagerFactory(
  _ json.RawMessage, al *AgentLoop,
) (ContextManager, error) {
  agent := al.registry.GetDefaultAgent()
  if agent == nil {
    return nil, fmt.Errorf(
      "pika/cm: no default agent configured",
    )
  }

  trail := pika.NewTrail()
  meta := pika.NewMeta()
  sp := pika.NewAlwaysHealthyProvider()
  planStore := pika.NewActivePlanStore()

  // PIKA-V3 wave 4: Get BotMemory from PikaSessionStore
  var botmem *pika.BotMemory
  if ps, ok := agent.Sessions.(*pika.PikaSessionStore); ok {
    botmem = ps.GetBotMemory()
  }

  // PIKA-V3 wave 4: Try to create real Archivist.
  // Only uses dedicated "background" model — no fallback to main
  // model to avoid interfering with test mock servers.
  var arch pika.ArchivistCaller
  if botmem != nil && al.cfg != nil {
    archProvider := resolveArchivistProvider(al.cfg)
    if archProvider != nil {
      arch = pika.NewArchivist(
        botmem, archProvider, trail, meta,
        pika.DefaultArchivistConfig(),
      )
      logger.InfoCF("pika",
        "Real Archivist wired successfully",
        nil,
      )
    }

    // PIKA-V3: Store BotMemory ref for RAD reasoning access (TZ-v2-8i).
    al.botmem = botmem

    // PIKA-V3: Mount AutoEvent EventObserver hook (D-136a, TZ-v2-8i, F14).
    autoHandler := pika.NewAutoEventHandler(botmem, nil, nil, pika.EventClasses{})
    _ = al.MountHook(HookRegistration{
      Name: "autoevent",
      Hook: &autoEventAdapter{handler: autoHandler},
    })
  }
  if arch == nil {
    arch = pika.NewNoopArchivistCaller()
    logger.InfoCF("pika",
      "Using NoopArchivist (no background model)",
      nil,
    )
  }

  cm := pika.NewPikaContextManager(
    agent.Workspace, trail, meta, sp, arch,
  )

  // PIKA-V3 wave 4: Wire BotMemory and PlanStore
  if botmem != nil {
    cm.SetBotMemory(botmem)
  }
  cm.SetPlanStore(planStore)

  // --- Phase В (ТЗ-v2-8j): create adapter and register PromptContributors ---
  adapter := &pikaContextManagerAdapter{
    cm: cm,
    al: al,
  }

  // Register 4 Pika PromptContributors on the agent's ContextBuilder.
  // These provide MEMORY BRIEF, TRAIL, ACTIVE_PLAN, DEGRADATION via the
  // upstream PromptRegistry (else-branch in pipeline_setup.go).
  for _, c := range []PromptContributor{
    &pikaMemoryBriefContributor{adapter: adapter},
    &pikaTrailContributor{cm: cm},
    &pikaActivePlanContributor{adapter: adapter},
    &pikaDegradationContributor{cm: cm},
  } {
    if err := agent.ContextBuilder.RegisterPromptContributor(c); err != nil {
      logger.WarnCF("pika",
        "Failed to register PromptContributor",
        map[string]any{
          "source": string(c.PromptSource().ID),
          "error":  err.Error(),
        },
      )
    }
  }

  logger.InfoCF("pika",
    "PikaContextManager initialized (Phase B — upstream path)",
    map[string]any{"workspace": agent.Workspace},
  )

  return adapter, nil
}

// resolveArchivistProvider creates an LLM provider for the
// "background" model from config. Returns nil if the model
// is not configured.
func resolveArchivistProvider(
  cfg *config.Config,
) providers.LLMProvider {
  mc, err := cfg.GetModelConfig("background")
  if err != nil {
    return nil
  }
  p, _, pErr := providers.CreateProviderFromConfig(mc)
  if pErr != nil {
    logger.WarnCF("pika",
      "Archivist provider creation failed",
      map[string]any{"error": pErr.Error()},
    )
    return nil
  }
  return p
}

// ---------------------------------------------------------------------------
// pikaContextManagerAdapter
// ---------------------------------------------------------------------------

type pikaContextManagerAdapter struct {
  cm *pika.PikaContextManager
  al *AgentLoop

  // lastSessionKey is stored during Assemble so that PromptContributors
  // (which don't receive sessionKey in PromptBuildRequest) can access it.
  lastSessionKey string
}

// Assemble returns history and summary from PikaSessionStore.
// SystemPrompt is intentionally empty — pipeline_setup.go falls through
// to the upstream ContextBuilder path where PromptRegistry.Collect()
// invokes our registered PromptContributors.
func (a *pikaContextManagerAdapter) Assemble(
  ctx context.Context, req *AssembleRequest,
) (*AssembleResponse, error) {
  agent := a.al.registry.GetDefaultAgent()
  if agent == nil {
    return &AssembleResponse{}, nil
  }

  // Store sessionKey for PromptContributors.
  a.lastSessionKey = req.SessionKey

  // Get history from PikaSessionStore (SQLite)
  history := agent.Sessions.GetHistory(req.SessionKey)
  summary := agent.Sessions.GetSummary(req.SessionKey)

  // Phase В: SystemPrompt intentionally empty.
  // Our layers (MEMORY BRIEF, TRAIL, ACTIVE_PLAN, DEGRADATION) are
  // provided via PromptContributors in the upstream else-branch.
  return &AssembleResponse{
    History: history,
    Summary: summary,
    // SystemPrompt: "" -> falls to upstream ContextBuilder path
  }, nil
}

func (a *pikaContextManagerAdapter) Compact(
  _ context.Context, req *CompactRequest,
) error {
  return a.cm.Compact(
    req.SessionKey, string(req.Reason),
  )
}

func (a *pikaContextManagerAdapter) Ingest(
  _ context.Context, req *IngestRequest,
) error {
  return a.cm.Ingest(req.SessionKey)
}

func (a *pikaContextManagerAdapter) Clear(
  _ context.Context, sessionKey string,
) error {
  return a.cm.Clear(sessionKey)
}

// ---------------------------------------------------------------------------
// PromptContributors — Phase В (ТЗ-v2-8j)
// ---------------------------------------------------------------------------

// --- MEMORY BRIEF ---

type pikaMemoryBriefContributor struct {
  adapter *pikaContextManagerAdapter
}

func (c *pikaMemoryBriefContributor) PromptSource() PromptSourceDescriptor {
  return PromptSourceDescriptor{
    ID:              "pika:memory_brief",
    Owner:           "pika",
    Description:     "Archivist-assembled memory brief",
    Allowed:         []PromptPlacement{{Layer: PromptLayerContext, Slot: PromptSlotMemory}},
    StableByDefault: false,
  }
}

func (c *pikaMemoryBriefContributor) ContributePrompt(
  ctx context.Context, _ PromptBuildRequest,
) ([]PromptPart, error) {
  sk := c.adapter.lastSessionKey
  if sk == "" {
    return nil, nil
  }
  result, err := c.adapter.cm.GetArchivist().BuildPrompt(
    ctx, pika.ArchivistInput{SessionKey: sk},
  )
  if err != nil || result == nil || strings.TrimSpace(result.BriefText) == "" {
    return nil, nil
  }
  return []PromptPart{{
    ID:      "context.pika_memory_brief",
    Layer:   PromptLayerContext,
    Slot:    PromptSlotMemory,
    Source:  PromptSource{ID: "pika:memory_brief", Name: "pika:archivist"},
    Title:   "memory brief",
    Content: "--- MEMORY BRIEF ---\n" + result.BriefText,
    Stable:  false,
    Cache:   PromptCacheEphemeral,
  }}, nil
}

// --- TRAIL ---

type pikaTrailContributor struct {
  cm *pika.PikaContextManager
}

func (c *pikaTrailContributor) PromptSource() PromptSourceDescriptor {
  return PromptSourceDescriptor{
    ID:              "pika:trail",
    Owner:           "pika",
    Description:     "Recent tool call trail (ring buffer)",
    Allowed:         []PromptPlacement{{Layer: PromptLayerContext, Slot: PromptSlotRuntime}},
    StableByDefault: false,
  }
}

func (c *pikaTrailContributor) ContributePrompt(
  _ context.Context, _ PromptBuildRequest,
) ([]PromptPart, error) {
  trail := c.cm.GetTrail()
  if trail == nil {
    return nil, nil
  }
  text := trail.Serialize()
  if strings.TrimSpace(text) == "" {
    return nil, nil
  }
  return []PromptPart{{
    ID:      "context.pika_trail",
    Layer:   PromptLayerContext,
    Slot:    PromptSlotRuntime,
    Source:  PromptSource{ID: "pika:trail", Name: "pika:trail"},
    Title:   "tool call trail",
    Content: text,
    Stable:  false,
    Cache:   PromptCacheNone,
  }}, nil
}

// --- ACTIVE_PLAN ---

type pikaActivePlanContributor struct {
  adapter *pikaContextManagerAdapter
}

func (c *pikaActivePlanContributor) PromptSource() PromptSourceDescriptor {
  return PromptSourceDescriptor{
    ID:              "pika:active_plan",
    Owner:           "pika",
    Description:     "Active plan extracted from reasoning",
    Allowed:         []PromptPlacement{{Layer: PromptLayerContext, Slot: PromptSlotMemory}},
    StableByDefault: false,
  }
}

func (c *pikaActivePlanContributor) ContributePrompt(
  ctx context.Context, _ PromptBuildRequest,
) ([]PromptPart, error) {
  sk := c.adapter.lastSessionKey
  if sk == "" {
    return nil, nil
  }
  text := c.adapter.cm.ExtractActivePlan(ctx, sk)
  // Update PlanStore for wave 4 compatibility.
  if ps := c.adapter.cm.GetPlanStore(); ps != nil {
    ps.SetActivePlan(text)
  }
  if strings.TrimSpace(text) == "" {
    return nil, nil
  }
  return []PromptPart{{
    ID:      "context.pika_active_plan",
    Layer:   PromptLayerContext,
    Slot:    PromptSlotMemory,
    Source:  PromptSource{ID: "pika:active_plan", Name: "pika:plan"},
    Title:   "active plan",
    Content: "--- ACTIVE_PLAN ---\n" + text,
    Stable:  false,
    Cache:   PromptCacheEphemeral,
  }}, nil
}

// --- DEGRADATION ---

type pikaDegradationContributor struct {
  cm *pika.PikaContextManager
}

func (c *pikaDegradationContributor) PromptSource() PromptSourceDescriptor {
  return PromptSourceDescriptor{
    ID:              "pika:degradation",
    Owner:           "pika",
    Description:     "System degradation status",
    Allowed:         []PromptPlacement{{Layer: PromptLayerContext, Slot: PromptSlotRuntime}},
    StableByDefault: false,
  }
}

func (c *pikaDegradationContributor) ContributePrompt(
  _ context.Context, _ PromptBuildRequest,
) ([]PromptPart, error) {
  text := c.cm.BuildDegradationBlock()
  if strings.TrimSpace(text) == "" {
    return nil, nil
  }
  return []PromptPart{{
    ID:      "context.pika_degradation",
    Layer:   PromptLayerContext,
    Slot:    PromptSlotRuntime,
    Source:  PromptSource{ID: "pika:degradation", Name: "pika:health"},
    Title:   "degradation",
    Content: text,
    Stable:  false,
    Cache:   PromptCacheNone,
  }}, nil
}
