package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liquid-glass-file-manager/backend/internal/api"
	"github.com/liquid-glass-file-manager/backend/internal/auth"
	"github.com/liquid-glass-file-manager/backend/internal/db"
	appfs "github.com/liquid-glass-file-manager/backend/internal/fs"
	"github.com/liquid-glass-file-manager/backend/internal/volumes"
)

func TestAuthAndFilesystemAPI(t *testing.T) {
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })

	ro := filepath.Join(root, "ro")
	rw := filepath.Join(root, "rw")
	_ = os.MkdirAll(filepath.Join(ro, "d"), 0o755)
	_ = os.MkdirAll(rw, 0o755)
	_ = os.WriteFile(filepath.Join(ro, "a.txt"), []byte("abc"), 0o644)
	_ = os.WriteFile(filepath.Join(ro, "big.bin"), bytes.Repeat([]byte("x"), 4096), 0o644)

	cfgPath := filepath.Join(root, "volumes.yaml")
	_ = os.WriteFile(cfgPath, []byte(`
volumes:
  - id: fixture-ro
    name: RO
    path: `+ro+`
    readOnly: true
    showHiddenFiles: false
    thumbnails: false
  - id: fixture-rw
    name: RW
    path: `+rw+`
    readOnly: false
    showHiddenFiles: false
    thumbnails: false
`), 0o600)

	reg, err := volumes.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	authSvc := auth.New(database, false, 1, 5, 60)
	if err := authSvc.BootstrapAdmin("admin", "secret-pass"); err != nil {
		t.Fatal(err)
	}
	srv := &api.Server{
		Auth:    authSvc,
		FS:      appfs.New(reg),
		Volumes: reg,
		Ready:   true,
	}
	h := srv.Handler()

	// Health unauthenticated
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rr.Code != 200 {
		t.Fatalf("health: %d", rr.Code)
	}
	if got := rr.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") {
		t.Fatalf("missing restrictive content security policy: %q", got)
	}

	// Volumes require auth
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/volumes", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for volumes, got %d", rr.Code)
	}

	// Bad login
	rr = httptest.NewRecorder()
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "wrong"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("bad login: %d", rr.Code)
	}

	// Good login
	rr = httptest.NewRecorder()
	body, _ = json.Marshal(map[string]string{"username": "admin", "password": "secret-pass"})
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("login: %d %s", rr.Code, rr.Body.String())
	}
	var loginResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &loginResp)
	csrf, _ := loginResp["csrfToken"].(string)
	if csrf == "" {
		t.Fatal("missing csrf")
	}
	cookie := rr.Result().Cookies()
	if len(cookie) == 0 {
		t.Fatal("missing session cookie")
	}

	authed := func(method, url string, body io.Reader) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, url, body)
		for _, c := range cookie {
			req.AddCookie(c)
		}
		if method != http.MethodGet && method != http.MethodHead {
			req.Header.Set("X-CSRF-Token", csrf)
			req.Header.Set("Origin", "http://example.com")
			req.Host = "example.com"
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	rr = authed(http.MethodGet, "/api/volumes", nil)
	if rr.Code != 200 {
		t.Fatalf("volumes: %d", rr.Code)
	}
	if strings.Contains(rr.Body.String(), ro) {
		t.Fatal("response leaked container path")
	}

	rr = authed(http.MethodGet, "/api/volumes/fixture-ro/list?path=", nil)
	if rr.Code != 200 {
		t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}

	rr = authed(http.MethodGet, "/api/volumes/fixture-ro/list?path=..", nil)
	if rr.Code == 200 {
		t.Fatal("traversal should fail")
	}

	rr = authed(http.MethodGet, "/api/volumes/fixture-ro/download?path=a.txt", nil)
	if rr.Code != 200 || rr.Body.String() != "abc" {
		t.Fatalf("download: %d %q", rr.Code, rr.Body.String())
	}

	// Range request
	req = httptest.NewRequest(http.MethodGet, "/api/volumes/fixture-ro/download?path=big.bin", nil)
	for _, c := range cookie {
		req.AddCookie(c)
	}
	req.Header.Set("Range", "bytes=0-9")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusPartialContent {
		t.Fatalf("range: %d", rr.Code)
	}
	if rr.Body.Len() != 10 {
		t.Fatalf("range len %d", rr.Body.Len())
	}

	// Logout without CSRF fails
	req = httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	for _, c := range cookie {
		req.AddCookie(c)
	}
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("logout without csrf: %d", rr.Code)
	}

	rr = authed(http.MethodPost, "/api/auth/logout", nil)
	if rr.Code != 200 {
		t.Fatalf("logout: %d", rr.Code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })
	ro := filepath.Join(root, "ro")
	_ = os.MkdirAll(ro, 0o755)
	cfgPath := filepath.Join(root, "volumes.yaml")
	_ = os.WriteFile(cfgPath, []byte(`
volumes:
  - id: fixture-ro
    name: RO
    path: `+ro+`
    readOnly: true
    showHiddenFiles: false
    thumbnails: false
`), 0o600)
	reg, _ := volumes.Load(cfgPath)
	database, _ := db.Open(filepath.Join(root, "data"))
	t.Cleanup(func() { _ = database.Close() })
	authSvc := auth.New(database, false, 1, 3, 60)
	_ = authSvc.BootstrapAdmin("admin", "secret-pass")
	srv := &api.Server{Auth: authSvc, FS: appfs.New(reg), Volumes: reg, Ready: true}
	h := srv.Handler()

	for i := 0; i < 3; i++ {
		body, _ := json.Marshal(map[string]string{"username": "admin", "password": "wrong"})
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
		req.RemoteAddr = "10.0.0.1:1234"
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i, rr.Code)
		}
	}
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "wrong"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.RemoteAddr = "10.0.0.1:1234"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected rate limit, got %d", rr.Code)
	}
}
