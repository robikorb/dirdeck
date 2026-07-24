package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// Open opens (or creates) the application SQLite database and runs migrations.
func Open(dataDir string) (*sql.DB, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "app.db")
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)", path)
	database, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	database.SetMaxOpenConns(1)
	if err := migrate(database); err != nil {
		_ = database.Close()
		return nil, err
	}
	return database, nil
}

func migrate(database *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		);`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		);`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			csrf_token TEXT NOT NULL,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			expires_at TEXT NOT NULL,
			revoked_at TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);`,
		`CREATE TABLE IF NOT EXISTS preferences (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL DEFAULT (datetime('now'))
		);`,
		// Phase 1: durable transfer jobs
		`CREATE TABLE IF NOT EXISTS transfer_jobs (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			status TEXT NOT NULL,
			source_volume_id TEXT NOT NULL,
			source_path TEXT NOT NULL,
			source_paths TEXT NOT NULL DEFAULT '[]',
			dest_volume_id TEXT NOT NULL,
			dest_dir TEXT NOT NULL DEFAULT '',
			dest_name TEXT NOT NULL DEFAULT '',
			conflict_policy TEXT NOT NULL DEFAULT 'prompt',
			apply_to_all INTEGER NOT NULL DEFAULT 0,
			bytes_total INTEGER NOT NULL DEFAULT 0,
			bytes_done INTEGER NOT NULL DEFAULT 0,
			files_total INTEGER NOT NULL DEFAULT 0,
			files_done INTEGER NOT NULL DEFAULT 0,
			bytes_per_second REAL NOT NULL DEFAULT 0,
			current_path TEXT NOT NULL DEFAULT '',
			staging_name TEXT NOT NULL DEFAULT '',
			conflict_name TEXT,
			error_message TEXT,
			free_space_known INTEGER NOT NULL DEFAULT 0,
			free_space_bytes INTEGER,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			started_at TEXT,
			finished_at TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_transfer_jobs_status ON transfer_jobs(status);`,
		`CREATE INDEX IF NOT EXISTS idx_transfer_jobs_updated ON transfer_jobs(updated_at);`,
		// Phase 3: favorites, recent locations (pane state uses preferences)
		`CREATE TABLE IF NOT EXISTS favorites (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			volume_id TEXT NOT NULL,
			path TEXT NOT NULL DEFAULT '',
			label TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			UNIQUE(volume_id, path)
		);`,
		`CREATE TABLE IF NOT EXISTS recent_locations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			volume_id TEXT NOT NULL,
			path TEXT NOT NULL DEFAULT '',
			visited_at TEXT NOT NULL,
			UNIQUE(volume_id, path)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_recent_visited ON recent_locations(visited_at);`,
	}
	for _, s := range stmts {
		if _, err := database.Exec(s); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	if err := ensureColumn(database, "transfer_jobs", "source_paths", `TEXT NOT NULL DEFAULT '[]'`); err != nil {
		return err
	}
	for _, v := range []int{1, 2, 3, 4} {
		if _, err := database.Exec(`INSERT OR IGNORE INTO schema_migrations(version) VALUES (?)`, v); err != nil {
			return err
		}
	}
	return nil
}

func ensureColumn(database *sql.DB, table, column, definition string) error {
	rows, err := database.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		if strings.EqualFold(name, column) {
			found = true
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found {
		return nil
	}
	_, err = database.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + column + ` ` + definition)
	return err
}
