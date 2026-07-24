package db_test

import (
	"database/sql"
	"testing"

	"github.com/liquid-glass-file-manager/backend/internal/db"
)

func TestSourcePathsMigrationPreservesExistingDatabase(t *testing.T) {
	dataDir := t.TempDir()
	database, err := db.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`ALTER TABLE transfer_jobs DROP COLUMN source_paths`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	database, err = db.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if !hasColumn(t, database, "transfer_jobs", "source_paths") {
		t.Fatal("source_paths migration was not applied")
	}
	var version int
	if err := database.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version < 4 {
		t.Fatalf("migration version = %d", version)
	}
}

func hasColumn(t *testing.T, database *sql.DB, table, want string) bool {
	t.Helper()
	rows, err := database.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, kind string
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == want {
			return true
		}
	}
	return false
}
