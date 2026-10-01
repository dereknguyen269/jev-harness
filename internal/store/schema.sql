CREATE TABLE IF NOT EXISTS users (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    email      TEXT NOT NULL DEFAULT '',
    role       TEXT NOT NULL DEFAULT 'viewer',
    api_key    TEXT NOT NULL DEFAULT '',
    active     INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS rules (
    id          TEXT PRIMARY KEY,
    tool        TEXT NOT NULL DEFAULT '',
    pattern     TEXT NOT NULL DEFAULT '',
    action      TEXT NOT NULL DEFAULT 'block',
    priority    INTEGER NOT NULL DEFAULT 0,
    group_name  TEXT NOT NULL DEFAULT '',
    category    TEXT NOT NULL DEFAULT '',
    business    TEXT NOT NULL DEFAULT '',
    task        TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    source      TEXT NOT NULL DEFAULT 'db',
    approval_timeout INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS groups (
    name        TEXT PRIMARY KEY,
    description TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS categories (
    name        TEXT PRIMARY KEY,
    description TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS approvals (
    id         TEXT PRIMARY KEY,
    request_id TEXT NOT NULL DEFAULT '',
    tool       TEXT NOT NULL DEFAULT '',
    arguments  TEXT NOT NULL DEFAULT '{}',
    risk       REAL NOT NULL DEFAULT 0,
    reason     TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'pending',
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_approvals_status ON approvals(status);
CREATE INDEX IF NOT EXISTS idx_approvals_created ON approvals(created_at);

CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS jev_calls (
    id           INTEGER PRIMARY KEY,
    timestamp    TEXT NOT NULL,
    model        TEXT NOT NULL DEFAULT '',
    endpoint     TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT '',
    http_status  INTEGER NOT NULL DEFAULT 0,
    latency_ms   INTEGER NOT NULL DEFAULT 0,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    error        TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_jev_calls_id ON jev_calls(id DESC);
