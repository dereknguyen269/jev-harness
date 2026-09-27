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
    source      TEXT NOT NULL DEFAULT 'db'
);

CREATE TABLE IF NOT EXISTS groups (
    name        TEXT PRIMARY KEY,
    description TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS categories (
    name        TEXT PRIMARY KEY,
    description TEXT NOT NULL DEFAULT ''
);
