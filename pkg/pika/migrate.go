// PIKA-V3: migrate.go — Idempotent schema migration for bot_memory.db
package pika

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // PIKA-V3: pure Go SQLite driver (FTS5 enabled), driver name: "sqlite"
)

// PIKA-V3: migration represents a single schema migration step.
type migration struct {
	version     int
	description string
	sql         string
}

// PIKA-V3: migrations is the ordered list of all schema migrations.
// v0→v1 = full DDL from SSOT «Финальный DDL — bot_memory.db v3 (unified)».
var migrations = []migration{
	{
		version:     1,
		description: "unified v3 — initial schema",
		sql:         migrationV1,
	},
}

// Migrate opens (or creates) the database at dbPath,
// sets PRAGMAs, and applies missing migrations via schema_version.
// Idempotent — safe to call on every startup.
func Migrate(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("pika/migrate: open db: %w", err)
	}

	// PIKA-V3: PRAGMAs from DDL §0
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA cache_size = -64000",
		"PRAGMA busy_timeout = 5000",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("pika/migrate: pragmas: %w", err)
		}
	}

	// PIKA-V3: bootstrap schema_version for migration tracking
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version     INTEGER PRIMARY KEY,
		applied_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
		description TEXT NOT NULL
	)`); err != nil {
		db.Close()
		return nil, fmt.Errorf("pika/migrate: create schema_version: %w", err)
	}

	cur, err := CurrentVersion(db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("pika/migrate: read version: %w", err)
	}

	for _, m := range migrations {
		if m.version <= cur {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("pika/migrate: v%d: begin tx: %w", m.version, err)
		}
		if _, err := tx.Exec(m.sql); err != nil {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("pika/migrate: v%d: %w", m.version, err)
		}
		if _, err := tx.Exec(
			"INSERT INTO schema_version (version, description) VALUES (?, ?)",
			m.version, m.description,
		); err != nil {
			tx.Rollback()
			db.Close()
			return nil, fmt.Errorf("pika/migrate: v%d: record version: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			db.Close()
			return nil, fmt.Errorf("pika/migrate: v%d: commit: %w", m.version, err)
		}
	}

	return db, nil
}

// CurrentVersion returns the current schema version (0 if no migrations applied).
func CurrentVersion(db *sql.DB) (int, error) {
	var v int
	err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("pika/migrate: current version: %w", err)
	}
	return v, nil
}

// PIKA-V3: migrationV1 — full DDL from SSOT «Финальный DDL — bot_memory.db v3 (unified)».
// §1 (schema_version) handled by migration framework above.
// §4 (events_fts) deleted per D-93 → replaced by events_archive_fts (§7b).
// §15 (sessions) deleted per F12-15.
const migrationV1 = `
-- §2: messages — рабочий буфер
CREATE TABLE IF NOT EXISTS messages (
    id          INTEGER PRIMARY KEY,
    session_id  TEXT    NOT NULL,
    turn_id     INTEGER NOT NULL,
    ts          DATETIME DEFAULT CURRENT_TIMESTAMP,
    role        TEXT    NOT NULL
                        CHECK(role IN ('user','assistant','tool','system')),
    -- F12-9: 'tool_call'/'tool_result' → 'tool' (стандарт OpenAI API).
    -- tool call = role:'assistant' + metadata.tool_calls JSON.
    -- tool result = role:'tool' + metadata.tool_call_id.
    content     TEXT,
    tokens      INTEGER DEFAULT 0,
    msg_index   INTEGER,
    metadata    TEXT                    -- JSON (tool_calls, tool_call_id, reasoning…)
    -- D-82: UNIQUE(session_id, turn_id, role) убран.
    -- В agentic loop один turn → несколько assistant/tool записей.
    -- id PK гарантирует уникальность и порядок.
);

CREATE INDEX idx_messages_session_turn  ON messages(session_id, turn_id);
CREATE INDEX idx_messages_session_index ON messages(session_id, msg_index);
CREATE INDEX idx_messages_ts            ON messages(ts);

-- §3: events — рабочий буфер
CREATE TABLE IF NOT EXISTS events (
    id          INTEGER PRIMARY KEY,
    ts          DATETIME DEFAULT CURRENT_TIMESTAMP,
    type        TEXT    NOT NULL,       -- F7-3: intermediate type напрямую (compose_restart, grafana_export, ...)
                                        -- Go startup validation вместо DDL CHECK (validTypes set)
    summary     TEXT    NOT NULL,
    outcome     TEXT    CHECK(outcome IN ('success','fail','partial')),
    tags        TEXT,                   -- JSON array
    data        TEXT,                   -- JSON payload
    session_id  TEXT    NOT NULL,
    turn_id     INTEGER NOT NULL
);

CREATE INDEX idx_events_session_turn ON events(session_id, turn_id);
CREATE INDEX idx_events_type_outcome ON events(type, outcome);
CREATE INDEX idx_events_ts           ON events(ts);

-- §5: knowledge_atoms — атомы знаний
CREATE TABLE IF NOT EXISTS knowledge_atoms (
    id          INTEGER PRIMARY KEY,    -- D-116: стабильный rowid для FTS5 content_rowid
    atom_id     TEXT UNIQUE NOT NULL,   -- D-103: Go генерирует '{PREFIX}-{N}'
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
    confidence  REAL    DEFAULT 0.5 CHECK(confidence BETWEEN 0 AND 1),  -- F8-6
    polarity    TEXT    NOT NULL DEFAULT 'neutral'
                        CHECK(polarity IN ('positive','negative','neutral')),
    verified    INTEGER DEFAULT 0,
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    tags         TEXT,                   -- JSON array
    source_turns TEXT,                   -- JSON
    history      TEXT                    -- JSON
);

CREATE INDEX idx_katoms_session    ON knowledge_atoms(session_id);
CREATE INDEX idx_katoms_category   ON knowledge_atoms(category);
CREATE INDEX idx_katoms_confidence ON knowledge_atoms(confidence DESC);
CREATE INDEX idx_katoms_verified   ON knowledge_atoms(verified, updated_at);
CREATE INDEX idx_katoms_created    ON knowledge_atoms(created_at DESC);

-- §6: knowledge_fts — FTS5
-- D-75: FTS5 по summary + detail + tags
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

-- §7: messages_archive — холодный архив сообщений (D-78)
CREATE TABLE IF NOT EXISTS messages_archive (
    id          INTEGER PRIMARY KEY,    -- тот же id что был в messages
    session_id  TEXT    NOT NULL,
    turn_id     INTEGER NOT NULL,
    ts          DATETIME NOT NULL,
    role        TEXT    NOT NULL,
    tokens      INTEGER DEFAULT 0,
    blob        BLOB                    -- zstd(content + metadata JSON)
);

CREATE INDEX idx_msg_arch_session    ON messages_archive(session_id, turn_id);
CREATE INDEX idx_msg_arch_ts         ON messages_archive(ts);

-- §7a: events_archive — холодный архив событий (D-78)
CREATE TABLE IF NOT EXISTS events_archive (
    id          INTEGER PRIMARY KEY,    -- тот же id что был в events
    session_id  TEXT    NOT NULL,
    turn_id     INTEGER NOT NULL,
    ts          DATETIME NOT NULL,
    type        TEXT    NOT NULL,
    outcome     TEXT,
    summary     TEXT,                   -- D-94: открытая колонка
    tags        TEXT,                   -- D-94: открытая колонка JSON array
    blob        BLOB                    -- zstd(data JSON only)
);

CREATE INDEX idx_evt_arch_session    ON events_archive(session_id, turn_id);
CREATE INDEX idx_evt_arch_ts         ON events_archive(ts);
CREATE INDEX idx_evt_arch_type       ON events_archive(type, outcome);

-- §7b: events_archive_fts — FTS5 (D-93)
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

-- §8: registry — реестр runbooks / скриптов / снэпшотов / correction rules
CREATE TABLE IF NOT EXISTS registry (
    id        INTEGER PRIMARY KEY,
    ts        DATETIME DEFAULT CURRENT_TIMESTAMP,
    kind      TEXT NOT NULL CHECK(kind IN ('runbook','script','snapshot','correction_rule')),
    key       TEXT NOT NULL,
    summary   TEXT,
    data      TEXT,                     -- JSON payload
    verified  INTEGER DEFAULT 0,
    last_used DATETIME,
    tags      TEXT,                     -- JSON array
    UNIQUE(kind, key)
);

CREATE INDEX idx_registry_kind_key  ON registry(kind, key);
CREATE INDEX idx_registry_last_used ON registry(last_used);

-- §9: request_log — телеметрия LLM
CREATE TABLE IF NOT EXISTS request_log (
    id                  INTEGER PRIMARY KEY,
    ts                  DATETIME DEFAULT CURRENT_TIMESTAMP,
    session_id          TEXT,
    msg_index           INTEGER,
    direction           TEXT,           -- 'chat','build_prompt','atomize','review','mcp_guard'
    component           TEXT,           -- 'main','archivarius','atomizer','reflexor' (D-83)
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
    tool_names          TEXT,           -- JSON array
    cost_usd            REAL DEFAULT 0.0,
    prompt_lang         TEXT,           -- 'ru', 'en'
    error               TEXT,
    retry_count         INTEGER DEFAULT 0,
    response_ms         INTEGER,
    task_tag            TEXT,
    chain_id            TEXT,           -- UUID цепочки tool calls (D-51)
    chain_position      INTEGER,        -- позиция в цепочке
    context_tokens_cumulative INTEGER,
    plan_detected       INTEGER DEFAULT 0
);

CREATE INDEX idx_reqlog_session   ON request_log(session_id, msg_index);
CREATE INDEX idx_reqlog_ts        ON request_log(ts);
CREATE INDEX idx_reqlog_model     ON request_log(model, ts);
CREATE INDEX idx_reqlog_cost      ON request_log(cost_usd DESC);
CREATE INDEX idx_reqlog_chain     ON request_log(chain_id, chain_position);
CREATE INDEX idx_reqlog_component ON request_log(component, ts);

-- §10: reasoning_log — reasoning отдельно
CREATE TABLE IF NOT EXISTS reasoning_log (
    id                INTEGER PRIMARY KEY,
    ts                DATETIME DEFAULT CURRENT_TIMESTAMP,
    session_id        TEXT,
    msg_index         INTEGER,
    task              TEXT,             -- FOCUS.TASK
    mode              TEXT,             -- FOCUS.MODE
    reasoning_text    TEXT,
    reasoning_tokens  INTEGER,
    prompt_components TEXT,             -- JSON
    tool_calls        TEXT,             -- JSON array
    context_pct       REAL,             -- % контекстного окна
    reasoning_keywords TEXT,            -- JSON array (D-62)
    turn_id           INTEGER NOT NULL  -- D-78
);

CREATE INDEX idx_reason_session ON reasoning_log(session_id, msg_index);
CREATE INDEX idx_reason_turn    ON reasoning_log(session_id, turn_id);
CREATE INDEX idx_reason_ts      ON reasoning_log(ts);

-- §10a: reasoning_log_archive — холодный архив reasoning (D-77, D-78)
CREATE TABLE IF NOT EXISTS reasoning_log_archive (
    id                 INTEGER PRIMARY KEY,  -- тот же id что был в reasoning_log
    session_id         TEXT    NOT NULL,
    turn_id            INTEGER NOT NULL,
    msg_index          INTEGER,
    ts                 DATETIME NOT NULL,
    task               TEXT,
    mode               TEXT,
    reasoning_tokens   INTEGER,
    reasoning_keywords TEXT,                 -- D-86: JSON array
    context_pct        REAL,                 -- D-86
    blob               BLOB                 -- zstd(reasoning_text + tool_calls + prompt_components)
);

CREATE INDEX idx_rlog_arch_session ON reasoning_log_archive(session_id, turn_id);
CREATE INDEX idx_rlog_arch_ts      ON reasoning_log_archive(ts);

-- §11: trace_spans — единая трассировка (OTel-style)
CREATE TABLE IF NOT EXISTS trace_spans (
    span_id         TEXT PRIMARY KEY,   -- UUID
    parent_span_id  TEXT,               -- для дерева вызовов
    trace_id        TEXT NOT NULL,      -- UUID цепочки
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

    input_data      TEXT,               -- JSON
    output_data     TEXT,               -- JSON

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

-- §12: prompt_versions — версионирование промтов
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

-- §13: prompt_snapshots — снимок промта на каждый запрос
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

-- §14: atom_usage — использование атомов в промтах
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

-- §16: daily_metrics — заглушка (D-118)
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
    tool_calls_by_name       TEXT,      -- JSON
    atoms_created            INTEGER DEFAULT 0,
    top_atoms_used           TEXT,      -- JSON
    prompt_version_distribution TEXT,   -- JSON
    p50_latency_ms           INTEGER,
    p95_latency_ms           INTEGER,
    p99_latency_ms           INTEGER
);
`
