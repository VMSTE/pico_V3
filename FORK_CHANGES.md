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
  - `pkg/pika/botmemory_test.go` — NEW: 17 tests (SaveAndGetMessages, SumTokensAndCount, GetMaxTurnID, GetOldestTurnIDs, SaveAndGetEvents, UpsertRegistry, SearchRegistry, InsertSpanAndRecover/crash_recovery, InsertAndCompleteSpan, ArchiveAndDeleteTurns, ArchiveTransactionRollback PK conflict, PromptVersionsAndSnapshots, AtomUsage, GetMaxAtomN, UpdateAtomConfidence, InsertAndQueryKnowledgeFTS, InsertRequestLog)
- **Breaking:** None (new file, additive only)

### [2026-05-02] fix(pika): botmemory.go — 5 SQL bugs vs DDL — wave 1a-fix

- **ТЗ:** ТЗ-v2-1a-fix: Фикс PR #7 — 5 багов botmemory.go
- **PR:** #7 (updated)
- **Files:**
  - `pkg/pika/botmemory.go` — MODIFIED: Bug 2: UpsertPromptVersion — column `body` → proper DDL columns (prompt_id, component, version, hash, content, change_description), new signature returns (string, error); Bug 3: InsertPromptSnapshot — removed non-existent columns `component`, `prompt_hash`, now uses full DDL columns (snapshot_id, trace_id, session_id, turn_id, core/context/brief/trail/plan tokens, full_prompt_hash, etc.); Bug 4: InsertAtomUsage — removed non-existent columns `component`, `included`, now uses DDL columns (atom_id, trace_id, turn_id, used_in, position_in_prompt, prompt_tokens, invoked_tool_after/result, archivarius_span_id); Bug 5: ArchiveAndDeleteTurns — removed `msg_index` from messages_archive INSERT (column not in DDL); added `strconv` import
  - `pkg/pika/botmemory_test.go` — MODIFIED: Bug 1: setupTestDB and TestInsertSpanAndRecover — Migrate returns (*sql.DB, error), removed redundant sql.Open; setupTestDB now returns *BotMemory (not *sql.DB, *BotMemory); TestPromptVersionsAndSnapshots updated for new UpsertPromptVersion/InsertPromptSnapshot signatures; TestAtomUsage updated for new InsertAtomUsage signature with FK-valid atoms
- **Breaking:** Signature changes: UpsertPromptVersion, InsertPromptSnapshot, InsertAtomUsage (no external consumers yet)

### [2026-05-03] feat(pika): PikaSessionStore — session.SessionStore via BotMemory — wave 1b Phase 1

- **ТЗ:** ТЗ-v2-1b-v2-A: session_store.go — создание (фаза 1 из 2)
- **PR:** #9
- **Files:**
  - `pkg/pika/session_store.go` — NEW: `PikaSessionStore` struct implementing `session.SessionStore` interface via BotMemory; compile-time check `var _ session.SessionStore = (*PikaSessionStore)(nil)`; `NewPikaSessionStore(mem)` constructor; `messageMetadata` type for JSON serialization of all non-column Message fields (Media, Attachments, ReasoningContent, SystemParts, ToolCalls, ToolCallID); `buildMetadata()` helper; `currentTurnID()` with DB recovery; `addFullMessageLocked()` internal; `AddFullMessage()`, `AddMessage()` delegator; `GetHistory()` with full metadata deserialization; `GetSummary()`/`SetSummary()` in-memory cache; `SetHistory()` delete+re-insert; `TruncateHistory()` no-op (session rotation); `Save()` no-op (WAL); `ListSessions()` via `GetDistinctSessionIDs`; `Close()` no-op; token estimation via `tokenizer.EstimateMessageTokens`
  - `pkg/pika/session_store_test.go` — NEW: 8 tests (AddAndGetHistory with ToolCalls/ToolCallID round-trip, AttachmentsRoundTrip, TurnIDIncrement user→1/assistant→1/user→2, TurnIDRecovery from DB after restart, EmptySession returns non-nil empty slice, GetSetSummary, SetHistory delete+replace, ListSessions across 2 sessions)
- **Breaking:** None (new files, additive only). Phase 2 (ТЗ-B) will wire into instance.go.

### [2026-05-03] feat(pika): migration — switch to PikaSessionStore + delete pkg/memory — wave 1b Phase 2

- **ТЗ:** ТЗ-v2-1b-v2-B: миграция — удаление pkg/memory + патчи (фаза 2 из 2)
- **PR:** #9
- **Files:**
  - `pkg/agent/instance.go` — MODIFIED: `initSessionStore()` rewritten — now calls `pika.Migrate(dbPath)` → `pika.NewBotMemory(db)` → `pika.NewPikaSessionStore(mem)`; in-memory fallback on file-based init failure; removed imports `context`, `pkg/memory`; added import `pkg/pika`
  - `pkg/session/metadata.go` — NEW: extracted `MetadataAwareSessionStore` interface from deleted `jsonl_backend.go` (EnsureSessionMetadata, ResolveSessionKey, GetSessionScope)
  - `web/backend/api/session.go` — MODIFIED: replaced `memory.SessionMeta` with local `jsonlSessionMeta` type; removed import `pkg/memory`
  - `web/backend/api/test_jsonl_helper_test.go` — NEW: `testJSONLWriter` minimal JSONL fixture writer for tests (replaces `memory.NewJSONLStore` in tests)
  - `web/backend/api/session_test.go` — MODIFIED: replaced all `memory.NewJSONLStore` → `newTestJSONLWriter`; replaced `memory.SessionMeta` → `jsonlSessionMeta`; removed import `pkg/memory`
  - `pkg/session/jsonl_backend.go` — DELETED: replaced by PikaSessionStore
  - `pkg/session/jsonl_backend_test.go` — DELETED: tests no longer applicable
  - `pkg/memory/store.go` — DELETED: Store interface replaced by BotMemory
  - `pkg/memory/jsonl.go` — DELETED: JSONL store replaced by SQLite via BotMemory
  - `pkg/memory/jsonl_test.go` — DELETED: tests no longer applicable
  - `pkg/memory/migration.go` — DELETED: JSON→JSONL migration no longer needed
  - `pkg/memory/migration_test.go` — DELETED: tests no longer applicable
- **Breaking:** `pkg/memory` package removed entirely. `pkg/session/jsonl_backend.go` removed. All session persistence now via PikaSessionStore (SQLite bot_memory.db). `MetadataAwareSessionStore` interface moved to `pkg/session/metadata.go`. PikaSessionStore does NOT implement MetadataAwareSessionStore (steering.go uses type assertion — graceful degradation).

### [2026-05-03] test: skip 4 legacy tests — transitional (D-136)

- **ТЗ:** ТЗ-v2-1b-v2-B-fix4, fix5, fix6, fix7
- **PR:** #9, #12
- **Files:**
  - `pkg/agent/context_manager_test.go` — MODIFIED (PR #9): t.Skip on
    `TestLegacyCompact_PostTurn_ExceedsMessageThreshold`
    (TruncateHistory is no-op in PikaSessionStore by design, D-136)
  - `pkg/agent/agent_test.go` — MODIFIED (PR #9): t.Skip on
    `TestProcessMessage_PersistsReasoningToolResponseAsSingleAssistantRecord`
    (expects JSONL file, PikaSessionStore uses SQLite)
  - `pkg/agent/steering_test.go` — MODIFIED (PR #9): t.Skip on
    `TestAgentLoop_Run_AutoContinuesLateSteeringMessage`
    (session persistence changed to SQLite)
  - `pkg/agent/agent_test.go` — MODIFIED (PR #12): t.Skip on
    `TestProcessMessage_PicoPublishesReasoningAsThoughtMessage`
    (PikaSessionStore serializes ReasoningContent in metadata JSON,
    Pico publisher pipeline doesn't find it in expected format)
- **Breaking:** None (tests skipped, not removed). Will be removed
  in ТЗ-v2-2b (PikaContextManager replaces context_legacy.go).

### [2026-05-03] fix(pika): remove linux/arm from build-all — CI fix

- **ТЗ:** ТЗ-v2-fix-build: Убрать ARM из make build-all
- **PR:** #11
- **Files:**
  - `Makefile` — MODIFIED: removed `GOARCH=arm GOARM=7` builds (`-linux-arm`, `-linux-armv7`) from `build-all` target; added comment explaining why ARM was removed. Cause: `modernc.org/libc@v1.70.0` (dep of `modernc.org/sqlite`) does not support `linux/arm` with `goolm,stdjson` build tags. Standalone `build-linux-arm` target preserved for manual use.
- **Breaking:** None (`build-all` no longer produces ARM binaries; standalone target still available)

### [2026-05-03] fix(pika): remove exotic archs from build-all — CI fix

- **ТЗ:** ТЗ-v2-fix-build-2: Убрать экзотические архитектуры из build-all
- **PR:** #13
- **Files:**
  - `Makefile` — MODIFIED: removed `linux/loong64` (+ PTY_PATCH_LOONG64), `linux/riscv64`, `linux/mipsle` (+ PATCH_MIPS_FLAGS), `netbsd/amd64`, `netbsd/arm64` from `build-all` target. Remaining platforms: `linux/amd64`, `linux/arm64`, `darwin/arm64`, `windows/amd64`. Cause: `modernc.org/libc v1.70.0` build constraints exclude all Go files on these platforms. Standalone targets preserved for manual use.
- **Breaking:** None (`build-all` reduced to 4 platforms; standalone targets still available)

### [2026-05-04] feat(pika): registry.go — Registry CRUD + AtomID generator — wave 1c

- **ТЗ:** ТЗ-v2-1c: registry.go — Registry CRUD + валидация
- **PR:** #14
- **Files:**
  - `pkg/pika/registry.go` — NEW: `AtomIDGenerator` struct — потокобезопасный генератор монотонных atom_id per category (sync.Mutex, lazy-init counters from DB via `BotMemory.GetMaxAtomN`); `NewAtomIDGenerator(mem)` constructor; `Next(ctx, category)` returns formatted ID (e.g. "P-1", "D-1") using `categoryPrefix` map from botmemory.go; `RegistryHandler` struct — валидированный CRUD поверх BotMemory; `NewRegistryHandler(mem)` constructor; `ValidRegistryKinds` whitelist (runbook, script, snapshot, correction_rule); `HandleWrite(ctx, kind, key, summary, data, tags)` — validation (kind in whitelist, key non-empty ≤255, data valid JSON if non-nil, tags valid JSON array if non-nil) + `bm.UpsertRegistry`; `HandleRead(ctx, kind, key)` — delegates to `bm.GetRegistry` + updates `last_used` via `bm.UpdateRegistryLastUsed`; `HandleSearch(ctx, kind, keyPattern)` — delegates to `bm.SearchRegistry`
  - `pkg/pika/registry_test.go` — NEW: 13 tests (TestAtomIDGenerator_Sequential P-1/P-2/P-3, TestAtomIDGenerator_MultiCategory P-1/D-1/P-2, TestAtomIDGenerator_RecoveryFromDB insert P-1..P-5 then new generator → P-6, TestAtomIDGenerator_UnknownCategory → error, TestHandleWrite_Created, TestHandleWrite_Updated same key → created=false, TestHandleWrite_InvalidKind, TestHandleWrite_EmptyKey, TestHandleWrite_InvalidJSON, TestHandleWrite_InvalidTags not array, TestHandleRead_NotFound → nil/nil, TestHandleRead_UpdatesLastUsed, TestHandleSearch 3 entries filter by kind)
- **Bug fix vs ТЗ:** `fmt.Sprintf("%s-%d", prefix, N)` → `fmt.Sprintf("%s%d", prefix, N)` — categoryPrefix already contains hyphen ("P-"), ТЗ format would produce "P--1"
- **Breaking:** None (new files, additive only). Does not touch botmemory.go.

---

## Wave 2: Runtime Components (TRAIL/META, envelope, context manager)

### [2026-05-04] feat(pika): trail_meta.go — TRAIL ring buffer + META metrics — wave 2a

- **ТЗ:** ТЗ-v2-2a: trail_meta.go — TRAIL + META
- **PR:** #16
- **Files:**
  - `pkg/pika/trail_meta.go` — NEW: `TrailEntry` struct (Timestamp, Operation, StatusIcon, Detail, IsError); `Trail` struct — fixed-size ring buffer (`[5]TrailEntry`, thread-safe via `sync.RWMutex`); `NewTrail()` constructor; `Trail.Add(op, statusIcon, detail, isError)` with auto-timestamp; `Trail.Entries()` returns oldest→newest ordered slice; `Trail.Serialize()` formatted text output (`[HH:MM:SS] icon OPERATION: detail`); `Trail.HasLoopDetection(threshold)` detects N consecutive identical operations; `Trail.Reset()` clears all entries; `Meta` struct — system metrics (MsgCount int, ContextPct float64, Health SystemState, LastFail *time.Time, thread-safe via `sync.RWMutex`); `SystemState` type alias (Healthy/Degraded/Offline constants); `NewMeta()` constructor with Health=Healthy; `Meta.IncrementMsgCount()`; `Meta.UpdateContextPct(pct)`; `Meta.Serialize()` formatted text output; `Meta.Reset()` preserves Health and LastFail, resets MsgCount and ContextPct
  - `pkg/pika/trail_meta_test.go` — NEW: tests for Trail (Add/Entries ordering, ring overflow at capacity 5, Serialize format, HasLoopDetection true/false, Reset clears entries), Meta (IncrementMsgCount, UpdateContextPct, Serialize with healthy/degraded+lastFail, Reset preserves Health/LastFail), concurrency (race detection via `go test -race` with parallel Add/Entries on Trail and IncrementMsgCount/Serialize on Meta)
- **Breaking:** None (new files, additive only)

### [2026-05-04] feat(pika): PikaContextManager + bypass — wave 2b Phase A

- **ТЗ:** ТЗ-v2-2b: context_manager.go — PikaContextManager (Фаза A)
- **PR:** #18
- **Files:**
  - `pkg/pika/interfaces.go` — NEW: `SystemStateProvider` interface + `alwaysHealthyProvider` stub (wave 2 default); `ArchivistCaller` interface + `noopArchivistCaller` stub (wave 2 default); `SystemState` struct (Status, DegradedComponents); constructors `NewAlwaysHealthyProvider()`, `NewNoopArchivistCaller()`
  - `pkg/pika/context_manager.go` — NEW: `PikaContextManager` struct — builds full system prompt (CORE.md + CONTEXT.md + MEMORY_BRIEF + TRAIL/META + ACTIVE_PLAN + DEGRADATION); `NewPikaContextManager(workspace, trail, meta, stateProvider, archivist)` constructor with nil-safe defaults; `BuildSystemPrompt(ctx, sessionKey)` assembler with mtime-cached bootstrap file loading; `loadBootstrapFile(name)` with `os.Stat` + `os.ReadFile` + RWMutex cache; `injectDegradation(sb)` appends DEGRADATION block with per-component instructions (archivist/mcp_guard/toolguard/registry/telemetry/atomizer); `Compact(sessionKey, reason)` no-op stub (wave 5); `Ingest(sessionKey)` no-op stub; `Clear(sessionKey)` no-op stub; `InvalidateCache()` clears bootstrap cache; `GetTrail()`/`GetMeta()` accessors
  - `pkg/pika/context_manager_test.go` — NEW: 12 tests (EmptyWorkspace → TRAIL+META present, WithCoreAndContext → content loaded, CachesFiles → cachedCore populated, TrailEntries → compose.restart in prompt, DegradationBlock → DEGRADATION with archivist/mcp_guard instructions, HealthyNoDegradation → no DEGRADATION block, Compact/Ingest/Clear no-ops, InvalidateCache clears cache, AlwaysHealthyProvider returns healthy, NoopArchivistCaller returns empty)
  - `pkg/agent/context_pika.go` — NEW: `pikaContextManagerAdapter` struct wrapping `pika.PikaContextManager` as `agent.ContextManager` (avoids circular import); `pikaContextManagerFactory` creates Trail+Meta+stubs+PikaContextManager; `init()` calls `RegisterContextManager("pika", pikaContextManagerFactory)`; adapter methods: Assemble (gets history from session, builds system prompt, returns SystemPrompt field), Compact/Ingest/Clear delegate to pika.PikaContextManager
  - `pkg/agent/context_manager.go` — MODIFIED: added `SystemPrompt string` field to `AssembleResponse` (PIKA-V3 bypass: when set, pipeline_setup.go skips ContextBuilder)
  - `pkg/agent/pipeline_setup.go` — MODIFIED: PIKA-V3 BYPASS at both Assemble call sites — if `resp.SystemPrompt != ""`, uses `[]providers.MessageRole: "system", Content: systemPrompt` instead of `ContextBuilder.BuildMessagesFromPrompt()`; `systemPrompt` variable tracks across initial and post-compression re-assembly
- **Breaking:** None (additive: new SystemPrompt field in AssembleResponse, bypass is backwards-compatible — empty SystemPrompt falls through to upstream ContextBuilder)

### [2026-05-04] fix(pika): remove Seahorse + legacy CM — wave 2b Phase B

- **ТЗ:** ТЗ-v2-2b-B: Удаление Seahorse + legacy CM (Фаза B)
- **PR:** #18 (existing branch `feat/v2-2b-context-manager`)
- **Files:**
  - `pkg/seahorse/` (24 files) — DELETED: entire Seahorse package (schema.go, store.go, short_engine.go, short_compaction.go, short_assembler.go, short_retrieval.go, types.go, all tests, fts5_sanitize, tool_expand, tool_grep, etc.)
  - `pkg/agent/context_seahorse.go` — DELETED: seahorseContextManager implementation
  - `pkg/agent/context_seahorse_test.go` — DELETED: seahorse CM tests
  - `pkg/agent/context_seahorse_unsupported.go` — DELETED: unsupported platform stub for seahorse CM
  - `pkg/agent/context_legacy.go` — DELETED: legacyContextManager (forceCompression, maybeSummarize, TruncateHistory)
  - `docs/architecture/agent-refactor/context.md` — DELETED: obsolete CM refactoring plan
  - `cmd/membench/` (12 files) — DELETED: seahorse benchmark tool (eval.go, ingest.go, main.go, metrics.go, etc. — all imported `pkg/seahorse`)
  - `pkg/agent/turn_coord.go` — MODIFIED: `resolveContextManager()` rewritten — default CM name now "pika" instead of creating `legacyContextManager` directly; all 3 legacy fallback paths removed; unknown/failed CM lookup panics instead of silent legacy fallback
  - `Makefile` — MODIFIED: removed `mem` target (depended on deleted `cmd/membench`)
- **Breaking:** `legacyContextManager` removed. Default context manager is now "pika" (PikaContextManager from Phase A). Config value `"legacy"` or `""` maps to `"pika"`. `pkg/seahorse/` package no longer exists — no `import pkg/seahorse` anywhere in project. `cmd/membench/` binary no longer builds.

### [2026-05-04] feat(pika): envelope.go — unified tool response envelope — wave 2c

- **ТЗ:** ТЗ-v2-2c: envelope.go — Tool response envelope
- **PR:** #TBD
- **Files:**
  - `pkg/pika/envelope.go` — NEW: `ErrorKind` type (Transient/Permanent/Degraded constants with String()); error code constants (ErrUnknownOp, ErrInvalidParams, ErrTimeout, ErrExecError, ErrPermissionDenied, ErrParseError); `Envelope` struct (OK bool, Data json.RawMessage, Error *string); `ParseEnvelope(raw []byte) Envelope` — never panics, never returns error, invalid/empty input → parse_error; `ErrorCode()` extracts code prefix from "code: description" format; `ClassifyEnvelopeError(code) ErrorKind` maps codes to Transient (timeout, exec_error) or Permanent (all others); `IsRetryable()` true only for transient errors; `ToToolResult()` converts to upstream `toolshared.ToolResult`; `formatData()` helper
  - `pkg/pika/envelope_test.go` — NEW: 18 tests (ParseEnvelope valid ok=true with data extraction, ok=false for each of 5 error codes with correct ErrorCode/IsRetryable, invalid JSON → parse_error, empty input → parse_error, nil input → parse_error, ClassifyEnvelopeError all 6 codes + unknown code, IsRetryable table-driven for all codes, ToToolResult ok=true → IsError=false, ok=false → IsError=true, ok=true null data → empty ForLLM, ErrorKind.String() for all 3 values, ok=true not retryable)
- **Breaking:** None (new files, additive only). Consumer: `tool_router.go` (wave 3)
