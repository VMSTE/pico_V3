// migrate.go — idempotent migration of bot_memory.db and *sql.DB for runtime. PIKA-V3.

package pika

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

func Migrate(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("pika/migrate: open: %w", err)
	}
	if err := setPragmas(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pika/migrate: pragmas: %w", err)
	}
	if err := ensureVersionTable(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pika/migrate: version table: %w", err)
	}
	if err := applyMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pika/migrate: apply: %w", err)
	}
	return db, nil
}

func CurrentVersion(db *sql.DB) (int, error) {
	var v int
	err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&v)
	if err != nil {
		return 0, fmt.Errorf("pika/migrate: current version: %w", err)
	}
	return v, nil
}

func setPragmas(db *sql.DB) error {
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA cache_size = -64000",
		"PRAGMA busy_timeout = 5000",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	return nil
}

func ensureVersionTable(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version     INTEGER PRIMARY KEY,
		applied_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
		description TEXT NOT NULL
	)`)
	return err
}

func applyMigrations(db *sql.DB) error {
	current, err := CurrentVersion(db)
	if err != nil {
		return err
	}
	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin v%d: %w", m.version, err)
		}
		if _, err := tx.Exec(m.ddl); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("exec v%d: %w", m.version, err)
		}
		if _, err := tx.Exec(
			"INSERT INTO schema_version (version, description) VALUES (?, ?)",
			m.version, m.description,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record v%d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit v%d: %w", m.version, err)
		}
	}
	return nil
}

type migration struct {
	version     int
	description string
	ddl         string
}

var migrations = []migration{
	{
		version:     1,
		description: "unified v3 — initial schema (17 tables: 15 + 2 FTS5)",
		ddl:         migrationV1DDL,
	},
}

const migrationV1DDL = `
-- §2. messages
CREATE TABLE IF NOT EXISTS messages (
    id          INTEGER PRIMARY KEY,
    session_id  TEXT    NOT NULL,
    turn_id     INTEGER NOT NULL,
    ts          DATETIME DEFAULT CURRENT_TIMESTAMP,
    role        TEXT    NOT NULL CHECK(role IN ('user','assistant','tool','system')),
    content     TEXT,
    tokens      INTEGER DEFAULT 0,
    msg_index   INTEGER,
    metadata    TEXT
);
CREATE INDEX idx_messages_session_turn  ON messages(session_id, turn_id);
CREATE INDEX idx_messages_session_index ON messages(session_id, msg_index);
CREATE INDEX idx_messages_ts            ON messages(ts);

-- §3. events
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

-- §5. knowledge_atoms
CREATE TABLE IF NOT EXISTS knowledge_atoms (
    id          INTEGER PRIMARY KEY,
    atom_id     TEXT UNIQUE NOT NULL,
    session_id  TEXT    NOT NULL,
    turn_id     INTEGER NOT NULL,
    source_event_id   INTEGER,
    source_message_id INTEGER,
    category    TEXT    NOT NULL CHECK(category IN ('pattern','constraint','decision','tool_pref','summary','runbook_draft')),
    summary     TEXT    NOT NULL,
    detail      TEXT,
    confidence  REAL    DEFAULT 0.5 CHECK(confidence BETWEEN 0 AND 1),
    polarity    TEXT    NOT NULL DEFAULT 'neutral' CHECK(polarity IN ('positive','negative','neutral')),
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

CREATE VIRTUAL TABLE IF NOT EXISTS knowledge_fts USING fts5(summary, detail, tags, content = knowledge_atoms, content_rowid = id);
CREATE TRIGGER katoms_ai AFTER INSERT ON knowledge_atoms BEGIN INSERT INTO knowledge_fts(rowid, summary, detail, tags) VALUES (new.id, new.summary, new.detail, new.tags); END;
CREATE TRIGGER katoms_ad AFTER DELETE ON knowledge_atoms BEGIN INSERT INTO knowledge_fts(knowledge_fts, rowid, summary, detail, tags) VALUES ('delete', old.id, old.summary, old.detail, old.tags); END;
CREATE TRIGGER katoms_au AFTER UPDATE ON knowledge_atoms BEGIN INSERT INTO knowledge_fts(knowledge_fts, rowid, summary, detail, tags) VALUES ('delete', old.id, old.summary, old.detail, old.tags); INSERT INTO knowledge_fts(rowid, summary, detail, tags) VALUES (new.id, new.summary, new.detail, new.tags); END;

CREATE TABLE IF NOT EXISTS messages_archive (id INTEGER PRIMARY KEY, session_id TEXT NOT NULL, turn_id INTEGER NOT NULL, ts DATETIME NOT NULL, role TEXT NOT NULL, tokens INTEGER DEFAULT 0, blob BLOB);
CREATE INDEX idx_msg_arch_session ON messages_archive(session_id, turn_id);
CREATE INDEX idx_msg_arch_ts ON messages_archive(ts);

CREATE TABLE IF NOT EXISTS events_archive (id INTEGER PRIMARY KEY, session_id TEXT NOT NULL, turn_id INTEGER NOT NULL, ts DATETIME NOT NULL, type TEXT NOT NULL, outcome TEXT, summary TEXT, tags TEXT, blob BLOB);
CREATE INDEX idx_evt_arch_session ON events_archive(session_id, turn_id);
CREATE INDEX idx_evt_arch_ts ON events_archive(ts);
CREATE INDEX idx_evt_arch_type ON events_archive(type, outcome);

CREATE VIRTUAL TABLE IF NOT EXISTS events_archive_fts USING fts5(summary, tags, content = events_archive, content_rowid = id);
CREATE TRIGGER events_archive_ai AFTER INSERT ON events_archive BEGIN INSERT INTO events_archive_fts(rowid, summary, tags) VALUES (new.id, new.summary, new.tags); END;

CREATE TABLE IF NOT EXISTS registry (id INTEGER PRIMARY KEY, ts DATETIME DEFAULT CURRENT_TIMESTAMP, kind TEXT NOT NULL CHECK(kind IN ('runbook','script','snapshot','correction_rule')), key TEXT NOT NULL, summary TEXT, data TEXT, verified INTEGER DEFAULT 0, last_used DATETIME, tags TEXT, UNIQUE(kind, key));
CREATE INDEX idx_registry_kind_key ON registry(kind, key);
CREATE INDEX idx_registry_last_used ON registry(last_used);

CREATE TABLE IF NOT EXISTS request_log (id INTEGER PRIMARY KEY, ts DATETIME DEFAULT CURRENT_TIMESTAMP, session_id TEXT, msg_index INTEGER, direction TEXT, component TEXT, model TEXT, prompt_tokens INTEGER, completion_tokens INTEGER, cached_tokens INTEGER DEFAULT 0, reasoning_tokens INTEGER DEFAULT 0, total_tokens INTEGER GENERATED ALWAYS AS (prompt_tokens + completion_tokens) STORED, estimated_tokens INTEGER, tool_calls_requested INTEGER DEFAULT 0, tool_calls_success INTEGER DEFAULT 0, tool_calls_failed INTEGER DEFAULT 0, tool_names TEXT, cost_usd REAL DEFAULT 0.0, prompt_lang TEXT, error TEXT, retry_count INTEGER DEFAULT 0, response_ms INTEGER, task_tag TEXT, chain_id TEXT, chain_position INTEGER, context_tokens_cumulative INTEGER, plan_detected INTEGER DEFAULT 0);
CREATE INDEX idx_reqlog_session ON request_log(session_id, msg_index);
CREATE INDEX idx_reqlog_ts ON request_log(ts);
CREATE INDEX idx_reqlog_model ON request_log(model, ts);
CREATE INDEX idx_reqlog_cost ON request_log(cost_usd DESC);
CREATE INDEX idx_reqlog_chain ON request_log(chain_id, chain_position);
CREATE INDEX idx_reqlog_component ON request_log(component, ts);

CREATE TABLE IF NOT EXISTS reasoning_log (id INTEGER PRIMARY KEY, ts DATETIME DEFAULT CURRENT_TIMESTAMP, session_id TEXT, msg_index INTEGER, task TEXT, mode TEXT, reasoning_text TEXT, reasoning_tokens INTEGER, prompt_components TEXT, tool_calls TEXT, context_pct REAL, reasoning_keywords TEXT, turn_id INTEGER NOT NULL);
CREATE INDEX idx_reason_session ON reasoning_log(session_id, msg_index);
CREATE INDEX idx_reason_turn ON reasoning_log(session_id, turn_id);
CREATE INDEX idx_reason_ts ON reasoning_log(ts);

CREATE TABLE IF NOT EXISTS reasoning_log_archive (id INTEGER PRIMARY KEY, session_id TEXT NOT NULL, turn_id INTEGER NOT NULL, msg_index INTEGER, ts DATETIME NOT NULL, task TEXT, mode TEXT, reasoning_tokens INTEGER, reasoning_keywords TEXT, context_pct REAL, blob BLOB);
CREATE INDEX idx_rlog_arch_session ON reasoning_log_archive(session_id, turn_id);
CREATE INDEX idx_rlog_arch_ts ON reasoning_log_archive(ts);

CREATE TABLE IF NOT EXISTS trace_spans (span_id TEXT PRIMARY KEY, parent_span_id TEXT, trace_id TEXT NOT NULL, session_id TEXT, turn_id INTEGER, component TEXT NOT NULL, operation TEXT NOT NULL, started_at DATETIME NOT NULL, completed_at DATETIME, duration_ms INTEGER GENERATED ALWAYS AS (CASE WHEN completed_at IS NOT NULL THEN CAST((julianday(completed_at) - julianday(started_at)) * 86400000 AS INTEGER) ELSE NULL END) STORED, status TEXT DEFAULT 'ok' CHECK(status IN ('ok','error','timeout','cancelled')), error_type TEXT, error_message TEXT, input_data TEXT, output_data TEXT, stack_trace TEXT, input_preview TEXT, output_preview TEXT, FOREIGN KEY (parent_span_id) REFERENCES trace_spans(span_id) ON DELETE SET NULL);
CREATE INDEX idx_spans_trace ON trace_spans(trace_id);
CREATE INDEX idx_spans_session ON trace_spans(session_id, turn_id);
CREATE INDEX idx_spans_component_time ON trace_spans(component, started_at);
CREATE INDEX idx_spans_status ON trace_spans(status, started_at);
CREATE INDEX idx_spans_parent ON trace_spans(parent_span_id);

CREATE TABLE IF NOT EXISTS prompt_versions (prompt_id TEXT PRIMARY KEY, component TEXT NOT NULL CHECK(component IN ('CORE','CONTEXT','ATOMIZER','REFLEXOR','ARCHIVIST_BUILD','MCP_GUARD')), version INTEGER NOT NULL, hash TEXT UNIQUE NOT NULL, content TEXT NOT NULL, created_at DATETIME DEFAULT CURRENT_TIMESTAMP, created_by TEXT DEFAULT 'system', change_description TEXT, UNIQUE(component, version));
CREATE INDEX idx_pver_component ON prompt_versions(component, version);

CREATE TABLE IF NOT EXISTS prompt_snapshots (snapshot_id TEXT PRIMARY KEY, trace_id TEXT NOT NULL UNIQUE, session_id TEXT NOT NULL, turn_id INTEGER NOT NULL, core_prompt_id TEXT, context_prompt_id TEXT, brief_hash TEXT, archivarius_version TEXT, atomizer_version TEXT, reflexor_version TEXT, core_tokens INTEGER DEFAULT 0, context_tokens INTEGER DEFAULT 0, brief_tokens INTEGER DEFAULT 0, trail_tokens INTEGER DEFAULT 0, plan_tokens INTEGER DEFAULT 0, total_tokens INTEGER GENERATED ALWAYS AS (core_tokens + context_tokens + brief_tokens + trail_tokens + plan_tokens) STORED, full_prompt_hash TEXT, full_prompt_preview TEXT, built_at DATETIME DEFAULT CURRENT_TIMESTAMP, build_duration_ms INTEGER, FOREIGN KEY (core_prompt_id) REFERENCES prompt_versions(prompt_id), FOREIGN KEY (context_prompt_id) REFERENCES prompt_versions(prompt_id));
CREATE INDEX idx_psnap_trace ON prompt_snapshots(trace_id);
CREATE INDEX idx_psnap_versions ON prompt_snapshots(core_prompt_id, context_prompt_id);

CREATE TABLE IF NOT EXISTS atom_usage (usage_id INTEGER PRIMARY KEY, atom_id TEXT NOT NULL, trace_id TEXT NOT NULL, turn_id INTEGER NOT NULL, used_in TEXT NOT NULL CHECK(used_in IN ('BRIEF','CONTEXT','PLAN')), position_in_prompt INTEGER, prompt_tokens INTEGER, invoked_tool_after TEXT, invoked_tool_result TEXT, archivarius_span_id TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP, FOREIGN KEY (atom_id) REFERENCES knowledge_atoms(atom_id) ON DELETE CASCADE, FOREIGN KEY (archivarius_span_id) REFERENCES trace_spans(span_id));
CREATE INDEX idx_ausage_atom ON atom_usage(atom_id, created_at DESC);
CREATE INDEX idx_ausage_trace ON atom_usage(trace_id, turn_id);
CREATE INDEX idx_ausage_invoked ON atom_usage(invoked_tool_after, invoked_tool_result);
CREATE INDEX idx_ausage_arch ON atom_usage(archivarius_span_id);

CREATE TABLE IF NOT EXISTS daily_metrics (metric_date DATE PRIMARY KEY, sessions_started INTEGER DEFAULT 0, sessions_completed INTEGER DEFAULT 0, avg_session_duration_ms INTEGER, total_llm_requests INTEGER DEFAULT 0, total_tokens INTEGER DEFAULT 0, total_cost_usd REAL DEFAULT 0.0, avg_tokens_per_request INTEGER, error_rate REAL, tool_calls_by_name TEXT, atoms_created INTEGER DEFAULT 0, top_atoms_used TEXT, prompt_version_distribution TEXT, p50_latency_ms INTEGER, p95_latency_ms INTEGER, p99_latency_ms INTEGER);
`
