package auth

import (
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/robikorb/dirdeck/backend/internal/db"
)

func TestSessionTokenIsHashedAndPruned(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	service := New(database, false, 1, 5, 60)
	if err := service.BootstrapAdmin("admin", "secret-pass"); err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/auth/login", nil)
	session, err := service.Login(recorder, request, "admin", "secret-pass")
	if err != nil {
		t.Fatal(err)
	}

	var storedID string
	if err := database.QueryRow(`SELECT id FROM sessions`).Scan(&storedID); err != nil {
		t.Fatal(err)
	}
	if storedID == session.ID {
		t.Fatal("plaintext session token was stored")
	}
	if storedID != sessionTokenHash(session.ID) {
		t.Fatal("stored session digest does not match cookie token")
	}

	authenticated := httptest.NewRequest("GET", "/api/auth/session", nil)
	authenticated.AddCookie(recorder.Result().Cookies()[0])
	if _, err := service.SessionFromRequest(authenticated); err != nil {
		t.Fatalf("hashed session lookup failed: %v", err)
	}

	if _, err := database.Exec(
		`INSERT INTO sessions(id, user_id, csrf_token, expires_at) VALUES (?, 1, 'expired', ?)`,
		"expired-session",
		time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		`INSERT INTO sessions(id, user_id, csrf_token, expires_at, revoked_at) VALUES (?, 1, 'revoked', ?, datetime('now'))`,
		"revoked-session",
		time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
	); err != nil {
		t.Fatal(err)
	}
	if err := service.PruneSessions(); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"expired-session", "revoked-session"} {
		var count int
		err := database.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = ?`, id).Scan(&count)
		if err != nil && err != sql.ErrNoRows {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("session %q was not pruned", id)
		}
	}
}
