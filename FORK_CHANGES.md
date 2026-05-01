# Fork Changes (pico_V3)

This file tracks all Pika v3 modifications to the upstream picoclaw codebase.
Each entry corresponds to a ТЗ (technical specification) task.

---

## ТЗ-v2-0a: migrate.go — Миграция SQLite (pkg/config/)

**Branch:** `feat/tz-v2-0a-migrate`
**Status:** merged

- Added `pkg/config/migrate.go` — SQLite schema migration engine
- Added `pkg/config/migrate_test.go` — unit tests for migration
- Zero external dependencies, uses `database/sql` + `modernc.org/sqlite`

---

## ТЗ-v2-0b: config.go — Unified Config (pkg/config/)

**Branch:** `feat/tz-v2-0b-config-unified`
**Status:** in progress

### New files
- `pkg/config/config_pika.go` — all Pika v3 config types:
  - Cross-agent: `ClarifyConfig`, `SecurityConfig`, `HealthConfig`
  - Per-agent nested: `ReasoningConfig`, `BudgetConfig`, `OutputGateConfig`,
    `LoopConfig`, `MemoryBriefConfig`, `ArchiveConfig`, `ScheduleConfig`
  - `BaseToolsConfig` (D-TOOL-CLASS §4.1b)
  - `ResolvedAgentConfig` + `Config.ResolveAgentConfig()` for config merge
  - `Config.IsBaseToolEnabled()` for BASE tool master switch
  - `ConfirmMode` with bool/string JSON unmarshal
- `pkg/config/config_pika_test.go` — tests for all new types and functions

### Modified files
- `pkg/config/envkeys.go` — added `PIKA_HOME`, `PIKA_CONFIG`,
  `PIKA_BUILTIN_SKILLS`, `PIKA_BINARY`, `PIKA_DB_PATH` constants;
  `GetHome()` now prefers `PIKA_HOME` over `PICOCLAW_HOME`

### Pending (requires config.go struct modifications)
- Add fields to `Config` struct: `Clarify`, `Security`, `Health`
- Add fields to `AgentDefaults`: ~15 Pika-specific fields
- Add fields to `AgentConfig`: ~30 per-agent override fields (pointer types)
- Add `APIKeyEnv` to `ModelConfig`
- Add `BaseTools` to `ToolsConfig`
- Update `defaults.go` with Pika defaults
- Delete 6 legacy files: `config_old.go`, `migration.go`, `migration_test.go`,
  `migration_integration_test.go`, `legacy_bindings.go`, `example_security_usage.go`
- Update `LoadConfig`: remove migration switch, add Pika validation

**Note:** `config.go` is 65KB and exceeds MCP API read limits (~30KB).
Struct modifications and legacy deletions documented in PR description.
