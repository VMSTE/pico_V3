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
	"regexp"
	"strings"
	"sync"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/pika"
	"github.com/sipeed/picoclaw/pkg/providers"
)

func init() {
	if err := RegisterContextManager(
		"pika", pikaContextManagerFactory,
	); err != nil {
		logger.ErrorCF(
			"agent",
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
	// D-AUDIT-65: интерфейс — ниже заменяется на живую телеметрию
	var sp pika.SystemStateProvider = pika.NewAlwaysHealthyProvider()
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
			// PIKA-V3: Create DiagnosticsEngine (TZ-v2-9b).
			// PIKA-V3 (Р-3, D-AUDIT-50): живой отправитель + пути промтов.
			al.diag = pika.NewDiagnosticsEngine(
				botmem,
				pika.NewManagerSender(al.bus, al.cfg),
				pikaPromptPaths(agent.Workspace),
			)

			// PIKA-V3: Create Archivist with diagnostics (TZ-v2-9b).
			archCfg := mapArchivistConfig(al.cfg.ResolveAgentConfig("archivist"))
			archCfg.Model = resolveSatelliteModelID(al.cfg, archCfg.Model)
			realArch := pika.NewArchivist(
				botmem, archProvider, trail, meta,
				archCfg,
			)
			realArch.SetDiagnostics(al.diag)
			arch = realArch
			logger.InfoCF("pika", "Archivist + Diagnostics wired (TZ-v2-9b)", nil)

			// PIKA-V3: Create Atomizer pipeline (TZ-v2-9b).
			atomGen := pika.NewAtomIDGenerator(botmem)
			atomCfg := mapAtomizerConfig(al.cfg.ResolveAgentConfig("atomizer"))
			atomCfg.Model = resolveSatelliteModelID(al.cfg, atomCfg.Model)
			al.atomizer = pika.NewAtomizer(
				botmem, atomGen, archProvider, al.telemetry,
				atomCfg,
			)
			al.atomizer.SetDiagnostics(al.diag)
			logger.InfoCF("pika", "Atomizer wired (TZ-v2-9b)", nil)

			// PIKA-V3: Create Reflector pipeline for cron-driven reflection (TZ-v2-9b).
			reflCfg := mapReflectorConfig(al.cfg.ResolveAgentConfig("reflexor"))
			reflCfg.Model = resolveSatelliteModelID(al.cfg, reflCfg.Model)
			al.reflector = pika.NewReflectorPipeline(
				botmem, atomGen, archProvider, al.telemetry,
				reflCfg,
			)
			al.reflector.SetDiagnostics(al.diag)
			logger.InfoCF("pika", "Reflector wired (TZ-v2-9b)", nil)

			// PIKA-V3: Create MCPSecurity pipeline (TZ-v2-9b).
			guardCfg := mapMCPGuardConfig(al.cfg.ResolveAgentConfig("mcp_guard"))
			guardCfg.Model = resolveSatelliteModelID(al.cfg, guardCfg.Model)
			al.mcpSecurity = pika.NewMCPSecurityPipeline(
				guardCfg, mapMCPServerPolicies(al.cfg), al.telemetry,
			)
			al.mcpSecurity.SetDiagnostics(al.diag)
			logger.InfoCF("pika", "MCPSecurity wired (TZ-v2-9b)", nil)
		}

		// PIKA-V3: Store BotMemory ref for RAD reasoning access (TZ-v2-8i).
		al.botmem = botmem

		// PIKA-V3: Create and wire Telemetry (budget, health, cost) (TZ-v2-9a).
		// PIKA-V3 (D-AUDIT-65, D-HRL): живой отправитель уведомлений
		// менеджеру о деградации/восстановлении компонентов (было nil).
		var progressNotifier pika.ProgressNotifier
		if ms := pika.NewManagerSender(al.bus, al.cfg); ms != nil {
			progressNotifier = pika.ProgressObserverFactory(al.cfg, ms)
		}
		al.telemetry = pika.NewTelemetry(
			mapTelemetryConfig(al.cfg.Health, al.cfg.ResolveAgentConfig("main").Budget),
			botmem,
			progressNotifier,
		)

		// PIKA-V3: Mount AutoEvent EventObserver hook (D-136a, TZ-v2-8i, F14).
		// D-AUDIT-59: живые таблицы — MCP-секция по включённым серверам
		// + классы событий. До этого журнал работал с пустым блокнотом.
		var autoServerNames []string
		if al.cfg != nil {
			for name, srv := range al.cfg.Tools.MCP.Servers {
				if srv.Enabled {
					autoServerNames = append(autoServerNames, name)
				}
			}
		}
		autoTM, autoTG, autoClasses := pika.BuildAutoEventConfig(autoServerNames)
		autoHandler := pika.NewAutoEventHandler(botmem, autoTM, autoTG, autoClasses)
		for _, w := range autoHandler.ValidateStartup() {
			logger.WarnCF("pika", "autoevent startup validation: "+w, nil)
		}
		al.autoEvent = autoHandler
		_ = al.MountHook(HookRegistration{
			Name: "autoevent",
			Hook: &autoEventAdapter{handler: autoHandler},
		})
	}
	if arch == nil {
		arch = pika.NewNoopArchivistCaller()
		logger.InfoCF(
			"pika",
			"Using NoopArchivist (no background model)",
			nil,
		)
	}

	// PIKA-V3 (D-AUDIT-65, D-92, ТЗ-v2-2b): реальное состояние системы
	// вместо AlwaysHealthy-заглушки — блок DEGRADATION с таблицей
	// перенаправлений появляется при реальной деградации.
	if al.telemetry != nil {
		sp = al.telemetry
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
	agentCfg := al.cfg.ResolveAgentConfig(agent.Name)
	// PIKA-V3: Phase 6 — compile topic trigger regexes
	for _, pat := range agentCfg.TopicTriggers {
		if re, err := regexp.Compile(pat); err == nil {
			adapter.topicRegexes = append(adapter.topicRegexes, re)
		}
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
			logger.WarnCF(
				"pika",
				"Failed to register PromptContributor",
				map[string]any{
					"source": string(c.PromptSource().ID),
					"error":  err.Error(),
				},
			)
		}
	}

	logger.InfoCF(
		"pika",
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
		logger.WarnCF(
			"pika",
			"Archivist provider creation failed",
			map[string]any{"error": pErr.Error()},
		)
		return nil
	}
	return p
}

// resolveSatelliteModelID разрешает алиас модели (model_name из model_list)
// в реальный model ID для API — как это делает main-агент. Без этого спутники
// отправляли провайдеру сырой алиас («background is not a valid model ID»,
// бой 19 авг). Пустой/неизвестный — возвращаем как есть.
func resolveSatelliteModelID(cfg *config.Config, modelName string) string {
	if cfg == nil || strings.TrimSpace(modelName) == "" {
		return modelName
	}
	mc, err := cfg.GetModelConfig(modelName)
	if err != nil || mc == nil {
		return modelName
	}
	_, modelID := providers.ExtractProtocol(mc)
	if strings.TrimSpace(modelID) == "" {
		return modelName
	}
	return modelID
}

// ---------------------------------------------------------------------------
// pikaContextManagerAdapter
// ---------------------------------------------------------------------------

type pikaContextManagerAdapter struct {
	cm *pika.PikaContextManager
	al *AgentLoop

	// lastSessionKey is stored during Assemble so that PromptContributors
	// (which don't receive sessionKey in PromptBuildRequest) can access it.
	mu             sync.RWMutex // D-AUDIT-101: guards lastSessionKey (parallel turns)
	lastSessionKey string

	// PIKA-V3: Phase 6
	topicRegexes     []*regexp.Regexp
	rotateRegistered sync.Map
}

// D-AUDIT-101: parallel turns (different sessions) share one adapter —
// lastSessionKey access must be synchronized (race found by CI -race).
func (a *pikaContextManagerAdapter) setLastSessionKey(sk string) {
	a.mu.Lock()
	a.lastSessionKey = sk
	a.mu.Unlock()
}

func (a *pikaContextManagerAdapter) sessionKey() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastSessionKey
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
	a.setLastSessionKey(req.SessionKey)

	// PIKA-V3: Phase 6 — lazy-register OnRotate → InvalidateBrief
	if _, loaded := a.rotateRegistered.LoadOrStore(req.SessionKey, true); !loaded {
		if ps, ok := agent.Sessions.(*pika.PikaSessionStore); ok {
			if sl := ps.Session(req.SessionKey); sl != nil {
				sl.OnRotate(func(_ string) {
					a.cm.GetArchivist().InvalidateBrief()
				})
			}
		}
	}

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
	ctx context.Context, req PromptBuildRequest,
) ([]PromptPart, error) {
	sk := c.adapter.sessionKey()
	if sk == "" {
		return nil, nil
	}
	// PIKA-V3: Phase 6 — topic trigger → InvalidateBrief
	for _, re := range c.adapter.topicRegexes {
		if re.MatchString(req.CurrentMessage) {
			c.adapter.cm.GetArchivist().InvalidateBrief()
			break
		}
	}

	// PIKA-V3: collect tool & skill catalogs for Archivist
	agent := c.adapter.al.registry.GetDefaultAgent()
	var toolCat, skillCat []string
	if agent != nil {
		// D-AUDIT-60: имена + описания (GetSummaries), не голые имена.
		// Это тулы ОСНОВНОЙ МОДЕЛИ — search_context сюда не входит.
		toolCat = agent.Tools.GetSummaries()
		skillCat = agent.ContextBuilder.ListSkillNames()
	}
	result, err := c.adapter.cm.GetArchivist().BuildPrompt(
		ctx, pika.ArchivistInput{
			SessionKey:           sk,
			Message:              req.CurrentMessage, // D-AUDIT-60: раньше не передавалось!
			ToolCatalog:          toolCat,
			SkillCatalog:         skillCat,
			ActivePlan:           c.adapter.cm.ExtractActivePlan(ctx, sk),
			MaxRecommendedTools:  c.adapter.al.cfg.ToolSelection.MaxRecommendedTools,
			MaxRecommendedSkills: c.adapter.al.cfg.ToolSelection.MaxRecommendedSkills,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("pika/archivist: BuildPrompt: %w", err)
	}
	// Волна 88 (бой 20 авг): пустой бриф — отсутствие вклада, а не
	// ошибка. Промт Архивариуса легально разрешает пустые блоки при
	// пустом поиске; hard error выкидывал весь MEMORY BRIEF и шумел
	// в логах на каждый пустой результат.
	if result == nil || strings.TrimSpace(result.BriefText) == "" {
		return nil, nil
	}
	// PIKA-V3: Progressive Disclosure — promote recommended tools (Block B2)
	if c.adapter.al.cfg.ToolSelection.Enabled && agent != nil &&
		len(result.RecommendedTools) > 0 {
		agent.Tools.PromoteTools(result.RecommendedTools, 2)
	}
	// PIKA-V3: build content with brief + recommended tools/skills
	content := "--- MEMORY BRIEF ---\n" + result.BriefText
	if len(result.RecommendedTools) > 0 {
		content += "\n--- RECOMMENDED TOOLS ---\n" +
			strings.Join(result.RecommendedTools, ", ")
	}
	if len(result.RecommendedSkills) > 0 {
		content += "\n--- RECOMMENDED SKILLS ---\n" +
			strings.Join(result.RecommendedSkills, ", ")
	}
	return []PromptPart{{
		ID:      "context.pika_memory_brief",
		Layer:   PromptLayerContext,
		Slot:    PromptSlotMemory,
		Source:  PromptSource{ID: "pika:memory_brief", Name: "pika:archivist"},
		Title:   "memory brief",
		Content: content,
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
	ctx context.Context, req PromptBuildRequest,
) ([]PromptPart, error) {
	sk := c.adapter.sessionKey()
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
