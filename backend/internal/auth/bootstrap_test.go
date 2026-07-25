package auth

import (
	"database/sql"
	"testing"

	appdb "github.com/robikorb/dirdeck/backend/internal/db"
)

func TestBootstrapAdminKeepsHashOnRestartAndRevokesOnRotation(t *testing.T) {
	database, err := appdb.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	service := New(database, false, 12, 10, 60)
	if err := service.BootstrapAdmin("admin", "first-long-password"); err != nil {
		t.Fatal(err)
	}
	firstHash := readAdminHash(t, database)

	if err := service.BootstrapAdmin("admin", "first-long-password"); err != nil {
		t.Fatal(err)
	}
	if got := readAdminHash(t, database); got != firstHash {
		t.Fatal("unchanged bootstrap credentials should not rehash on restart")
	}

	if _, err := database.Exec(`
		INSERT INTO sessions(id, user_id, csrf_token, created_at, expires_at)
		VALUES ('session-1', 1, 'csrf', datetime('now'), datetime('now', '+1 hour'))
	`); err != nil {
		t.Fatal(err)
	}
	if err := service.BootstrapAdmin("admin", "second-long-password"); err != nil {
		t.Fatal(err)
	}
	if got := readAdminHash(t, database); got == firstHash {
		t.Fatal("rotated password should update the stored hash")
	}
	var revoked sql.NullString
	if err := database.QueryRow(`SELECT revoked_at FROM sessions WHERE id = 'session-1'`).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	if !revoked.Valid {
		t.Fatal("password rotation should revoke existing sessions")
	}
}

func readAdminHash(t *testing.T, database *sql.DB) string {
	t.Helper()
	var hash string
	if err := database.QueryRow(`SELECT password_hash FROM users WHERE id = 1`).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	return hash
}
