# FORK_CHANGES — pico_V3

Log of all changes made by Pika GitHub agent.

## [2026-05-01] feat(pika): add migrate.go — wave 0a

- **ТЗ:** ТЗ-v2-0a: migrate.go — Схема bot_memory.db
- **Branch:** `feat/tz-v2-0a-migrate`
- **Files:**
  - `pkg/pika/migrate.go` — `Migrate(dbPath)` + `CurrentVersion(db)`, PRAGMAs (WAL, FK, cache, busy_timeout), migration v0→v1 full DDL from SSOT (17 tables, 4 triggers, all indexes), transactional via `schema_version`
  - `pkg/pika/migrate_test.go` — 4 tests: new DB, idempotency, pragmas, FTS5 MATCH smoke
  - `FORK_CHANGES.md` — this file
