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
