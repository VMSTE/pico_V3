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
- **PR:** #TBD
- **Files:**
  - `pkg/pika/atomizer.go` — NEW: `Atomizer` struct — Go-pipeline extracting knowledge atoms from hot buffer. `AtomizerConfig` (D-133: trigger_tokens=800k, chunk_max_tokens=200k, prompt_file, max_retries=2, model=background). `DefaultAtomizerConfig()`. `AtomLLMOutput` / `atomizerLLMResponse` — LLM structured output types. `NewAtomizer(mem, atomGen, provider, telemetry, cfg)` constructor. `ShouldAtomize(ctx, sessionID)` — threshold check. `Run(ctx, sessionID)` — full pipeline: chunk selection (oldest turns ≤ budget) → hot-reload prompt (`os.ReadFile`) → LLM call (structured output, 0 tool calls) → parse+validate (category/polarity/confidence/source_turns) → retry loop (up to MaxRetries with REPAIR prompt on validation error) → INSERT atoms (via `AtomIDGenerator.Next` + `BotMemory.InsertAtom`) → archive+delete (1 txn via `BotMemory.ArchiveAndDeleteTurns`). Tags inherited from events per turn (D-75: `collectTagsByTurn` + `mergeTagsForTurns`). Telemetry: `ReportComponentFailure/ReportComponentSuccess`. Helper: `getMessagesByTurns` (same-package access to `BotMemory.db`). JSON extraction: `extractAtomizerJSON` + `extractBalanced`. Default prompt constant `defaultAtomizerPrompt`.
  - `pkg/pika/atomizer_test.go` — NEW: 16 tests (ShouldAtomize_BelowThreshold, ShouldAtomize_AboveThreshold, ShouldAtomize_Disabled, Run_HappyPath, Run_ValidationRetry, Run_AllRetriesExhausted, Run_EmptySession, Run_LLMError, ValidateAtoms_Valid, ValidateAtoms_InvalidCategory, ValidateAtoms_InvalidPolarity, ValidateAtoms_ConfidenceOutOfRange, ValidateAtoms_TurnNotInChunk, ValidateAtoms_Empty, ValidateAtoms_EmptySummary, ExtractAtomizerJSON, CollectTagsByTurn, MergeTagsForTurns, DefaultAtomizerConfig)
- **Breaking:** None (new files, additive only)
- **Dependencies:** `pkg/pika/botmemory.go` (BotMemory CRUD), `pkg/pika/registry.go` (AtomIDGenerator), `pkg/pika/telemetry.go` (Telemetry), `pkg/providers` (LLMProvider)

### [2026-05-05] feat(pika): reflector.go — Reflector pipeline — wave 5b

- **ТЗ:** ТЗ-v2-5b: reflector.go — Reflector pipeline
- **PR:** #TBD
- **Files:**
  - `pkg/pika/reflector.go` — NEW: `ReflectorPipeline` struct — Go-pipeline for behavioral optimization via cheap LLM (structured output, 0 tool calls). `ReflectorConfig` + `DefaultReflectorConfig()` (model=background, T=0.2 per F8-18, prompt_file, max_retries=1, schedule: daily/weekly/monthly per D-134). `NewReflectorPipeline(mem, atomGen, provider, telemetry, cfg)` constructor. `Run(ctx, mode)` — full pipeline: data prep (SQL SELECT by scope) → hot-reload prompt (`os.ReadFile`, D-90) → LLM call → parse+validate JSON → apply (INSERT/UPDATE/DELETE in txn). 3 modes: daily (1 day), weekly (7 days), monthly (all atoms, D-134). 4 tasks: (1) Merge duplicates (D-147: polarity validation, 1 txn INSERT merged → UPDATE atom_usage → DELETE originals, history.parent_avg baseline, tags/source_turns UNION, confidence=AVG), (2) Pattern detection (antipattern/recurring_failure → INSERT pattern atom), (3) Confidence updates (D-59: delta clamp 0.0–1.0, no time decay per F8-8, +0.1 confirmed / -0.2 contradicted), (4) Runbook drafts (D-87/F9-5: INSERT runbook_draft atom with steps/trigger/rollback). Monthly extras: stale marking (confidence < 0.2, D-147). LLM structured output types: `reflectorLLMResponse` with merges/patterns/confidence_updates/runbook_drafts arrays. `callWithRetry` with REPAIR prompt on JSON parse failure. `extractReflectorJSON` via `extractBalancedPair`. Default prompt constant. Telemetry: `ReportComponentFailure/Success("reflector")`.
  - `pkg/pika/reflector_test.go` — NEW: 14 tests (EmptyDB_SkipsLLM, Disabled_NoOp, HappyPath_MergeAndConfidence, PatternInsertion, RunbookDraft, ConfidenceClamp, InvalidJSON_Retry, LLMError_ReportsFailure, MergePolarityMismatch_Skipped, InvalidAtomID_Skipped, WeeklyScope, MonthlyScope, ParseOutput_EmptyArrays, ParseOutput_NoJSON, ClampConfidence, DefaultConfig, BuildUserContent)
- **Breaking:** None (new files, additive only)
- **Dependencies:** `pkg/pika/botmemory.go` (BotMemory CRUD + direct db access), `pkg/pika/registry.go` (AtomIDGenerator), `pkg/pika/telemetry.go` (Telemetry), `pkg/providers` (LLMProvider, Message)
