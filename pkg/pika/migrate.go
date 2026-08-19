// Package pika implements the Pika v3 runtime for bot_memory.db.
// PIKA-V3: migrate.go — Idempotent schema migration for bot_memory.db
package pika

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Migrate opens (or creates) the SQLite database at dbPath, applies PRAGMAs,
// and runs all pending migrations. Returns the open *sql.DB handle.
func Migrate(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("pika/migrate: open %s: %w", dbPath, err)
	}

	// PIKA-V3: PRAGMAs — must run outside transaction
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA cache_size=-64000",
		"PRAGMA busy_timeout=5000",
	}
	for _, p := range pragmas {
		_, err = db.Exec(p)
		if err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("pika/migrate: pragma %q: %w", p, err)
		}
	}

	// PIKA-V3: Ensure schema_version table exists (framework-managed)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version     INTEGER PRIMARY KEY,
		applied_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
		description TEXT NOT NULL
	)`)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pika/migrate: create schema_version: %w", err)
	}

	// Current version
	ver, err := CurrentVersion(db)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pika/migrate: read version: %w", err)
	}

	// PIKA-V3: Migration registry
	type migration struct {
		version     int
		description string
		ddl         string
	}
	migrations := []migration{
		{version: 1, description: "unified v3 — initial schema", ddl: migrationV1},
		{version: 2, description: "rename session_id->chat_id, turn_id->pika_session_id", ddl: migrationV2},
		{version: 3, description: "messages_fts — FTS5+BM25 message search (D-AUDIT-106)", ddl: migrationV3},
		{version: 4, description: "request_log.agent_id — named agent identity (D-AUDIT-108)", ddl: migrationV4},
	}

	for _, m := range migrations {
		if m.version <= ver {
			continue
		}
		tx, txErr := db.Begin()
		if txErr != nil {
			_ = db.Close()
			return nil, fmt.Errorf("pika/migrate: begin v%d: %w", m.version, txErr)
		}

		_, execErr := tx.Exec(m.ddl)
		if execErr != nil {
			_ = tx.Rollback()
			_ = db.Close()
			return nil, fmt.Errorf("pika/migrate: apply v%d: %w", m.version, execErr)
		}
		_, execErr = tx.Exec(
			"INSERT INTO schema_version (version, description) VALUES (?, ?)",
			m.version, m.description,
		)
		if execErr != nil {
			_ = tx.Rollback()
			_ = db.Close()
			return nil, fmt.Errorf("pika/migrate: record v%d: %w", m.version, execErr)
		}
		if commitErr := tx.Commit(); commitErr != nil {
			_ = db.Close()
			return nil, fmt.Errorf("pika/migrate: commit v%d: %w", m.version, commitErr)
		}
	}

	return db, nil
}

// CurrentVersion returns the highest applied migration version (0 if none).
func CurrentVersion(db *sql.DB) (int, error) {
	var ver int
	err := db.QueryRow("SELECT COALESCE(MAX(version),0) FROM schema_version").Scan(&ver)
	if err != nil {
		return 0, fmt.Errorf("pika/migrate: current version: %w", err)
	}
	return ver, nil
}

// PIKA-V3: migrationV1 — full DDL from SSOT (Финальный DDL — bot_memory.db v3 unified)
// Excluded: §1 schema_version (framework-managed), §4 events_fts (D-93 deleted), §15 sessions (F12-15 deleted)
const migrationV1 = `
-- §2. messages — рабочий буфер
CREATE TABLE IF NOT EXISTS messages (
    id          INTEGER PRIMARY KEY,
    session_id  TEXT    NOT NULL,
    turn_id     INTEGER NOT NULL,
    ts          DATETIME DEFAULT CURRENT_TIMESTAMP,
    role        TEXT    NOT NULL
                        CHECK(role IN ('user','assistant','tool','system')),
    content     TEXT,
    tokens      INTEGER DEFAULT 0,
    msg_index   INTEGER,
    metadata    TEXT
);

CREATE INDEX idx_messages_session_turn  ON messages(session_id, turn_id);
CREATE INDEX idx_messages_session_index ON messages(session_id, msg_index);
CREATE INDEX idx_messages_ts            ON messages(ts);

-- §3. events — рабочий буфер
CREATE TABLE IF NOT EXISTS events (
    id          INTEGER PRIMARY KEY,
    ts          DATETIME DEFAULT CURRENT_TIMESTAMP,
    type        TEXT    NOT NULL,
    summary     TEXT    NOT NULL,
    outcome     TEXT    CHECK(outcome IN ('success','fail','partial')),
    tags        TEXT,
    data        TEXT,
    session_id  TEXT    NOT NULL,
    turn_id     INTEGER NOT NULL
);

CREATE INDEX idx_events_session_turn ON events(session_id, turn_id);
CREATE INDEX idx_events_type_outcome ON events(type, outcome);
CREATE INDEX idx_events_ts           ON events(ts);

-- §5. knowledge_atoms — атомы знаний
CREATE TABLE IF NOT EXISTS knowledge_atoms (
    id          INTEGER PRIMARY KEY,
    atom_id     TEXT UNIQUE NOT NULL,
    session_id  TEXT    NOT NULL,
    turn_id     INTEGER NOT NULL,
    source_event_id   INTEGER,
    source_message_id INTEGER,
    category    TEXT    NOT NULL
                        CHECK(category IN (
                            'pattern','constraint','decision',
                            'tool_pref','summary','runbook_draft'
                        )),
    summary     TEXT    NOT NULL,
    detail      TEXT,
    confidence  REAL    DEFAULT 0.5 CHECK(confidence BETWEEN 0 AND 1),
    polarity    TEXT    NOT NULL DEFAULT 'neutral'
                        CHECK(polarity IN ('positive','negative','neutral')),
    verified    INTEGER DEFAULT 0,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    tags         TEXT,
    source_turns TEXT,
    history      TEXT
);

CREATE INDEX idx_katoms_session    ON knowledge_atoms(session_id);
CREATE INDEX idx_katoms_category   ON knowledge_atoms(category);
CREATE INDEX idx_katoms_confidence ON knowledge_atoms(confidence DESC);
CREATE INDEX idx_katoms_verified   ON knowledge_atoms(verified, updated_at);
CREATE INDEX idx_katoms_created    ON knowledge_atoms(created_at DESC);

-- §6. knowledge_fts — FTS5
CREATE VIRTUAL TABLE IF NOT EXISTS knowledge_fts USING fts5(
    summary,
    detail,
    tags,
    content = knowledge_atoms,
    content_rowid = id
);

CREATE TRIGGER katoms_ai AFTER INSERT ON knowledge_atoms BEGIN
    INSERT INTO knowledge_fts(rowid, summary, detail, tags)
    VALUES (new.id, new.summary, new.detail, new.tags);
END;

CREATE TRIGGER katoms_ad AFTER DELETE ON knowledge_atoms BEGIN
    INSERT INTO knowledge_fts(knowledge_fts, rowid, summary, detail, tags)
    VALUES ('delete', old.id, old.summary, old.detail, old.tags);
END;

CREATE TRIGGER katoms_au AFTER UPDATE ON knowledge_atoms BEGIN
    INSERT INTO knowledge_fts(knowledge_fts, rowid, summary, detail, tags)
    VALUES ('delete', old.id, old.summary, old.detail, old.tags);
    INSERT INTO knowledge_fts(rowid, summary, detail, tags)
    VALUES (new.id, new.summary, new.detail, new.tags);
END;

-- §7. messages_archive — холодный архив сообщений (D-78)
CREATE TABLE IF NOT EXISTS messages_archive (
    id          INTEGER PRIMARY KEY,
    session_id  TEXT    NOT NULL,
    turn_id     INTEGER NOT NULL,
    ts          DATETIME NOT NULL,
    role        TEXT    NOT NULL,
    tokens      INTEGER DEFAULT 0,
    blob        BLOB
);

CREATE INDEX idx_msg_arch_session    ON messages_archive(session_id, turn_id);
CREATE INDEX idx_msg_arch_ts         ON messages_archive(ts);

-- §7a. events_archive — холодный архив событий (D-78)
CREATE TABLE IF NOT EXISTS events_archive (
    id          INTEGER PRIMARY KEY,
    session_id  TEXT    NOT NULL,
    turn_id     INTEGER NOT NULL,
    ts          DATETIME NOT NULL,
    type        TEXT    NOT NULL,
    outcome     TEXT,
    summary     TEXT,
    tags        TEXT,
    blob        BLOB
);

CREATE INDEX idx_evt_arch_session    ON events_archive(session_id, turn_id);
CREATE INDEX idx_evt_arch_ts         ON events_archive(ts);
CREATE INDEX idx_evt_arch_type       ON events_archive(type, outcome);

-- §7b. events_archive_fts — FTS5 (D-93)
CREATE VIRTUAL TABLE IF NOT EXISTS events_archive_fts USING fts5(
    summary,
    tags,
    content = events_archive,
    content_rowid = id
);

CREATE TRIGGER events_archive_ai AFTER INSERT ON events_archive BEGIN
    INSERT INTO events_archive_fts(rowid, summary, tags)
    VALUES (new.id, new.summary, new.tags);
END;

-- §8. registry — реестр runbooks / скриптов / снэпшотов / correction rules
CREATE TABLE IF NOT EXISTS registry (
    id        INTEGER PRIMARY KEY,
    ts        DATETIME DEFAULT CURRENT_TIMESTAMP,
    kind      TEXT NOT NULL CHECK(kind IN ('runbook','script','snapshot','correction_rule')),
    key       TEXT NOT NULL,
    summary   TEXT,
    data      TEXT,
    verified  INTEGER DEFAULT 0,
    last_used DATETIME,
    tags      TEXT,
    UNIQUE(kind, key)
);

CREATE INDEX idx_registry_kind_key  ON registry(kind, key);
CREATE INDEX idx_registry_last_used ON registry(last_used);

-- §9. request_log — телеметрия LLM
CREATE TABLE IF NOT EXISTS request_log (
    id                  INTEGER PRIMARY KEY,
    ts                  DATETIME DEFAULT CURRENT_TIMESTAMP,
    session_id          TEXT,
    msg_index           INTEGER,
    direction           TEXT,
    component           TEXT,
    model               TEXT,
    prompt_tokens       INTEGER,
    completion_tokens   INTEGER,
    cached_tokens       INTEGER DEFAULT 0,
    reasoning_tokens    INTEGER DEFAULT 0,
    total_tokens        INTEGER GENERATED ALWAYS AS (
                            prompt_tokens + completion_tokens
                        ) STORED,
    estimated_tokens    INTEGER,
    tool_calls_requested INTEGER DEFAULT 0,
    tool_calls_success   INTEGER DEFAULT 0,
    tool_calls_failed    INTEGER DEFAULT 0,
    tool_names          TEXT,
    cost_usd            REAL DEFAULT 0.0,
    prompt_lang         TEXT,
    error               TEXT,
    retry_count         INTEGER DEFAULT 0,
    response_ms         INTEGER,
    task_tag            TEXT,
    chain_id            TEXT,
    chain_position      INTEGER,
    context_tokens_cumulative INTEGER,
    plan_detected       INTEGER DEFAULT 0
);

CREATE INDEX idx_reqlog_session ON request_log(session_id, msg_index);
CREATE INDEX idx_reqlog_ts      ON request_log(ts);
CREATE INDEX idx_reqlog_model   ON request_log(model, ts);
CREATE INDEX idx_reqlog_cost    ON request_log(cost_usd DESC);
CREATE INDEX idx_reqlog_chain     ON request_log(chain_id, chain_position);
CREATE INDEX idx_reqlog_component ON request_log(component, ts);

-- §10. reasoning_log — reasoning отдельно
CREATE TABLE IF NOT EXISTS reasoning_log (
    id                INTEGER PRIMARY KEY,
    ts                DATETIME DEFAULT CURRENT_TIMESTAMP,
    session_id        TEXT,
    msg_index         INTEGER,
    task              TEXT,
    mode              TEXT,
    reasoning_text    TEXT,
    reasoning_tokens  INTEGER,
    prompt_components TEXT,
    tool_calls        TEXT,
    context_pct       REAL,
    reasoning_keywords TEXT,
    turn_id           INTEGER NOT NULL
);

CREATE INDEX idx_reason_session  ON reasoning_log(session_id, msg_index);
CREATE INDEX idx_reason_turn     ON reasoning_log(session_id, turn_id);
CREATE INDEX idx_reason_ts       ON reasoning_log(ts);

-- §10a. reasoning_log_archive — холодный архив reasoning (D-77, D-78)
CREATE TABLE IF NOT EXISTS reasoning_log_archive (
    id                 INTEGER PRIMARY KEY,
    session_id         TEXT    NOT NULL,
    turn_id            INTEGER NOT NULL,
    msg_index          INTEGER,
    ts                 DATETIME NOT NULL,
    task               TEXT,
    mode               TEXT,
    reasoning_tokens   INTEGER,
    reasoning_keywords TEXT,
    context_pct        REAL,
    blob               BLOB
);

CREATE INDEX idx_rlog_arch_session   ON reasoning_log_archive(session_id, turn_id);
CREATE INDEX idx_rlog_arch_ts        ON reasoning_log_archive(ts);

-- §11. trace_spans — единая трассировка (OTel-style)
CREATE TABLE IF NOT EXISTS trace_spans (
    span_id         TEXT PRIMARY KEY,
    parent_span_id  TEXT,
    trace_id        TEXT NOT NULL,
    session_id      TEXT,
    turn_id         INTEGER,
    component       TEXT NOT NULL,
    operation       TEXT NOT NULL,
    started_at      DATETIME NOT NULL,
    completed_at    DATETIME,
    duration_ms     INTEGER GENERATED ALWAYS AS (
                        CASE WHEN completed_at IS NOT NULL
                             THEN CAST((julianday(completed_at) - julianday(started_at)) * 86400000 AS INTEGER)
                             ELSE NULL
                        END
                    ) STORED,
    status          TEXT DEFAULT 'ok'
                        CHECK(status IN ('ok','error','timeout','cancelled')),
    error_type      TEXT,
    error_message   TEXT,
    input_data      TEXT,
    output_data     TEXT,
    stack_trace     TEXT,
    input_preview   TEXT,
    output_preview  TEXT,
    FOREIGN KEY (parent_span_id) REFERENCES trace_spans(span_id) ON DELETE SET NULL
);

CREATE INDEX idx_spans_trace          ON trace_spans(trace_id);
CREATE INDEX idx_spans_session        ON trace_spans(session_id, turn_id);
CREATE INDEX idx_spans_component_time ON trace_spans(component, started_at);
CREATE INDEX idx_spans_status         ON trace_spans(status, started_at);
CREATE INDEX idx_spans_parent         ON trace_spans(parent_span_id);

-- §12. prompt_versions — версионирование промтов
CREATE TABLE IF NOT EXISTS prompt_versions (
    prompt_id   TEXT PRIMARY KEY,
    component   TEXT NOT NULL
                    CHECK(component IN ('CORE','CONTEXT','ATOMIZER','REFLEXOR',
                                        'ARCHIVIST_BUILD','MCP_GUARD')),
    version     INTEGER NOT NULL,
    hash        TEXT UNIQUE NOT NULL,
    content     TEXT NOT NULL,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    created_by  TEXT DEFAULT 'system',
    change_description TEXT,
    UNIQUE(component, version)
);

CREATE INDEX idx_pver_component ON prompt_versions(component, version);

-- §13. prompt_snapshots — снимок промта на каждый запрос
CREATE TABLE IF NOT EXISTS prompt_snapshots (
    snapshot_id           TEXT PRIMARY KEY,
    trace_id              TEXT NOT NULL UNIQUE,
    session_id            TEXT NOT NULL,
    turn_id               INTEGER NOT NULL,
    core_prompt_id        TEXT,
    context_prompt_id     TEXT,
    brief_hash            TEXT,
    archivarius_version   TEXT,
    atomizer_version      TEXT,
    reflexor_version      TEXT,
    core_tokens           INTEGER DEFAULT 0,
    context_tokens        INTEGER DEFAULT 0,
    brief_tokens          INTEGER DEFAULT 0,
    trail_tokens          INTEGER DEFAULT 0,
    plan_tokens           INTEGER DEFAULT 0,
    total_tokens          INTEGER GENERATED ALWAYS AS (
                              core_tokens + context_tokens +
                              brief_tokens + trail_tokens + plan_tokens
                          ) STORED,
    full_prompt_hash      TEXT,
    full_prompt_preview   TEXT,
    built_at              DATETIME DEFAULT CURRENT_TIMESTAMP,
    build_duration_ms     INTEGER,
    FOREIGN KEY (core_prompt_id)    REFERENCES prompt_versions(prompt_id),
    FOREIGN KEY (context_prompt_id) REFERENCES prompt_versions(prompt_id)
);

CREATE INDEX idx_psnap_trace    ON prompt_snapshots(trace_id);
CREATE INDEX idx_psnap_versions ON prompt_snapshots(core_prompt_id, context_prompt_id);

-- §14. atom_usage — использование атомов в промтах
CREATE TABLE IF NOT EXISTS atom_usage (
    usage_id             INTEGER PRIMARY KEY,
    atom_id              TEXT    NOT NULL,
    trace_id             TEXT    NOT NULL,
    turn_id              INTEGER NOT NULL,
    used_in              TEXT    NOT NULL
                                CHECK(used_in IN ('BRIEF','CONTEXT','PLAN')),
    position_in_prompt   INTEGER,
    prompt_tokens        INTEGER,
    invoked_tool_after   TEXT,
    invoked_tool_result  TEXT,
    archivarius_span_id  TEXT,
    created_at           DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (atom_id)             REFERENCES knowledge_atoms(atom_id) ON DELETE CASCADE,
    FOREIGN KEY (archivarius_span_id) REFERENCES trace_spans(span_id)
);

CREATE INDEX idx_ausage_atom    ON atom_usage(atom_id, created_at DESC);
CREATE INDEX idx_ausage_trace   ON atom_usage(trace_id, turn_id);
CREATE INDEX idx_ausage_invoked ON atom_usage(invoked_tool_after, invoked_tool_result);
CREATE INDEX idx_ausage_arch    ON atom_usage(archivarius_span_id);

-- §16. daily_metrics — заглушка
CREATE TABLE IF NOT EXISTS daily_metrics (
    metric_date              DATE PRIMARY KEY,
    sessions_started         INTEGER DEFAULT 0,
    sessions_completed       INTEGER DEFAULT 0,
    avg_session_duration_ms  INTEGER,
    total_llm_requests       INTEGER DEFAULT 0,
    total_tokens             INTEGER DEFAULT 0,
    total_cost_usd           REAL    DEFAULT 0.0,
    avg_tokens_per_request   INTEGER,
    error_rate               REAL,
    tool_calls_by_name       TEXT,
    atoms_created            INTEGER DEFAULT 0,
    top_atoms_used           TEXT,
    prompt_version_distribution TEXT,
    p50_latency_ms           INTEGER,
    p95_latency_ms           INTEGER,
    p99_latency_ms           INTEGER
);
`

// PIKA-V3 (D-AUDIT-108): migrationV4 — request_log.agent_id.
// Стабильная идентичность агента: "main" / именованный агент (delegate target).
// Одноразовые spawn-субагенты пишут "" — дашборд группирует их отдельно.
// Старые строки получают '' (legacy). Колонка добавочная — откат тривиален.
const migrationV4 = `
ALTER TABLE request_log ADD COLUMN agent_id TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_reqlog_agent ON request_log(agent_id, ts);
`

// PIKA-V3: migrationV2 — rename session_id->chat_id, turn_id->pika_session_id (TEXT).
// Temporary pika_ prefix avoids confusion with upstream "session_id" scattered across code.
// Final rename pika_session_id->session_id will be a separate migration after full cleanup.
// NOTE: ALTER TABLE RENAME COLUMN does not change type affinity. Old INTEGER values in
// pika_session_id columns are auto-cast to TEXT by SQLite when read as string.
const migrationV2 = `
-- §1. messages
ALTER TABLE messages RENAME COLUMN session_id TO chat_id;
ALTER TABLE messages RENAME COLUMN turn_id TO pika_session_id;

-- §2. events
ALTER TABLE events RENAME COLUMN session_id TO chat_id;
ALTER TABLE events RENAME COLUMN turn_id TO pika_session_id;

-- §3. knowledge_atoms
ALTER TABLE knowledge_atoms RENAME COLUMN session_id TO chat_id;
ALTER TABLE knowledge_atoms RENAME COLUMN turn_id TO pika_session_id;

-- §4. reasoning_log
ALTER TABLE reasoning_log RENAME COLUMN session_id TO chat_id;
ALTER TABLE reasoning_log RENAME COLUMN turn_id TO pika_session_id;

-- §5. trace_spans
ALTER TABLE trace_spans RENAME COLUMN session_id TO chat_id;
ALTER TABLE trace_spans RENAME COLUMN turn_id TO pika_session_id;

-- §6. prompt_snapshots
ALTER TABLE prompt_snapshots RENAME COLUMN session_id TO chat_id;
ALTER TABLE prompt_snapshots RENAME COLUMN turn_id TO pika_session_id;

-- §7. request_log (session_id only, no turn_id)
ALTER TABLE request_log RENAME COLUMN session_id TO chat_id;

-- §8. messages_archive
ALTER TABLE messages_archive RENAME COLUMN session_id TO chat_id;
ALTER TABLE messages_archive RENAME COLUMN turn_id TO pika_session_id;

-- §9. events_archive
ALTER TABLE events_archive RENAME COLUMN session_id TO chat_id;
ALTER TABLE events_archive RENAME COLUMN turn_id TO pika_session_id;

-- §10. reasoning_log_archive
ALTER TABLE reasoning_log_archive RENAME COLUMN session_id TO chat_id;
ALTER TABLE reasoning_log_archive RENAME COLUMN turn_id TO pika_session_id;

-- §11. atom_usage (turn_id only, no session_id)
ALTER TABLE atom_usage RENAME COLUMN turn_id TO pika_session_id;

-- §12. Recreate indices with correct names.
-- SQLite RENAME COLUMN auto-updates column refs inside indices,
-- but index NAMES stay old and become misleading. Drop + recreate.

-- messages
DROP INDEX IF EXISTS idx_messages_session_turn;
DROP INDEX IF EXISTS idx_messages_session_index;
CREATE INDEX idx_messages_chat_session ON messages(chat_id, pika_session_id);
CREATE INDEX idx_messages_chat_index   ON messages(chat_id, msg_index);

-- events
DROP INDEX IF EXISTS idx_events_session_turn;
CREATE INDEX idx_events_chat_session ON events(chat_id, pika_session_id);

-- knowledge_atoms
DROP INDEX IF EXISTS idx_katoms_session;
CREATE INDEX idx_katoms_chat ON knowledge_atoms(chat_id);

-- messages_archive
DROP INDEX IF EXISTS idx_msg_arch_session;
CREATE INDEX idx_msg_arch_chat ON messages_archive(chat_id, pika_session_id);

-- events_archive
DROP INDEX IF EXISTS idx_evt_arch_session;
CREATE INDEX idx_evt_arch_chat ON events_archive(chat_id, pika_session_id);

-- request_log
DROP INDEX IF EXISTS idx_reqlog_session;
CREATE INDEX idx_reqlog_chat ON request_log(chat_id, msg_index);

-- reasoning_log
DROP INDEX IF EXISTS idx_reason_session;
DROP INDEX IF EXISTS idx_reason_turn;
CREATE INDEX idx_reason_chat         ON reasoning_log(chat_id, msg_index);
CREATE INDEX idx_reason_pika_session ON reasoning_log(chat_id, pika_session_id);

-- reasoning_log_archive
DROP INDEX IF EXISTS idx_rlog_arch_session;
CREATE INDEX idx_rlog_arch_chat ON reasoning_log_archive(chat_id, pika_session_id);

-- trace_spans
DROP INDEX IF EXISTS idx_spans_session;
CREATE INDEX idx_spans_chat ON trace_spans(chat_id, pika_session_id);

-- atom_usage
DROP INDEX IF EXISTS idx_ausage_trace;
CREATE INDEX idx_ausage_trace ON atom_usage(trace_id, pika_session_id);
`

// PIKA-V3: migrationV3 — messages_fts для поиска с BM25 (D-AUDIT-106).
// LIKE+счётчик слов топил факты в свежем шуме; BM25/IDF: редкое слово
// («синий» в 1 документе) весит больше повторяющегося шума.
// Паттерн = knowledge_fts: external content + триггеры + rebuild-бэкфилл.
const migrationV3 = `
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    content,
    content = messages,
    content_rowid = id
);

CREATE TRIGGER messages_fts_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, content) VALUES (new.id, new.content);
END;

CREATE TRIGGER messages_fts_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, content)
    VALUES ('delete', old.id, old.content);
END;

CREATE TRIGGER messages_fts_au AFTER UPDATE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, content)
    VALUES ('delete', old.id, old.content);
    INSERT INTO messages_fts(rowid, content) VALUES (new.id, new.content);
END;

INSERT INTO messages_fts(messages_fts) VALUES('rebuild');
`
