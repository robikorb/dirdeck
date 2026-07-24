package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	sessionCookieName = "lgfm_session"
	csrfHeaderName    = "X-CSRF-Token"
	argonTime         = 3
	argonMemory       = 64 * 1024
	argonThreads      = 4
	argonKeyLen       = 32
	argonSaltLen      = 16
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrUnauthorized       = errors.New("unauthorized")
	ErrCSRF               = errors.New("csrf validation failed")
	ErrRateLimited        = errors.New("too many login attempts")
)

// Service manages authentication, sessions, and CSRF.
type Service struct {
	db              *sql.DB
	secureCookie    bool
	sessionTTL      time.Duration
	rateLimitMax    int
	rateLimitWindow time.Duration

	mu       sync.Mutex
	attempts map[string][]time.Time
}

// Session holds authenticated session data.
type Session struct {
	ID        string
	UserID    int64
	Username  string
	CSRFToken string
	ExpiresAt time.Time
}

// New creates an auth service.
func New(database *sql.DB, secureCookie bool, sessionTTLHours, rateLimitMax, rateLimitSec int) *Service {
	return &Service{
		db:              database,
		secureCookie:    secureCookie,
		sessionTTL:      time.Duration(sessionTTLHours) * time.Hour,
		rateLimitMax:    rateLimitMax,
		rateLimitWindow: time.Duration(rateLimitSec) * time.Second,
		attempts:        make(map[string][]time.Time),
	}
}

// BootstrapAdmin ensures a single admin user exists from secret files.
func (s *Service) BootstrapAdmin(username, password string) error {
	if username == "" || password == "" {
		return fmt.Errorf("admin username and password are required")
	}
	var existingUsername, existingHash string
	err := s.db.QueryRow(
		`SELECT username, password_hash FROM users WHERE id = 1`,
	).Scan(&existingUsername, &existingHash)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		hash, hashErr := hashPassword(password)
		if hashErr != nil {
			return hashErr
		}
		_, err = s.db.Exec(
			`INSERT INTO users(id, username, password_hash) VALUES (1, ?, ?)`,
			username, hash,
		)
		return err
	case err != nil:
		return err
	}
	if existingUsername == username && verifyPassword(existingHash, password) {
		return nil
	}

	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(
		`UPDATE users SET username = ?, password_hash = ?, updated_at = datetime('now') WHERE id = 1`,
		username, hash,
	); err != nil {
		return err
	}
	if _, err = tx.Exec(`UPDATE sessions SET revoked_at = datetime('now') WHERE revoked_at IS NULL`); err != nil {
		return err
	}
	return tx.Commit()
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	// format: argon2id$v=19$m=65536,t=3,p=4$salt$hash
	return fmt.Sprintf("argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" {
		return false
	}
	var memory, timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func sessionTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// PruneSessions removes expired and revoked credentials from durable state.
func (s *Service) PruneSessions() error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(
		`DELETE FROM sessions WHERE expires_at <= ? OR revoked_at IS NOT NULL`,
		now,
	)
	return err
}

// Login authenticates and creates a new session (rotating any prior cookie).
func (s *Service) Login(w http.ResponseWriter, r *http.Request, username, password string) (*Session, error) {
	ip := clientIP(r)
	if s.isRateLimited(ip) {
		return nil, ErrRateLimited
	}
	var (
		id   int64
		hash string
		user string
	)
	err := s.db.QueryRow(`SELECT id, username, password_hash FROM users WHERE id = 1`).Scan(&id, &user, &hash)
	passwordOK := verifyPassword(hash, password)
	usernameOK := subtle.ConstantTimeCompare([]byte(user), []byte(username)) == 1
	if err != nil || !usernameOK || !passwordOK {
		s.recordFailure(ip)
		return nil, ErrInvalidCredentials
	}
	s.clearFailures(ip)

	sessionID, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	csrf, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	expires := time.Now().UTC().Add(s.sessionTTL)
	_, err = s.db.Exec(
		`INSERT INTO sessions(id, user_id, csrf_token, expires_at) VALUES (?, ?, ?, ?)`,
		sessionTokenHash(sessionID), id, csrf, expires.Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	// Revoke any previous session from cookie (rotation).
	if old, err := r.Cookie(sessionCookieName); err == nil && old.Value != "" {
		_, _ = s.db.Exec(`UPDATE sessions SET revoked_at = datetime('now') WHERE id = ?`, sessionTokenHash(old.Value))
	}
	s.setSessionCookie(w, sessionID, expires)
	return &Session{ID: sessionID, UserID: id, Username: user, CSRFToken: csrf, ExpiresAt: expires}, nil
}

// Logout revokes the current session.
func (s *Service) Logout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		_, _ = s.db.Exec(`UPDATE sessions SET revoked_at = datetime('now') WHERE id = ?`, sessionTokenHash(c.Value))
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.secureCookie,
	})
	return nil
}

// SessionFromRequest loads a valid session from the cookie.
func (s *Service) SessionFromRequest(r *http.Request) (*Session, error) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return nil, ErrUnauthorized
	}
	var sess Session
	var expires string
	err = s.db.QueryRow(`
		SELECT s.id, s.user_id, u.username, s.csrf_token, s.expires_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.id = ? AND s.revoked_at IS NULL
	`, sessionTokenHash(c.Value)).Scan(&sess.ID, &sess.UserID, &sess.Username, &sess.CSRFToken, &expires)
	if err != nil {
		return nil, ErrUnauthorized
	}
	exp, err := time.Parse(time.RFC3339, expires)
	if err != nil || time.Now().UTC().After(exp) {
		return nil, ErrUnauthorized
	}
	sess.ExpiresAt = exp
	return &sess, nil
}

// RequireCSRF validates Origin/Referer and CSRF token for state-changing requests.
func (s *Service) RequireCSRF(r *http.Request, sess *Session) error {
	if !sameOrigin(r) {
		return ErrCSRF
	}
	token := r.Header.Get(csrfHeaderName)
	if token == "" {
		return ErrCSRF
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(sess.CSRFToken)) != 1 {
		return ErrCSRF
	}
	return nil
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin != "" {
		return originMatchesHost(origin, r.Host)
	}
	referer := r.Header.Get("Referer")
	if referer == "" {
		// Same-origin fetch from same host without Origin is allowed only for non-browser tools
		// when Host matches; browsers send Origin/Referer for cross-site. Require one of them
		// for CSRF defense-in-depth when cookie auth is used.
		return false
	}
	return originMatchesHost(referer, r.Host)
}

func originMatchesHost(originOrURL, host string) bool {
	// Accept http(s)://host or http(s)://host/...
	o := strings.TrimSpace(originOrURL)
	if i := strings.Index(o, "://"); i >= 0 {
		o = o[i+3:]
	}
	if i := strings.IndexAny(o, "/?#"); i >= 0 {
		o = o[:i]
	}
	return strings.EqualFold(o, host)
}

func (s *Service) setSessionCookie(w http.ResponseWriter, id string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    id,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   s.secureCookie,
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Service) isRateLimited(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(ip)
	return len(s.attempts[ip]) >= s.rateLimitMax
}

func (s *Service) recordFailure(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts[ip] = append(s.attempts[ip], time.Now())
	s.pruneLocked(ip)
}

func (s *Service) clearFailures(ip string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.attempts, ip)
}

func (s *Service) pruneLocked(ip string) {
	cutoff := time.Now().Add(-s.rateLimitWindow)
	arr := s.attempts[ip]
	n := 0
	for _, t := range arr {
		if t.After(cutoff) {
			arr[n] = t
			n++
		}
	}
	if n == 0 {
		delete(s.attempts, ip)
	} else {
		s.attempts[ip] = arr[:n]
	}
}

type ctxKey int

const sessionKey ctxKey = 1

// WithSession stores the session on the context.
func WithSession(ctx context.Context, sess *Session) context.Context {
	return context.WithValue(ctx, sessionKey, sess)
}

// SessionFromContext retrieves the session.
func SessionFromContext(ctx context.Context) (*Session, bool) {
	sess, ok := ctx.Value(sessionKey).(*Session)
	return sess, ok
}
