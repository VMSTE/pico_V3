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
  - `pkg/config/config_pika.go` — NEW: 18 Pika v3 types (ClarifyConfig, SecurityConfig, DangerousOpsConfig, RADConfig, MCPSecurityConfig, HealthConfig, FallbackProviderConfig, HealthReportingConfig, ProgressConfig, ReasoningConfig, BudgetConfig, OutputGateConfig, LoopConfig, MemoryBriefConfig, ArchiveConfig, ScheduleConfig, BaseToolsConfig, ConfirmMode); ResolvedAgentConfig struct; ResolveAgentConfig() merge function; IsBaseToolEnabled(); ConfirmMode with bool/string UnmarshalJSON
  - `pkg/config/config_pika_test.go` — NEW: tests for all types, ResolveAgentConfig (unknown/inherit/override/full merge), BaseTools (master switch, per-tool, BRAIN), ConfirmMode (string/bool/invalid)
- **Breaking:** None (additive only)

### [2026-05-02] feat(pika): config.go struct patching + legacy cleanup — wave 0b Phase 2

- **ТЗ:** ТЗ-v2-0b-p2: config.go — Phase 2 (struct patching + legacy cleanup)
- **PR:** #5 (merged, replaces reverted PR #4)
- **Files:**
  - `pkg/config/config.go` — MODIFIED: added Pika fields to Config (+Clarify, +Security, +Health), AgentDefaults (~15 fields: MemoryDBPath, BaseToolsDir, SkillsDir, TopP, TopK, Telemetry*, Retry*, IdleTimeoutMin), AgentConfig (~30 pointer override fields for main/archivist/atomizer/mcp_guard roles), ModelConfig (+APIKeyEnv), ToolsConfig (+BaseTools); DEPRECATED comments on Isolation, Session, Devices, Voice; LoadConfig() simplified (migration switch removed, only CurrentVersion), APIKeyEnv→APIKeys resolve added, ContextManager default "pika"; loadConfig() moved from migration.go; Config.IsBaseToolEnabled() wrapper
  - `pkg/config/defaults.go` — MODIFIED: Pika defaults in DefaultConfig() (MemoryDBPath, telemetry, retry, idle, ContextManager="pika"), Agents.List=[{ID:"main"}], Clarify/Security/Health/BaseTools full defaults
  - `pkg/config/config_pika.go` — MODIFIED: completed ResolveAgentConfig() with all Pika field resolution
  - `pkg/config/config_pika_test.go` — MODIFIED: added Phase 2 tests (DefaultConfig_PikaDefaults, Config_IsBaseToolEnabled, ResolveAgentConfig_FullPikaMerge)
  - `pkg/config/migration.go` — EMPTIED to `package config`
  - `pkg/config/config_old.go` — EMPTIED to `package config`
  - `pkg/config/legacy_bindings.go` — EMPTIED to `package config`
  - `pkg/config/migration_test.go` — EMPTIED to `package config`
  - `pkg/config/migration_integration_test.go` — EMPTIED to `package config`
  - `pkg/config/example_security_usage.go` — EMPTIED to `package config`
- **Breaking:** Config versions 0/1/2 no longer supported (migration switch removed). Only version 3 loads.

### [2026-05-02] feat(pika): post-merge cleanup — wave 0b Phase 3

- **ТЗ:** ТЗ-v2-0b-p3: config.go — Post-merge cleanup
- **PR:** #6 (merged)
- **Files:**
  - `pkg/config/envkeys.go` — MODIFIED: added PIKA_* env constants (EnvPikaHome, EnvPikaConfig, EnvPikaBuiltinSkills, EnvPikaBinary, EnvPikaDBPath); GetHome() updated with PIKA_HOME priority over PICOCLAW_HOME
  - `pkg/config/config.go` — MODIFIED: added MemoryDBPath validation in LoadConfig() (agents.defaults.memory_db_path is required)
  - `FORK_CHANGES.md` — MODIFIED: added Phase 1 + Phase 2 changelog entries
- **Breaking:** LoadConfig() now returns error if memory_db_path is empty (DefaultConfig fills it, so only affects hand-crafted JSON without this field)

---

## Wave 1: CRUD Layer (botmemory + session store + registry)

### [2026-05-02] feat(pika): botmemory.go — CRUD layer for bot_memory.db — wave 1a

- **ТЗ:** ТЗ-v2-1a: botmemory.go — CRUD bot_memory.db
- **PR:** #7
- **Files:**
  - `pkg/pika/botmemory.go` — NEW: `BotMemory` struct (sole SQL access layer for bot_memory.db); zstd Encoder/Decoder; `NewBotMemory(db)` constructor with crash-recovery (`recoverStaleSpans`); `Close()`; all row types (MessageRow, EventRow, KnowledgeAtomRow, RegistryRow, RequestLogRow, ReasoningLogRow, TraceSpanRow, EventArchiveRow); Messages CRUD (SaveMessage, GetMessages, GetDistinctSessionIDs, SumTokensBySession, GetOldestTurnIDs, CountMessagesBySession, GetMaxTurnID, DeleteAllMessages); Events (SaveEvent, GetEventsByTurns); Knowledge Atoms (InsertAtom, QueryKnowledgeFTS, UpdateAtomConfidence, GetMaxAtomN with category→prefix map); Registry (UpsertRegistry INSERT OR IGNORE + UPDATE, GetRegistry, SearchRegistry, UpdateRegistryLastUsed); Request/Reasoning Log (InsertRequestLog, InsertReasoningLog, GetReasoningByTurns); Trace Spans (InsertSpan, CompleteSpan, recoverStaleSpans); `ArchiveAndDeleteTurns` transactional archiver (messages→messages_archive with zstd blob, events→events_archive with zstd blob, reasoning_log→reasoning_log_archive with zstd blob, then DELETE hot); Archive Read (ReadArchivedMessage with decompress, SearchEventsArchiveFTS); Prompt Versions (UpsertPromptVersion, InsertPromptSnapshot); Atom Usage (InsertAtomUsage)
  - `pkg/pika/botmemory_test.go` — NEW: 17 tests
- **Breaking:** None (new file, additive only)

### [2026-05-02] fix(pika): botmemory.go — 5 SQL bugs vs DDL — wave 1a-fix

- **ТЗ:** ТЗ-v2-1a-fix: Фикс PR #7 — 5 багов botmemory.go
- **PR:** #7 (updated)
- **Files:**
  - `pkg/pika/botmemory.go` — MODIFIED: 5 bug fixes (UpsertPromptVersion, InsertPromptSnapshot, InsertAtomUsage, ArchiveAndDeleteTurns signatures)
  - `pkg/pika/botmemory_test.go` — MODIFIED: updated for new signatures
- **Breaking:** Signature changes (no external consumers yet)

### [2026-05-03] feat(pika): PikaSessionStore — session.SessionStore via BotMemory — wave 1b Phase 1

- **ТЗ:** ТЗ-v2-1b-v2-A: session_store.go — создание (фаза 1 из 2)
- **PR:** #9
- **Files:**
  - `pkg/pika/session_store.go` — NEW: `PikaSessionStore` struct implementing `session.SessionStore` interface via BotMemory
  - `pkg/pika/session_store_test.go` — NEW: 8 tests
- **Breaking:** None (new files, additive only)

### [2026-05-03] feat(pika): migration — switch to PikaSessionStore + delete pkg/memory — wave 1b Phase 2

- **ТЗ:** ТЗ-v2-1b-v2-B: миграция — удаление pkg/memory + патчи (фаза 2 из 2)
- **PR:** #9
- **Files:**
  - `pkg/agent/instance.go` — MODIFIED: `initSessionStore()` rewritten for PikaSessionStore
  - `pkg/session/metadata.go` — NEW: extracted `MetadataAwareSessionStore` interface
  - `pkg/session/jsonl_backend.go` — DELETED
  - `pkg/memory/` — DELETED (entire package)
- **Breaking:** `pkg/memory` package removed. All session persistence via PikaSessionStore.

### [2026-05-03] test: skip 4 legacy tests — transitional (D-136)

- **PR:** #9, #12
- **Breaking:** None (tests skipped, not removed)

### [2026-05-03] fix(pika): remove linux/arm + exotic archs from build-all — CI fix

- **PR:** #11, #13
- **Breaking:** None (`build-all` reduced to 4 platforms)

### [2026-05-04] feat(pika): registry.go — Registry CRUD + AtomID generator — wave 1c

- **ТЗ:** ТЗ-v2-1c: registry.go — Registry CRUD + валидация
- **PR:** #14
- **Files:**
  - `pkg/pika/registry.go` — NEW: `AtomIDGenerator` + `RegistryHandler`
  - `pkg/pika/registry_test.go` — NEW: 13 tests
- **Breaking:** None (new files, additive only)

---

## Wave 2: Runtime Components (TRAIL/META, envelope, context manager)

### [2026-05-04] feat(pika): trail_meta.go — TRAIL ring buffer + META metrics — wave 2a

- **ТЗ:** ТЗ-v2-2a: trail_meta.go — TRAIL + META
- **PR:** #16
- **Files:**
  - `pkg/pika/trail_meta.go` — NEW: `Trail` ring buffer (5 entries) + `Meta` system metrics
  - `pkg/pika/trail_meta_test.go` — NEW: tests + concurrency race detection
- **Breaking:** None (new files, additive only)

### [2026-05-04] feat(pika): PikaContextManager + delete Seahorse/legacy + cleanup pipeline — wave 2b (Phases A+B+C)

- **PR:** existing PR on `feat/v2-2b-context-manager` branch
- **Breaking:** Seahorse CM + Legacy CM + `cmd/membench` deleted. Pipeline compression removed.

### [2026-05-04] feat(pika): envelope.go — unified tool response envelope — wave 2c

- **ТЗ:** ТЗ-v2-2c: envelope.go — Tool response envelope
- **Files:**
  - `pkg/pika/envelope.go` — NEW: `Envelope` + `ParseEnvelope` + `ErrorKind` + `IsRetryable`
  - `pkg/pika/envelope_test.go` — NEW: 18 tests
- **Breaking:** None (new files, additive only)

### [2026-05-04] fix(pika): SystemPrompt bypass in pipeline_setup.go + cleanup

- **Breaking:** None (bypass is conditional)

---

## Wave 3: Tools, Router, Archivist

### [2026-05-04] feat(pika): archivist.go — Archivist agentic LLM session — wave 3a

- **ТЗ:** ТЗ-v2-3a: archivist.go — Архивариус
- **PR:** #21
- **Files:**
  - `pkg/pika/archivist.go` — NEW: `Archivist` struct implementing `ArchivistCaller` with agentic LLM loop, `search_context` tool, caching, size control
  - `pkg/pika/archivist_test.go` — NEW: 8 tests
  - `pkg/pika/interfaces.go` — MODIFIED: updated `ArchivistCaller` interface + new types
  - `pkg/pika/context_manager.go` — MODIFIED: adapted for new `ArchivistCaller` interface
- **Breaking:** `ArchivistCaller` interface signature changed (no external consumers)

### [2026-05-04] feat(pika): tool_router.go — unified tool routing (D-TOOL-CLASS) — wave 3b

- **ТЗ:** ТЗ-v2-3b: tool_router.go — Tool routing
- **PR:** #22
- **Files:**
  - `pkg/pika/tool_router.go` — NEW: `ToolRouter` with 4-category dispatch (🧠→🔧→🛠️→🔌→error)
  - `pkg/pika/tool_router_test.go` — NEW: 15 tests
- **Breaking:** None (new files, additive only)

### [2026-05-04] feat(pika): memory_tools.go — search_memory Go-native tool — wave 3c

- **ТЗ:** ТЗ-v2-3c: memory_tools.go — search_memory
- **PR:** #23
- **Files:**
  - `pkg/pika/memory_tools.go` — NEW: `MemorySearch` struct implementing `toolshared.Tool` (D-NEW-1). Stateless singleton, registered via `toolRouter.RegisterBrain(ms)`. `SessionIDKey` context key. `SearchMemoryArgs`, `SearchResult` types. 6 parallel search layers via `errgroup.Group`: (1) messages — SQL LIKE on current session, (2) knowledge — FTS5 MATCH on knowledge_fts + bm25(), (3) archive — atom → source_message_id → ReadArchivedMessage → decompress → snippet, (4) events_archive — FTS5 MATCH on events_archive_fts + bm25(), (5) reasoning — json_each LIKE on reasoning_keywords (hot + archive), (6) registry — LIKE on snapshots (key, summary, data, tags). Dedup by source+id. Scoring: `normalized_bm25 * layer_priority + recency_boost` (layer priorities: knowledge=1.0, events=0.9, archive=0.8, reasoning=0.7, registry=0.6, messages=0.5; BM25 min-max normalization for FTS layers, 1.0 for non-FTS; recency linear decay 30 days clamp 0..0.1). Sort DESC → top-N. Limit clamp 1..20. Timeout 5s. Layer error → log.Warn, layer=[], others continue.
  - `pkg/pika/memory_tools_test.go` — NEW: 10 tests (BasicQuery — 3 layers merged+scored+sorted, LimitClamp — 0→1/100→20, EmptySessionID — layer 1 empty/others work, LayerFailure — DROP TABLE → partial results, Timeout — context cancelled → valid JSON, Dedup — knowledge+archive no duplicates, EmptyDB → [], ScoringOrder — knowledge > registry, ArchivePipeline — atom → archive blob → decompress, ReasoningJsonEach — json_each LIKE match)
- **Breaking:** None (new files, additive only). Consumer: `loop.go` (wave 4) via `toolRouter.RegisterBrain(ms)`
- **Dependencies:** ТЗ-v2-1a (`botmemory.go` — BotMemory, ReadArchivedMessage, all row types), ТЗ-v2-0a (`migrate.go` — Migrate for tests)

### [2026-05-04] feat(pika): clarify.go — HITL clarify tool — wave 3d

- **ТЗ:** ТЗ-v2-3d: clarify.go — HITL clarify
- **PR:** #24
- **Files:**
  - `pkg/pika/clarify.go` — NEW: `ClarifyHandler` struct implementing `toolshared.Tool` (D-NEW-2). HITL clarify with per-session state via `sync.Map`. Algorithm: (1) streak check → bypass FTS5 at MaxStreakBeforeBypass, (2) decision/confirmation regex patterns → immediate escalation, (3) FTS5 pre-check via knowledge_fts with configurable timeout, (4) escalation via `ClarifySender` interface (SendMessage + WaitForReply). Types: `ClarifyConfig`, `ClarifySender` interface, `ClarifyInput`, `ClarifyResult`, `knowledgeHit`. Helper functions: `escapeFTSSpecial`, `formatFTSResults`, `formatQuestionForManager`, `parseClarifyArgs`. Reuses `buildFTSQuery` and `SessionIDKey` from memory_tools.go. Registration: `toolRouter.RegisterBrain(ch)`.
  - `pkg/pika/clarify_test.go` — NEW: 9 tests (MemoryHit — FTS5 hit → source=memory, EscalateToUser — empty knowledge → source=manager, Timeout — WaitForReply error → source=timeout, StreakBypass — streak≥2 → immediate escalation with history, DecisionQuestion — «делать?» regex → escalation, ResetStreak — streak=0+lastQuestions=nil, CleanupSession — sync.Map delete, IsAwaiting — true during WaitForReply, PrecheckTimeout — cancelled context → escalation)
- **Breaking:** None (new files, additive only). Consumer: `loop.go` (wave 4) via `toolRouter.RegisterBrain(ch)`
- **Dependencies:** ТЗ-v2-1a (`botmemory.go` — BotMemory.db for FTS5 queries), ТЗ-v2-3c (`memory_tools.go` — `buildFTSQuery`, `SessionIDKey`)

### [2026-05-04] feat(pika): autoevent.go — deterministic event generator — wave 3e

- **ТЗ:** ТЗ-v2-3e: autoevent.go — AutoEvent
- **PR:** #TBD
- **Files:**
  - `pkg/pika/autoevent.go` — NEW: `AutoEventHandler` struct — deterministic event generator after tool calls (0 LLM). `EventClasses` (Critical/Diagnostic/Heartbeat). `NewAutoEventHandler()` merges filesystem autoEvent.json + hardcoded BRAIN mappings (brainAutoEventMap, brainAutoTagMap). `HandleToolResult()` — key builder (toolName.operation + _fail suffix), toolTypeMap lookup, runtime guard (validTypes), entropy filter (consecutive dedup ring buffer, drop_if_consecutive_same=3), event class routing (critical/diagnostic→INSERT, heartbeat→atomic counter, heartbeat+fail→escalate to INSERT). `FlushHeartbeats()` — writes summary events on session rotation, resets entropy filter. `ValidateStartup()` — consistency checks (unclassified types, orphan classes, unmapped write-ops F7-4). `SetRegisteredWriteOps()` for coverage check.
  - `pkg/pika/autoevent_test.go` — NEW: 11 tests (WriteOp, ReadOpSkipped, FailSuffix, ConsecutiveDedup, HeartbeatCounter, HeartbeatFlush, HeartbeatFailEscalate, InvalidType, ValidateStartup, CoverageCheck, BrainTools)
- **Breaking:** None (new files, additive only). Consumer: `loop.go` (wave 4)
- **Dependencies:** ТЗ-v2-1a (`botmemory.go` — BotMemory.SaveEvent, EventRow), ТЗ-v2-0a (`migrate.go` — Migrate for tests)
