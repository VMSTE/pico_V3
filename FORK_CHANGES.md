# FORK_CHANGES — pico_V3

Tracker of all structural changes made in the Pika v3 fork vs upstream PicoClaw.
Each entry maps to a single wave/phase and its merged PR.

---

## Wave 0: Foundation (schema + config)

### [2026-05-01] feat(pika): add migrate.go — wave 0a

- **ТЗ:** ТЗ-v2-0a: migrate.go — Схема bot_memory.db
- **PR:** #1 (merged)
- **Files:**
  - `pkg/pika/migrate.go` — NEW: `Migrate(dbPath)` + `CurrentVersion(db)`, PRAGMAs (WAL, FK, cache, busy_timeout), migration v0→v1 full DDL from SSOT (17 tables, 4 triggers, all indexes), transactional via `schema_version`
  - `pkg/pika/migrate_test.go` — NEW: 4 tests (new DB, idempotency, pragmas, FTS5 MATCH smoke)
- **Breaking:** None (new package `pkg/pika/`)

### [2026-05-01] feat(pika): config.go unified config — wave 0b Phase 1

- **ТЗ:** ТЗ-v2-0b: config.go — Unified Config (pkg/config/)
- **PR:** #2 (merged)
- **Files:**
  - `pkg/config/config_pika.go` — NEW: 18 Pika v3 types
  - `pkg/config/config_pika_test.go` — NEW: tests
- **Breaking:** None (additive only)

### [2026-05-02] feat(pika): config.go struct patching + legacy cleanup — wave 0b Phase 2

- **ТЗ:** ТЗ-v2-0b-p2: config.go — Phase 2 (struct patching + legacy cleanup)
- **PR:** #5 (merged)
- **Breaking:** Config versions 0/1/2 no longer supported.

### [2026-05-02] feat(pika): post-merge cleanup — wave 0b Phase 3

- **ТЗ:** ТЗ-v2-0b-p3: config.go — Post-merge cleanup
- **PR:** #6 (merged)
- **Breaking:** LoadConfig() now returns error if memory_db_path is empty.

---

## Wave 1: CRUD Layer (botmemory + session store + registry)

### [2026-05-02] feat(pika): botmemory.go — CRUD layer for bot_memory.db — wave 1a

- **PR:** #7
- **Breaking:** None (new file, additive only)

### [2026-05-02] fix(pika): botmemory.go — 5 SQL bugs vs DDL — wave 1a-fix

- **PR:** #7 (updated)

### [2026-05-03] feat(pika): PikaSessionStore — wave 1b Phase 1+2

- **PR:** #9
- **Breaking:** `pkg/memory` package removed.

### [2026-05-03] test: skip 4 legacy tests — transitional (D-136)

- **PR:** #9, #12

### [2026-05-03] fix(pika): remove linux/arm + exotic archs from build-all — CI fix

- **PR:** #11, #13

### [2026-05-04] feat(pika): registry.go — Registry CRUD + AtomID generator — wave 1c

- **PR:** #14
- **Breaking:** None (new files, additive only)

---

## Wave 2: Runtime Components (TRAIL/META, envelope, context manager)

### [2026-05-04] feat(pika): trail_meta.go — TRAIL ring buffer + META metrics — wave 2a

- **PR:** #16

### [2026-05-04] feat(pika): PikaContextManager + delete Seahorse/legacy + cleanup pipeline — wave 2b

- **PR:** existing PR on `feat/v2-2b-context-manager` branch

### [2026-05-04] feat(pika): envelope.go — unified tool response envelope — wave 2c

- **Breaking:** None (new files, additive only)

### [2026-05-04] fix(pika): SystemPrompt bypass in pipeline_setup.go + cleanup

---

## Wave 3: Tools, Router, Archivist

### [2026-05-04] feat(pika): archivist.go — wave 3a

- **PR:** #21

### [2026-05-04] feat(pika): tool_router.go — wave 3b

- **PR:** #22

### [2026-05-04] feat(pika): memory_tools.go — wave 3c

- **PR:** #23

### [2026-05-04] feat(pika): clarify.go — wave 3d

- **PR:** #24

### [2026-05-04] feat(pika): autoevent.go — wave 3e

- **PR:** #TBD

---

## Wave 4: Pipeline Integration

### [2026-05-05] feat(pika): telemetry.go — wave 4f

- **PR:** #TBD

### [2026-05-05] feat(pika): session.go — wave 4b

- **PR:** #TBD

### [2026-05-05] feat(pika): toolguard.go — ToolGuard AfterLLM hook — wave 4c

- **ТЗ:** ТЗ-v2-4c: toolguard.go — ToolGuard (AfterLLM builtin hook)
- **PR:** #TBD
- **Files:**
  - `pkg/pika/toolguard.go` — NEW: `ToolGuard` struct with local hook types (HookAction, HookDecision, ToolGuardLLMResponse). `ActivePlanGetter` interface (implemented by PikaContextManager). `ToolGuardFactory(cfg, planGetter)` constructor. `AfterLLM(resp)` — detects missing tool call when ACTIVE_PLAN active: plan=="" → continue, HasToolCalls → continue, Content=="" → continue, retryCount≥max → continue (exhausted), otherwise → HookActionModify with reminder. `ResetTurn()` — per-turn retry counter reset. Max 1 retry. **No import of pkg/agent** (avoids import cycle via context_pika.go). Wiring adapter in instance.go (ТЗ-4a) converts local types ↔ agent.LLMInterceptor.
  - `pkg/pika/toolguard_test.go` — NEW: 8 tests (ActivePlanTextNoTools_Modify, ActivePlanWithToolCalls_Continue, NoPlan_Continue, RetryExhausted_Continue, EmptyResponse_Continue, NilPlanGetter_Continue, NilResponse_Continue, ResetTurn)
- **Breaking:** None (new files, additive only). Consumer: instance.go (ТЗ-4a) via wiring adapter
- **Dependencies:** `pkg/config` (ToolGuardFactory signature), `pkg/logger`

### [2026-05-05] feat(pika): confirm_gate.go — ConfirmGate ToolApprover hook — wave 4d

- **ТЗ:** ТЗ-v2-4d: confirm_gate.go — ConfirmGate (ToolApprover builtin hook)
- **PR:** #TBD
- **Files:**
  - `pkg/pika/confirm_gate.go` — NEW: `ConfirmGate` struct implementing ToolApprover (D-136a). Local types: `ConfirmApprovalRequest`, `ConfirmApprovalDecision` (mirror agent.ToolApprovalRequest/ApprovalDecision). `TelegramSender` interface (shared pattern with clarify.go ClarifySender). `ConfirmGateFactory(cfg, sender, health)` constructor. `ApproveTool(ctx, req)` — matches tool.operation against `security.dangerous_ops` config, evaluates confirm rules (always/if_healthy/if_critical_path/never), reflex for compose.restart+exited, sends Telegram confirmation, fail-closed on timeout/error. Helper functions: `getOperation`, `isExited`, `isInCriticalPath` (glob match), `extractPath`, `summarizeArgs`, `evaluateConfirmRule`. Uses existing `SystemStateProvider` from interfaces.go and `config.DangerousOpEntry`/`ConfirmMode` from config_pika.go. **No import of pkg/agent** (avoids import cycle).
  - `pkg/pika/confirm_gate_test.go` — NEW: 9 tests (DeployRequest_Approved, DeployRequest_Denied, ComposeRestart_Exited, ComposeRestart_Healthy, ComposeRestart_Degraded, FilesWrite_CriticalPath, FilesWrite_NonCritical, NotInTable, Timeout_Deny)
- **Breaking:** None (new files, additive only). Consumer: instance.go (ТЗ-4a) via wiring adapter
- **Dependencies:** `pkg/config` (SecurityConfig, DangerousOpsConfig, ConfirmMode), `pkg/logger`, `pkg/pika/interfaces.go` (SystemStateProvider)

---

## Wave 5: Sub-agents

### [2026-05-05] feat(pika): atomizer.go — Atomizer pipeline — wave 5a

- **ТЗ:** ТЗ-v2-5a: atomizer.go — Atomizer pipeline
- **PR:** #33 (merged)
- **Files:**
  - `pkg/pika/atomizer.go` — NEW: `Atomizer` struct — Go-pipeline extracting knowledge atoms from hot buffer. `AtomizerConfig` (D-133: trigger_tokens=800k, chunk_max_tokens=200k, prompt_file, max_retries=2, model=background). `DefaultAtomizerConfig()`. `AtomLLMOutput` / `atomizerLLMResponse` — LLM structured output types. `NewAtomizer(mem, atomGen, provider, telemetry, cfg)` constructor. `ShouldAtomize(ctx, sessionID)` — threshold check. `Run(ctx, sessionID)` — full pipeline: chunk selection (oldest turns ≤ budget) → hot-reload prompt (`os.ReadFile`) → LLM call (structured output, 0 tool calls) → parse+validate (category/polarity/confidence/source_turns) → retry loop (up to MaxRetries with REPAIR prompt on validation error) → INSERT atoms (via `AtomIDGenerator.Next` + `BotMemory.InsertAtom`) → archive+delete (1 txn via `BotMemory.ArchiveAndDeleteTurns`). Tags inherited from events per turn (D-75: `collectTagsByTurn` + `mergeTagsForTurns`). Telemetry: `ReportComponentFailure/ReportComponentSuccess`. Helper: `getMessagesByTurns` (same-package access to `BotMemory.db`). JSON extraction: `extractAtomizerJSON` + `extractBalanced`. Default prompt constant `defaultAtomizerPrompt`.
  - `pkg/pika/atomizer_test.go` — NEW: 16 tests
- **Breaking:** None (new files, additive only)

### [2026-05-05] feat(pika): reflector.go — Reflector pipeline — wave 5b

- **ТЗ:** ТЗ-v2-5b: reflector.go — Reflector pipeline
- **PR:** #34
- **Files:**
  - `pkg/pika/reflector.go` — NEW: `ReflectorPipeline` struct — Go-pipeline for behavioral optimization via cheap LLM (structured output, 0 tool calls). 3 modes (D-134): daily (1 day), weekly (7 days), monthly (full scan). 4 tasks: (1) Merge duplicates (D-147: polarity validation, 1 txn), (2) Pattern detection, (3) Confidence updates (D-59: clamp 0.0–1.0, no time decay F8-8), (4) Runbook drafts (D-87/F9-5). Monthly: crystallization + stale marking. Hot-reload prompt (D-90). Retry 1x on invalid JSON.
  - `pkg/pika/reflector_cron.go` — NEW: `RegisterReflectorJobs(cronSvc, pipeline, schedule)` — registers 3 cron jobs in upstream CronService. `HandleReflectorJob(pipeline, job)` — dispatches to pipeline.Run. `schedToCronExpr` — schedule string → cron expression conversion.
  - `pkg/pika/reflector_test.go` — NEW: 14+ tests (EmptyDB, ParseJSON, Validation, ConfidenceClamp, MergePolarityMismatch, MergeSuccess, RunbookDraft, DailyPipeline, PromptHotReload, CronExpr, RegisterJobs valid/empty/invalid, HandleJob)
- **Breaking:** None (new files, additive only)
- **Dependencies:** `pkg/pika/botmemory.go`, `pkg/pika/registry.go` (AtomIDGenerator), `pkg/pika/telemetry.go`, `pkg/providers`, `pkg/cron` (upstream as-is)

---

## Wave 6: Security

### [2026-05-05] feat(pika): rad.go — Reasoning Anomaly Detector — wave 6a

- **ТЗ:** ТЗ-v2-6a: rad.go — Reasoning Anomaly Detector
- **PR:** #TBD
- **Files:**
  - `pkg/pika/rad.go` — NEW: `RAD` struct — fast pre-action security gate on reasoning tokens (D-SEC-v2, Layer 6). 0 LLM, sync. Types: `RADVerdict` (safe/warning/anomaly), `RADResult` (verdict+score+detectors+reason), `RADConfig` (enabled, pattern keywords RU/EN, drift_threshold, block/warn scores), `RADSession` (minimal session view: last_tool_source, prev_keywords), `RADToolCall` (minimal pending call: name, risk_level). `DefaultRADConfig()` with production keywords. `NewRAD(cfg)` — compiles regex at creation (fail-fast on invalid patterns). `Analyze(ctx, reasoning, session, pendingCall)` — main entry point, runs 3 detectors: (1) Pattern Detector (+3): case-insensitive regex on configurable RU/EN keywords; (2) Drift Detector (+2): Jaccard keyword overlap < threshold after MCP call, skips non-MCP; (3) Escalation Detector (+2): red-risk action after MCP output. Scoring: ≥block_score(3)→ANOMALY, ≥warn_score(2)→WARNING, else SAFE. Helpers: `jaccardIndex`, `extractKeywords` (Unicode-aware tokenizer). autoEvent mapping: `rad.blocked`→`rad_anomaly`, `rad.warning`→`rad_warning` (critical class, defined in config toolTypeMap).
  - `pkg/pika/rad_test.go` — NEW: 15 tests (PatternDetect_RU, PatternDetect_EN, PatternDetect_CleanReasoning, DriftDetect_LowOverlap, DriftDetect_HighOverlap, DriftDetect_NonMCPSkip, EscalationDetect_RedAfterMCP, EscalationDetect_GreenAfterMCP, CompoundScoring_Safe, CompoundScoring_Warning, CompoundScoring_Anomaly, Disabled, JaccardIndex, ExtractKeywords, DriftPlusEscalation_Anomaly)
- **Breaking:** None (new files, additive only)
- **Dependencies:** None (standalone, 0 external imports from pkg/pika)

### [2026-05-06] feat(pika): mcp_security.go — MCP Security Pipeline — wave 6b

- **ТЗ:** ТЗ-v2-6b: mcp_security.go — MCP Security
- **PR:** #TBD
- **Files:**
  - `pkg/pika/mcp_security.go` — MODIFIED: rename extractJSON→extractGuardJSON (conflict with archivist.go)
  - `pkg/pika/mcp_security_test.go` — NEW: 24 tests covering all 15 acceptance criteria (Output Sanitizer, NFKC, credentials, taint tracking, ACL, capability negotiation, MCP Guard startup/canary, Rug Pull Guard, adaptive baseline, degraded mode, audit trail, prompt versioning)
- **Breaking:** None (new files, additive only)
- **Dependencies:** `pkg/pika/telemetry.go` (ReportComponentFailure/Success), `pkg/pika/autoevent.go` (EventClasses)

---

## Wave 7: Diagnostics

### [2026-05-06] feat(pika): diagnostics.go — Diagnostics Engine — wave 7a

- **ТЗ:** ТЗ-v2-7a
- **PR:** #TBD
- **Files:**
  - `pkg/pika/diagnostics.go` — NEW: `DiagnosticsEngine` struct — single point for subagent error diagnosis, correction rule (CR) management, and subagent prompt assembly with active CR injection. `Diagnose` (error attribution by trace_id, pattern detection ≥2 similar errors → SuggestedCR), `CreateCR` (insert CR into registry, TG notification D-149, threshold alert ≥3 active CRs), `BuildSubagentPrompt` (hot-reload base prompt + append active CRs within 500-token budget, oldest-trim), `IncrementVerified` (count++ on successful subagent call, auto-promote active→verified at threshold 5), `ReviewCRs` (weekly Reflector pipeline: promote verified+7d, deactivate active+30d+unverified). `CorrectionRule` type with lifecycle: active → verified → promoted/deactivated. Constants: `defaultMaxActiveCRs=10`, `defaultMaxCRTokens=500`, `defaultVerifyThreshold=5`, `defaultPromotionMinAgeDays=7`, `defaultDeactivationMaxAgeDays=30`. `validCRComponents` map for component validation. `estimateCRTokens` helper (~4 chars/token).
  - `pkg/pika/diagnostics_test.go` — NEW: 10 tests (`TestDiagnose_ErrorFound`, `TestDiagnose_NoErrors`, `TestDiagnose_SuggestedCR`, `TestCreateCR_Valid`, `TestCreateCR_InvalidComponent`, `TestBuildSubagentPrompt_NoCRs`, `TestBuildSubagentPrompt_WithCRs`, `TestBuildSubagentPrompt_TokenOverflow`, `TestBuildSubagentPrompt_MissingFile`, `TestIncrementVerified`, `TestReviewCRs`)
  - `pkg/pika/archivist.go` — MODIFIED: added `diag *DiagnosticsEngine` field to `Archivist` struct, `loadPromptFile` now calls `BuildSubagentPrompt` with fallback to original behavior when diag=nil
  - `pkg/pika/atomizer.go` — MODIFIED: added `diag *DiagnosticsEngine` field to `Atomizer` struct, same `loadPromptFile` fallback pattern
  - `pkg/pika/reflector.go` — MODIFIED: added `diag *DiagnosticsEngine` field to `ReflectorPipeline` struct, same `loadPromptFile` fallback pattern (multi-line signature)
  - `pkg/pika/mcp_security.go` — MODIFIED: added `diag *DiagnosticsEngine` field to `MCPSecurityPipeline` struct, `loadGuardPrompt` now calls `BuildSubagentPrompt` with `cachedPromptSHA` update + fallback
- **Breaking:** None (new files, additive only; caller-side patches backward-compatible: diag=nil → original behavior)
- **Dependencies:** `pkg/pika/botmemory.go` (BotMemory, registry table), `pkg/pika/interfaces.go` (TelegramSender), `pkg/pika/botmemory.go` (TraceSpanRow)

### [2026-05-06] feat(pika): analytics.go — Go-only Analytics Pipeline — wave 7b

- **ТЗ:** ТЗ-v2-7b
- **PR:** #TBD
- **Files:**
  - `pkg/config/config_pika_analytics.go` — NEW: `AnalyticsConfig` struct (schedule weekly/monthly cron, Telegram channels, anomaly thresholds), `AnalyticsSchedule` struct, `DefaultAnalyticsConfig()` with sensible defaults
  - `pkg/pika/analytics.go` — NEW: `AnalyticsEngine` struct — full Go-only analytics pipeline. `Run(ctx, mode)` orchestrates: period computation, metric collection (7 SQL query sets), delta calculation vs previous period, anomaly detection (7 rules: error rate, tool fail rate, latency P95, subagent errors, unused atoms, stale atoms, significant deltas), Telegram report formatting (≤4096 chars with auto-split), registry snapshot storage (kind=snapshot, upsert). Helper functions: `analyticsComputePeriods`, `analyticsComputeDeltas`, `analyticsDetectAnomalies`, `analyticsFormatReport`, `analyticsPercentile`, `analyticsSplitMessage`, `analyticsFormatCount`, `analyticsHasCritical`. Constants: `AnalyticsWeekly`/`AnalyticsMonthly`, 7 anomaly thresholds, `reportMaxTelegramChars=4096`
  - `pkg/pika/analytics_cron.go` — NEW: `RegisterAnalyticsJobs` (registers weekly+monthly cron jobs reusing `schedToCronExpr` from reflector), `HandleAnalyticsJob` (dispatches cron payload to engine.Run)
  - `pkg/pika/analytics_test.go` — NEW: 21 tests (CollectMetrics happy/partial/empty, Deltas increase/decrease/zero, Anomalies x7 + clean, FormatReport x2, StoreReport upsert, P95, SplitMessage, Periods weekly/monthly, HasCritical, FormatCount)
  - `workspace/queries/analytics_llm.sql` — NEW: LLM metrics (total requests, tokens, cost, avg/P95 latency, error rate, reasoning ratio, cost by component)
  - `workspace/queries/analytics_tools.sql` — NEW: Tool calling aggregates (requested/success/failed, success rate, top tools via json_each)
  - `workspace/queries/analytics_chains.sql` — NEW: Chain analysis (total chains, avg length, avg cost per chain)
  - `workspace/queries/analytics_subagents.sql` — NEW: Subagent health (error/timeout counts, avg/P95 duration per component)
  - `workspace/queries/analytics_knowledge.sql` — NEW: Knowledge quality (total atoms, new in period, by category/polarity/confidence bands)
  - `workspace/queries/analytics_atom_usage.sql` — NEW: Atom usage (total usages, unique atoms, effectiveness %, top atoms, unused count)
  - `workspace/queries/analytics_tasks.sql` — NEW: Task efficiency (top-5 tasks by cost, avg tokens/tools per task)
- **Breaking:** None (new files, additive only)
- **Dependencies:** `pkg/pika/botmemory.go` (BotMemory, registry table), `pkg/pika/interfaces.go` (TelegramSender), `pkg/config/config_pika_analytics.go` (AnalyticsConfig), `pkg/cron` (CronService, CronJob, CronSchedule)


### [2026-05-07] feat(pika): TZ-v2-8i — AutoEvent + RAD + Analytics wiring — wave 8i
- **T3:** TZ-v2-8i
- **Fixes:** #TBD
- **Files:**
  - `pkg/agent/hook_pika.go` — NEW: 'autoEventAdapter' struct wrapping 'pika.AutoEventHandler' as 'agent.EventObserver'. Translates 'EventKindToolExecEnd' → 'HandleToolResult'. Compile-time interface check added.
  - `pkg/agent/context_pika.go` — MOD: mount 'autoEventAdapter' as builtin hook via HookRegistration after BotMemory init. Set 'al.botmem = botmem' for RAD reasoning access.
  - `pkg/agent/agent.go` — MOD: added 'rad *pika.RAD' and 'botmem *pika.BotMemory' fields to AgentLoop. Added 'GetBotMemory()' public getter for gateway access.
  - `pkg/agent/agent_init.go` — MOD: RAD initialization from 'cfg.Security.RAD' after resolveContextManager(). Uses pika.NewRAD(pika.RADConfig{...}).
  - `pkg/agent/rad_gate.go` — NEW: 'radPreActionGate()' — direct RAD call in pipeline (NOT hook). Gets reasoning via BotMemory.GetLastReasoningText, calls RAD.Analyze, blocks on RADAnomaly, warns on RADWarning.
  - `pkg/agent/pipeline_execute.go` — MOD: inserted RAD pre-action gate before each tool call in ExecuteTools (D-136a checkpoint F16).
  - `pkg/pika/bus_sender.go` — NEW: 'BusSender' adapter (msgBus → TelegramSender interface). Universal sender for any connected messenger — not Telegram-specific.
  - `pkg/pika/analytics_cron.go` — NEW: 'AnalyticsCron' scheduler. Runs AnalyticsEngine.Run on weekly+monthly intervals via goroutines (D-136a checkpoint F17).
  - `pkg/gateway/gateway.go` — MOD: analytics wiring in restartServices() after CronService.Start(). Creates BusSender → AnalyticsEngine → AnalyticsCron pipeline.
  - `pkg/agent/rad_gate_test.go` — NEW: 3 tests (TestRadPreActionGate_NilRAD, TestRadPreActionGate_SafeTool, TestRadPreActionGate_WithBotmem)
  - `pkg/agent/hook_pika_test.go` — NEW: 2 tests (TestAutoEventAdapter_ImplementsEventObserver, TestAutoEventAdapter_NilHandler)
  - `pkg/pika/bus_sender_test.go` — NEW: 1 test (TestBusSender_ImplementsTelegramSender)
  - `pkg/pika/analytics_cron_test.go` — NEW: 3 tests (TestNewAnalyticsCron_Defaults, TestNewAnalyticsCron_CustomIntervals, TestAnalyticsCron_StartStop)
- **Breaking:** None (new files, additive only)
- **Dependencies:** pkg/pika/autoevent.go (wave 3e), pkg/pika/rad.go (wave 6a), pkg/pika/analytics.go (wave 7b), pkg/agent/hooks.go (upstream), pkg/bus/bus.go (upstream)
- **Design decisions:**
  - RAD: direct call in pipeline, NOT hook/EventObserver — per TZ-v2-8i spec. Reasoning extracted from BotMemory, not LLM response fields.
  - Analytics: BusSender wraps universal MessageBus instead of Telegram-specific channel. Bus routes to all connected messengers.
  - Analytics cron: goroutine-based (like HeartbeatService), not CronService jobs — simpler lifecycle, no cron expression parsing needed.

### [2026-05-10] feat(pika): ТЗ-v2-8j (Phase A) — Prompt files for subagents + MCP Guard fallback — wave 8
- **Files:**
  - `workspace/prompts/atomizer.md` — NEW: Atomizer system prompt extracted from defaultAtomizerPrompt Go constant. SSOT: Go code (pkg/pika/atomizer.go:642).
  - `workspace/prompts/archivist_build.md` — NEW: Archivist system prompt from Notion SSOT (Приложение: Промт Архивариуса v2). Version 2.2, unified search_context tool.
  - `workspace/prompts/reflexor.md` — NEW: Reflexor system prompt from Notion SSOT (Промт Рефлексора v1). XML-structured, 5 analysis sections, JSON output schema.
  - `workspace/prompts/mcp_guard.md` — NEW: MCP Guard system prompt from Notion SSOT (Приложение: Промт MCP Guard). English, 4-step CoT pipeline, STARTUP_AUDIT + RUNTIME_AUDIT modes.
  - `pkg/pika/mcp_security.go` — MOD: added `"errors"` import, `os.ErrNotExist` fallback in `LoadGuardPrompt()`, `defaultGuardPrompt` constant. Now matches D-90 fallback pattern used by archivist/atomizer/reflector.
- **Breaking:** None (new files, additive only; mcp_security.go fallback is backward-compatible)
- **Dependencies:** None (prompt files read at runtime via os.ReadFile, no go:embed)
- **Design decisions:**
  - All 4 subagent prompts stored as `workspace/prompts/*.md` — hot-reloadable at runtime via D-90 pattern (DiagnosticsEngine → file fallback → const fallback).
  - MCP Guard previously had no `defaultGuardPrompt` / `os.ErrNotExist` fallback — agent would crash if prompt file missing. Now aligned with other 3 subagents.
  - Backticks in mcp_guard.md replaced with single quotes in Go `defaultGuardPrompt` const (Go raw strings cannot contain backticks). File version preserves original formatting.
  - Prompt content sources: atomizer from Go code, archivist/reflexor/mcp_guard from Notion SSOT pages.

### [2026-05-10] feat(pika): memory pipeline — use MemoryDBPath from config — wave 8a
- **ТЗ:** ТЗ-v2-8j Phase Б
- **PR:** TBD
- **Files:**
  - `pkg/agent/instance.go` — MODIFIED:
    - `initSessionStore(dir string)` → `initSessionStore(dbPath string)`: принимает полный путь к DB вместо директории. Убран `filepath.Join(dir, "bot_memory.db")`, используется `filepath.Dir(dbPath)` для MkdirAll
    - Строки 120-123: хардкод `filepath.Join(workspace, "sessions")` заменён на `cfg.Agents.Defaults.MemoryDBPath`
    - NEW функция `migrateMemoryDB(workspace, newPath)`: при первом запуске переносит `sessions/bot_memory.db` → `memory/bot_memory.db` через `os.Rename`. No-op если target существует или legacy отсутствует
  - `workspace/memory/MEMORY.md` — DELETED: upstream шаблон для текстовой памяти, не используется Pika v3 (у нас SQL через bot_memory.db)
- **Breaking:** bot_memory.db перемещается из `sessions/` в `memory/` при первом запуске. Миграция автоматическая, данные не теряются
- **Rollback:** `git revert` коммита. После revert вручную `mv workspace/memory/bot_memory.db workspace/sessions/bot_memory.db`. Данные сохраняются — это тот же SQLite файл
- **Config:** `cfg.Agents.Defaults.MemoryDBPath` (default: `workspace/memory/bot_memory.db`, задаётся в `defaults.go:44`). Поле существовало ранее, но игнорировалось instance.go — теперь используется

### [2026-05-10] feat(pika): ТЗ-v2-8j (Phase В) — PromptContributor refactor + upstream bootstrap — wave 8
- **ТЗ:** ТЗ-v2-8j Phase В
- **PR:** TBD
- **Files:**
- `workspace/AGENT.md` — REWRITTEN: default PicoClaw template replaced with Pika v3 SSOT content from Notion (CORE.md v4 §2.2b). Role DevOps, 3-question thinking, 8 NEVER rules with WHY, antipatterns, examples, plan markup. 96 lines.
- `workspace/SOUL.md` — REWRITTEN: default PicoClaw template replaced with Pika v3 personality. Russian, trust boundaries, security invariants. 30 lines.
- `workspace/USER.md` — REWRITTEN: default PicoClaw template replaced with Pika v3 user context from Notion (CONTEXT.md §2.3). Manager garry, server paths, 5 work modes, risk matrix. 44 lines.
- `pkg/pika/context_manager.go` — MOD: added 5 exported getters (GetArchivist, GetStateProvider, GetPlanStore, ExtractActivePlan, BuildDegradationBlock) for PromptContributor access. BuildSystemPrompt() preserved but no longer called from Assemble.
- `pkg/agent/context_pika.go` — REWRITTEN: Assemble() returns empty SystemPrompt (pipeline falls to upstream else-branch). 4 new PromptContributor structs registered: pikaMemoryBriefContributor (pika:memory_brief), pikaTrailContributor (pika:trail), pikaActivePlanContributor (pika:active_plan), pikaDegradationContributor (pika:degradation). 379 lines.
- `pkg/agent/context.go` — MOD: getIdentity() patched — removed MEMORY.md and Daily Notes references from workspace description, removed rule 3 (Memory update instruction), reduced Sprintf args from 6 to 3. Prevents conflict with Archivist memory management.
- **Breaking:** System prompt assembly path changed from Pika if-branch to upstream else-branch. Prompt content now comes from AGENT.md/SOUL.md/USER.md (upstream LoadBootstrapFiles) + 4 PromptContributors instead of CORE.md/CONTEXT.md (which never existed as files).
- **Rollback:** `git revert` of commit. Restores old context_pika.go (BuildSystemPrompt path), old getIdentity() with MEMORY.md refs, old bootstrap file contents. Pika returns to if-branch (same behavior as before Phase В).
- **Dependencies:** pkg/agent/prompt.go (PromptContributor interface, PromptRegistry), pkg/pika/context_manager.go (Trail, Meta, Archivist, SystemStateProvider), pkg/agent/context.go (ContextBuilder.RegisterPromptContributor)
- **Design decisions:**
  - Upstream else-branch chosen over custom if-branch: one prompt assembly path instead of two. Upstream provides identity, bootstrap files, skills catalog, dynamic context, conversation summary. Pika adds 4 contributors via PromptRegistry.
  - MEMORY.md references removed from getIdentity(): prevents model from writing to MEMORY.md (conflicts with Archivist-managed bot_memory.db). GetMemoryContext() already returns empty (file deleted in Phase Б).
  - META removed from system prompt: was always non-empty (made BuildSystemPrompt never return ""). Channel payload delivery deferred to follow-up PR.
  - CORE.md/CONTEXT.md were never created as files — content was always in Notion SSOT. Now properly mapped: CORE.md content → AGENT.md, personality → SOUL.md, context → USER.md.
  - PlanStore updated inside pikaActivePlanContributor for wave 4 compatibility.

### [2026-05-10] refactor(pika): ТЗ-v2-8j cleanup — remove dead BuildSystemPrompt code — wave 8
- **ТЗ:** ТЗ-v2-8j (post Phase В cleanup)
- **Files:**
- `pkg/pika/context_manager.go` — MOD: BuildSystemPrompt() gutted to stub (return "", nil). Deleted: loadBootstrapFile(), getCached(), setCached(), InvalidateCache(). Removed struct fields: mu, cachedCore, cachedContext, coreModTime, contextModTime. Removed imports: os, filepath, time, sync. 215 lines (was 370).
- `pkg/pika/context_manager_test.go` — MOD: deleted 7 dead tests (TestBuildSystemPrompt_*, TestInvalidateCache). 66 lines (was 282). Surviving: TestCompact_NoOp, TestIngest_NoOp, TestClear_NoOp, TestAlwaysHealthyProvider, TestNoopArchivistCaller.
- **Breaking:** None (BuildSystemPrompt was already dead code — Assemble returns empty SystemPrompt since Phase В)
- **Design decisions:**
  - BuildSystemPrompt kept as stub (not deleted) for API compatibility — method signature preserved, body returns "", nil.
  - CORE.md/CONTEXT.md loading, file cache, InvalidateCache all removed — no longer needed since prompt content comes from upstream LoadBootstrapFiles (AGENT.md/SOUL.md/USER.md) + 4 PromptContributors.

### [2026-05-10] fix(deps): Go 1.25.10 — govulncheck green (ТЗ-v2-8q) — wave 8
- **ТЗ:** ТЗ-v2-8q
- **Files:**
- `go.mod` — MOD: `go 1.25.9` → `go 1.25.10`. Fixes 3 stdlib vulnerabilities: GO-2026-4976 (net/http/httputil), GO-2026-4971 (net), GO-2026-4918 (net/http).
- **Breaking:** None (patch-level stdlib upgrade only)

### [2026-05-10] fix(pika): ТЗ-v2-8l — Upstream embed fix + prompt protection — wave 8
- **ТЗ:** ТЗ-v2-8l
- **PR:** #TBD
- **Files:**
  - `cmd/picoclaw/internal/onboard/helpers.go` — MOD: `onboard()` signature: added `resetPrompts bool`. `createWorkspaceTemplates()` signature: added `preservePrompts bool`. `copyEmbeddedToTarget()` signature: added `preservePrompts bool`. Added skip logic: when `preservePrompts=true`, existing `prompts/*.md` files are not overwritten on re-onboard. Added `"strings"` import.
  - `cmd/picoclaw/internal/onboard/command.go` — MOD: added `--reset-prompts` CLI flag (default `false`). Passes `resetPrompts` to `onboard()`.
  - `pkg/config/config_pika.go` — MOD: added `OnboardConfig` struct with `PreserveUserPrompts bool`.
  - `pkg/config/config.go` — MOD: added `Onboard OnboardConfig` field to `Config` struct.
  - `pkg/config/defaults.go` — MOD: added `Onboard: OnboardConfig{PreserveUserPrompts: true}` default.
- **Breaking:** None (additive only, default behavior preserved for first onboard)
- **Design decisions:**
  - Prompt protection is config-driven (`onboard.preserve_user_prompts`, default `true`) — user controls via WebUI toggle or config.json.
  - CLI `--reset-prompts` flag is one-shot override: resets prompts in this run without changing config.
  - Only `prompts/*.md` are protected; other workspace files (SOUL.md, USER.md, skills/) update on re-onboard. Rationale: prompts are user-tunable via hot-reload, other files are upstream templates.
  - WebUI dashboard toggle deferred to separate follow-up (frontend change).

### [2026-05-10] feat(pika): ТЗ-v2-8l part 2c — WebUI toggle for prompt protection — wave 8
- **ТЗ:** ТЗ-v2-8l (часть 2c — WebUI)
- **PR:** #TBD
- **Files:**
  - `web/frontend/src/components/config/form-model.ts` — MODIFIED: added `preserveUserPrompts` field, default, and config parser
  - `web/frontend/src/components/config/config-sections.tsx` — MODIFIED: added `OnboardSection` with toggle
  - `web/frontend/src/components/config/config-page.tsx` — MODIFIED: import, render, and patchAppConfig mapping
- **Breaking:** None

---

## Wave 9: Wiring Audit

### [2026-05-12] feat(pika): ТЗ-v2-9b — Pipeline Wiring: Atomizer, Reflector, MCPSecurity, Diagnostics — wave 9b

- **ТЗ:** ТЗ-v2-9b: Pipeline Wiring
- **PR:** #TBD
- **Files:**
  - `pkg/agent/context_pika.go` — MOD: NewDiagnosticsEngine + NewAtomizer + NewReflectorPipeline + NewMCPSecurityPipeline creation. SetDiagnostics() calls to all subagents.
  - `pkg/agent/agent.go` — MOD: added reflector *pika.ReflectorPipeline + mcpSecurity *pika.MCPSecurityPipeline fields and GetReflector()/GetMCPSecurity() getters.
  - `pkg/agent/pipeline_finalize.go` — MOD: replaced no-op stub with Atomizer trigger (ShouldAtomize → Run in goroutine after each turn).
  - `pkg/agent/pipeline_execute.go` — MOD: MCPSecurity ProcessToolOutput call after MCP tool Execute. Simplified from switch/verdict to single facade call.
  - `pkg/gateway/gateway.go` — MOD: RegisterReflectorJobs + HandleReflectorJob in restart/reload path after analyticsCron.
  - `pkg/pika/archivist.go` — MOD: added SetDiagnostics() setter.
  - `pkg/pika/atomizer.go` — MOD: added SetDiagnostics() setter.
  - `pkg/pika/reflector.go` — MOD: added SetDiagnostics() setter.
  - `pkg/pika/mcp_security.go` — MOD: added SetDiagnostics() setter + ProcessToolOutput() facade (verdict logic inside pika, not agent).
  - `pkg/tools/integration/mcp_tool.go` — ROLLED BACK to main (audit fix: upstream file, ТЗ-v2-6b forbids modification).
- **Breaking:** None (all guards: if component != nil)
- **Known limitation:** RegisterReflectorJobs only in restart path (not cold start). Safe: nil-check skips. Works after first reload.
- **Dependencies:** pkg/pika/atomizer.go (wave 5a), pkg/pika/reflector.go (wave 5b), pkg/pika/mcp_security.go (wave 6b), pkg/pika/diagnostics.go (wave 7a)

### [2026-05-12] feat(config): extract hardcoded analytics/subagent settings into config — wave 8h
- **ТЗ:** ТЗ-v2-8h
- **PR:** #TBD
- **Files:**
  - `pkg/config/config_pika_analytics.go` — MODIFIED: extended `AnalyticsConfig` from 3 to 15 fields (Enabled, QueriesDir, Schedule, 7 thresholds: ToolFailThresholdPct/LLMErrorThresholdPct/LatencyP95ThresholdMs/UnusedAtomsPct/StaleAtomsPct/DeltaSpikePct/AnomalyWindowHours, 4 limits: TopQueriesLimit/TopAtomsLimit/ReportMaxLines/HistoryRetentionDays, DisableTelegramReports). `DefaultAnalyticsConfig()` with production defaults replacing 11 hardcoded consts from analytics.go.
  - `pkg/config/config.go` — MODIFIED: added `Analytics AnalyticsConfig` field to global `Config` struct (line 54).
  - `pkg/agent/config_mappers.go` — NEW: 5 config mappers (`mapAtomizerConfig`, `mapReflectorConfig`, `mapArchivistConfig`, `mapMCPGuardConfig`, `mapTelemetryConfig`) — replace hardcoded `Default*Config()` with values resolved from unified config via `cfg.ResolveAgentConfig()`.
  - `pkg/agent/context_pika.go` — MODIFIED: replaced 5× `Default*Config()` calls with `map*Config(cfg)` calls (Archivist :70, Atomizer :80, Reflector :88, MCPGuard :95, Telemetry :105).
  - `pkg/pika/analytics.go` — MODIFIED: removed 11 hardcoded const (thresholds/limits). Added `cfg` field to `AnalyticsEngine`. `NewAnalyticsEngine()` signature changed — accepts `config.AnalyticsConfig` as first arg. Added `applyAnalyticsDefaults()` for zero-value fallback. `analyticsDetectAnomalies()` accepts `config.AnalyticsConfig` as third arg. `DisableTelegramReports` flag wraps periodic report delivery (alerts always sent).
  - `pkg/pika/analytics_cron_service.go` — NEW: CronService-based analytics scheduler (prepared for Block 4 migration from ticker). Blocked by `SetOnJob` dispatcher ordering — deferred.
  - `pkg/gateway/gateway.go` — MODIFIED: analytics engine creation uses `cfg.Analytics` instead of zero-value. Reflector schedule read from `cfg.ResolveAgentConfig("reflexor").Schedule` with fallback defaults ("03:00"/"Sun 04:00"/"1st 05:00"). Removed broken `HandleAnalyticsJob` block from `SetOnJob` dispatcher (lines 815-823). Fixed `fmt.Println` placement inside `if botmem != nil` block.
  - `pkg/pika/analytics_test.go` — MODIFIED: updated 8× `analyticsDetectAnomalies()` calls + 4× `NewAnalyticsEngine()` calls for new signatures. Added `config` import. Zero-value `AnalyticsConfig{}` wrapped in `applyAnalyticsDefaults()` for correct threshold defaults in tests.
- **Breaking:** `NewAnalyticsEngine()` signature changed (config.AnalyticsConfig added as first arg). `analyticsDetectAnomalies()` signature changed (config.AnalyticsConfig added as third arg).
- **Dependencies:** `pkg/config/config_pika_analytics.go` (AnalyticsConfig), `pkg/config/config_pika.go` (ResolveAgentConfig, ResolvedAgentConfig, ScheduleConfig)
- **Design decisions:**
  - DisableTelegramReports controls only periodic reports. Alerts (anomalies, degraded) always go to manager chat — per founder requirement.
  - analytics_cron_service.go created but not wired — CronService migration blocked by SetOnJob dispatcher ordering (documented in ТЗ as Block 4).
  - Reflector schedule uses fallback defaults when config values are empty — backward-compatible with existing deployments without explicit schedule config.
