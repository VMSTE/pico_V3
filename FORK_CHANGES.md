# FORK_CHANGES.md

Tracker of all structural changes made in the Pika fork vs upstream PicoClaw.
Each entry maps to a single wave/phase and its PR.

---

## [2026-04-30] feat(pika): migrate.go cleanup — wave 0a

- **ТЗ:** ТЗ-v2-0a: migrate.go — Legacy Cleanup
- **PR:** #1 (merged)
- **Files:**
  - `pkg/config/migration.go` — emptied (all migration logic removed, only `package config` remains)
  - `pkg/config/migration_test.go` — emptied
  - `pkg/config/migration_integration_test.go` — emptied
- **Breaking:** Config versions 0/1/2 no longer auto-migrate; only version 3 is supported.

## [2026-05-01] feat(pika): config.go unified config — wave 0b Phase 1

- **ТЗ:** ТЗ-v2-0b: config.go — Unified Config (pkg/config/)
- **PR:** #2 (merged)
- **Files:**
  - `pkg/config/config_pika.go` — NEW: 18 Pika v3 types (SecurityConfig, HealthConfig, ClarifyConfig, ReasoningConfig, BudgetConfig, etc.), ResolvedAgentConfig, ResolveAgentConfig(), BaseToolsConfig + IsBaseToolEnabled(), ConfirmMode with bool/string UnmarshalJSON
  - `pkg/config/config_pika_test.go` — NEW: tests for all new types, ResolveAgentConfig (unknown/inherit/override/full merge), BaseTools, ConfirmMode
  - `pkg/config/envkeys.go` — added PIKA_HOME, PIKA_CONFIG constants
- **Breaking:** None (additive only)

## [2026-05-02] feat(pika): config.go struct patching + legacy cleanup — wave 0b Phase 2

- **ТЗ:** ТЗ-v2-0b-p2: config.go — Phase 2
- **PR:** #5 (merged)
- **Files:**
  - `pkg/config/config.go` — MODIFIED: added Pika fields to Config, AgentDefaults (~15), AgentConfig (~30 pointer overrides), ModelConfig (+APIKeyEnv), ToolsConfig (+BaseTools); DEPRECATED: Isolation, Session, Devices, Voice; LoadConfig simplified (migration switch removed, APIKeyEnv resolve added, ContextManager default); loadConfig() moved from migration.go; Config.IsBaseToolEnabled() wrapper
  - `pkg/config/defaults.go` — MODIFIED: Pika defaults (MemoryDBPath, telemetry, retry, idle), Agents.List with main, Clarify/Security/Health/BaseTools defaults
  - `pkg/config/config_pika.go` — MODIFIED: completed ResolveAgentConfig() (all Pika fields resolved)
  - `pkg/config/config_pika_test.go` — MODIFIED: Phase 2 tests (DefaultConfig_PikaDefaults, Config_IsBaseToolEnabled, FullPikaMerge)
  - `pkg/config/migration.go` — EMPTIED (package config only)
  - `pkg/config/config_old.go` — EMPTIED
  - `pkg/config/legacy_bindings.go` — EMPTIED
  - `pkg/config/migration_test.go` — EMPTIED
  - `pkg/config/migration_integration_test.go` — EMPTIED
  - `pkg/config/example_security_usage.go` — EMPTIED
- **Breaking:** Config versions 0/1/2 no longer supported (migration switch removed)
