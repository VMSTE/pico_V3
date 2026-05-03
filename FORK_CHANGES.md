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

## Wave 1: CRUD Layer (botmemory)

### [2026-05-02] feat(pika): botmemory.go — CRUD layer for bot_memory.db — wave 1a

- **ТЗ:** ТЗ-v2-1a: botmemory.go — CRUD bot_memory.db
- **PR:** #7
- **Files:**
  - `pkg/pika/botmemory.go` — NEW: `BotMemory` struct (sole SQL access layer for bot_memory.db); zstd Encoder/Decoder; `NewBotMemory(db)` constructor with crash-recovery (`recoverStaleSpans`); `Close()`; all row types (MessageRow, EventRow, KnowledgeAtomRow, RegistryRow, RequestLogRow, ReasoningLogRow, TraceSpanRow, EventArchiveRow); Messages CRUD (SaveMessage, GetMessages, GetDistinctSessionIDs, SumTokensBySession, GetOldestTurnIDs, CountMessagesBySession, GetMaxTurnID, DeleteAllMessages); Events (SaveEvent, GetEventsByTurns); Knowledge Atoms (InsertAtom, QueryKnowledgeFTS, UpdateAtomConfidence, GetMaxAtomN with category→prefix map); Registry (UpsertRegistry INSERT OR IGNORE + UPDATE, GetRegistry, SearchRegistry, UpdateRegistryLastUsed); Request/Reasoning Log (InsertRequestLog, InsertReasoningLog, GetReasoningByTurns); Trace Spans (InsertSpan, CompleteSpan, recoverStaleSpans); `ArchiveAndDeleteTurns` transactional archiver (messages→messages_archive with zstd blob, events→events_archive with zstd blob, reasoning_log→reasoning_log_archive with zstd blob, then DELETE hot); Archive Read (ReadArchivedMessage with decompress, SearchEventsArchiveFTS); Prompt Versions (UpsertPromptVersion, InsertPromptSnapshot); Atom Usage (InsertAtomUsage)
  - `pkg/pika/botmemory_test.go` — NEW: 14 tests (SaveAndGetMessages, SumTokensAndCount, GetMaxTurnID, GetOldestTurnIDs, SaveAndGetEvents, UpsertRegistry, SearchRegistry, InsertSpanAndRecover/crash_recovery, InsertAndCompleteSpan, ArchiveAndDeleteTurns, ArchiveTransactionRollback PK conflict, PromptVersionsAndSnapshots, AtomUsage, GetMaxAtomN, UpdateAtomConfidence)
- **Breaking:** None (new file, additive only)

### [2026-05-02] fix(pika): botmemory.go — 5 SQL bugs vs DDL — wave 1a-fix

- **ТЗ:** ТЗ-v2-1a-fix: Фикс PR #7 — 5 багов botmemory.go
- **PR:** #7 (updated)
- **Files:**
  - `pkg/pika/botmemory.go` — MODIFIED: Bug 2: UpsertPromptVersion — column `body` → proper DDL columns (prompt_id, component, version, hash, content, change_description), new signature returns (string, error); Bug 3: InsertPromptSnapshot — removed non-existent columns `component`, `prompt_hash`, now uses full DDL columns (snapshot_id, trace_id, session_id, turn_id, core/context/brief/trail/plan tokens, full_prompt_hash, etc.); Bug 4: InsertAtomUsage — removed non-existent columns `component`, `included`, now uses DDL columns (atom_id, trace_id, turn_id, used_in, position_in_prompt, prompt_tokens, invoked_tool_after/result, archivarius_span_id); Bug 5: ArchiveAndDeleteTurns — removed `msg_index` from messages_archive INSERT (column not in DDL); added `strconv` import
  - `pkg/pika/botmemory_test.go` — MODIFIED: Bug 1: setupTestDB and TestInsertSpanAndRecover — Migrate returns (*sql.DB, error), removed redundant sql.Open; setupTestDB now returns *BotMemory (not *sql.DB, *BotMemory); TestPromptVersionsAndSnapshots updated for new UpsertPromptVersion/InsertPromptSnapshot signatures; TestAtomUsage updated for new InsertAtomUsage signature with FK-valid atoms
- **Breaking:** Signature changes: UpsertPromptVersion, InsertPromptSnapshot, InsertAtomUsage (no external consumers yet)

### [2026-05-03] feat(pika): session_store.go — PikaSessionStore — wave 1b

- **ТЗ:** ТЗ-v2-1b: session_store.go — PikaSessionStore
- **PR:** #8
- **Files:**
  - `pkg/pika/session_store.go` — NEW: `PikaSessionStore` struct implementing `session.SessionStore` interface with SQLite WAL backend via BotMemory; compile-time interface check `var _ session.SessionStore = (*PikaSessionStore)(nil)`; per-session turn_id counter with mutex + DB recovery via `GetMaxTurnID`; `AddFullMessage` — persists messages through BotMemory with turn_id management (user→increment, other→same turn), token estimation via `tokenizer.EstimateMessageTokens`, metadata serialization (tool_calls, tool_call_id); `AddMessage` — convenience wrapper; `GetHistory` — retrieves messages from BotMemory with metadata deserialization back to `providers.Message` fields; `ListSessions` — delegates to `BotMemory.GetDistinctSessionIDs`; `SetHistory` — delete-all + re-insert (legacy compat); `GetSummary`/`SetSummary` — in-memory cache (transitional for legacy ContextManager); `TruncateHistory` — no-op (session rotation replaces truncation); `Save` — no-op (SQLite WAL = immediate persistence); `Close` — no-op (BotMemory manages db lifecycle)
  - `pkg/pika/session_store_test.go` — NEW: 8 tests (AddFullMessage_and_GetHistory, AddMessage_convenience, TurnID_management user/assistant grouping, Metadata_tool_calls round-trip, Metadata_tool_call_id round-trip, SetHistory_replaces, ListSessions, Summary_cache)
  - `pkg/agent/instance.go` — MODIFIED: removed `"context"` import; replaced `"github.com/sipeed/picoclaw/pkg/memory"` with `"github.com/sipeed/picoclaw/pkg/pika"`; removed `sessionsDir` variable; `initSessionStore` now takes `*config.Config` (was `dir string`), uses `pika.Migrate(cfg.Agents.Defaults.MemoryDBPath)` → `pika.NewBotMemory(db)` → `pika.NewPikaSessionStore(mem)`; panics on init errors with `logger.ErrorCF` logging
  - `pkg/memory/store.go` — DELETED (dead code, no longer imported)
  - `pkg/memory/jsonl.go` — DELETED (dead code, no longer imported)
  - `pkg/memory/jsonl_test.go` — DELETED (dead code, no longer imported)
  - `pkg/memory/migration.go` — DELETED (dead code, no longer imported)
  - `pkg/memory/migration_test.go` — DELETED (dead code, no longer imported)
- **Breaking:** `pkg/memory/` package removed entirely. `initSessionStore` signature changed from `(dir string)` to `(*config.Config)`. Session storage backend changed from JSONL/JSON files to SQLite WAL via bot_memory.db.

### [2026-05-03] fix(pika): steering.go undefined + session_test.go pkg/memory — wave 1b-fix2

- **ТЗ:** ТЗ-v2-1b-fix2: Migrate signature + проверка реальных типов
- **PR:** #8 (updated)
- **Files:**
  - `pkg/session/metadata.go` — NEW: `MetadataAwareSessionStore` interface extending `SessionStore` with `GetSessionScope(sessionKey) *SessionScope` — fixes CI error `undefined: session.MetadataAwareSessionStore` in `pkg/agent/steering.go:396`
  - `web/backend/api/session_test.go` — MODIFIED: removed `"github.com/sipeed/picoclaw/pkg/memory"` import (package deleted in wave 1b); replaced all `memory.NewJSONLStore(dir)` with local `testJSONLWriter` helper that writes JSONL + .meta.json files directly; replaced `memory.SessionMeta{...}` with local `sessionMeta{...}` (already defined in session.go); all 27 tests preserved with identical assertions
- **Verified (no fix needed):**
  - Баг 1 (Migrate signature): `instance.go` already uses correct `db, err := pika.Migrate(dbPath)` — no double `sql.Open`
  - Баг 2a (tokenizer): `tokenizer.EstimateMessageTokens(msg)` exists and is used correctly in `session_store.go`
  - Баг 2b (providers.Message fields): `ToolCalls []ToolCall` and `ToolCallID string` match exactly
  - Баг 2c (SessionStore interface): PikaSessionStore satisfies all methods with compile-time check
  - Задача 4 (jsonl_backend.go): already deleted, not present in `pkg/session/`
- **Breaking:** None (additive interface + test fix only)
