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
CREATE TABLE IF NOT EXISTS dan_courses (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL,
    chart_ids TEXT NOT NULL,
    song_levels TEXT NOT NULL DEFAULT '[]',
    is_force_gameover INTEGER NOT NULL DEFAULT 0 CHECK (is_force_gameover IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS dan_admin_sessions (
    token_hash BLOB PRIMARY KEY,
    csrf_hash BLOB NOT NULL,
    expires_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS dan_admin_sessions_expiry ON dan_admin_sessions(expires_at);
`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize SQLite schema: %w", err)
	}
	if err := ensureDanCourseSongLevelsColumn(db); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable SQLite foreign keys: %w", err)
	}
	return db, nil
}

func ensureDanCourseSongLevelsColumn(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(dan_courses)")
	if err != nil {
		return fmt.Errorf("inspect Dan course schema: %w", err)
	}
	hasColumn := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return fmt.Errorf("read Dan course schema: %w", err)
		}
		if name == "song_levels" {
			hasColumn = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate Dan course schema: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close Dan course schema query: %w", err)
	}
	if hasColumn {
		return nil
	}
	if _, err := db.Exec("ALTER TABLE dan_courses ADD COLUMN song_levels TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return fmt.Errorf("add Dan course song levels column: %w", err)
	}
	return nil
}
