# FORK_CHANGES.md — Pika v3 Hard Fork Changelog

### [2026-05-01] migrate.go — Idempotent schema migration for bot_memory.db
- What: Added `pkg/pika/migrate.go` + `pkg/pika/migrate_test.go`. `Migrate(dbPath)` opens/creates SQLite DB, sets PRAGMAs (WAL, FK, cache, busy_timeout), applies versioned migrations via `schema_version`. Migration v0→v1 creates full DDL (17 tables: 15 + 2 FTS5, 4 triggers, all indexes) from SSOT. `CurrentVersion(db)` returns current schema version. Tests: new DB, idempotency, pragmas, FTS5 smoke.
- Why: ТЗ-v2-0a — foundation for bot_memory.db runtime. DDL copied as-is from «Финальный DDL — bot_memory.db v3 (unified)».
- Breaking: нет
