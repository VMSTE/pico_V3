# Fork Changes — VMSTE/pico_V3

## Phase 1: config_pika.go (Pika v3 types)
- Added Pika v3 config types, ResolvedAgentConfig, BaseToolsConfig
- Added initial ResolveAgentConfig (upstream fields only)
- Added Phase 1 tests

## Phase 2: config.go struct patching + legacy cleanup
- Patched Config, AgentDefaults, AgentConfig, ModelConfig, ToolsConfig structs with Pika v3 fields
- Added Pika defaults to DefaultConfig()
- Completed ResolveAgentConfig with all Pika field resolution
- Simplified LoadConfig: removed migration switch (v0/v1/v2), added APIKeyEnv resolve
- Moved loadConfig helper from migration.go to config.go
- Added IsBaseToolEnabled wrapper on *Config
- Deleted legacy files: config_old.go, migration.go, migration_test.go,
  migration_integration_test.go, legacy_bindings.go, example_security_usage.go
