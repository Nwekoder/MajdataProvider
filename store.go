package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

func openStore(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect to SQLite database: %w", err)
	}

	const schema = `
CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    username TEXT NOT NULL COLLATE NOCASE UNIQUE,
    nickname TEXT NOT NULL DEFAULT '',
    introduction TEXT,
    password_salt BLOB NOT NULL,
    password_hash BLOB NOT NULL,
    created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
    token_hash BLOB PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_user_id ON sessions(user_id);
CREATE TABLE IF NOT EXISTS scores (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    chart_id TEXT NOT NULL,
    chart_level INTEGER NOT NULL CHECK (chart_level BETWEEN 0 AND 6),
    acc_dx REAL NOT NULL,
    acc_classic REAL NOT NULL,
    dx_score INTEGER NOT NULL CHECK (dx_score >= 0),
    combo_state INTEGER NOT NULL CHECK (combo_state BETWEEN 0 AND 4),
    hash TEXT NOT NULL,
    played_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS scores_user_chart_level ON scores(user_id, chart_id, chart_level);
CREATE INDEX IF NOT EXISTS scores_chart_level ON scores(chart_id, chart_level);
CREATE TABLE IF NOT EXISTS game_settings (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    settings_json TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS playlists (
    id TEXT PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    visibility INTEGER NOT NULL CHECK (visibility IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS playlists_owner_activity ON playlists(owner_id, updated_at DESC);
CREATE TABLE IF NOT EXISTS playlist_items (
    playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
    chart_id TEXT NOT NULL,
    position INTEGER NOT NULL,
    PRIMARY KEY (playlist_id, chart_id),
    UNIQUE (playlist_id, position)
);
CREATE TABLE IF NOT EXISTS favorites (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    collection_id TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (user_id, collection_id)
);
`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize SQLite schema: %w", err)
	}
	if err := ensureUserProfileColumns(db); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable SQLite foreign keys: %w", err)
	}
	return db, nil
}

func ensureUserProfileColumns(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(users)")
	if err != nil {
		return fmt.Errorf("inspect user schema: %w", err)
	}
	hasNickname, hasIntroduction := false, false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return fmt.Errorf("read user schema: %w", err)
		}
		switch name {
		case "nickname":
			hasNickname = true
		case "introduction":
			hasIntroduction = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate user schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close user schema query: %w", err)
	}
	if !hasNickname {
		if _, err := db.Exec("ALTER TABLE users ADD COLUMN nickname TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("add user nickname column: %w", err)
		}
	}
	if !hasIntroduction {
		if _, err := db.Exec("ALTER TABLE users ADD COLUMN introduction TEXT"); err != nil {
			return fmt.Errorf("add user introduction column: %w", err)
		}
	}
	return nil
}
