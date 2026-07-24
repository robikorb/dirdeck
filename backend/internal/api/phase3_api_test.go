package api_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
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
	"github.com/liquid-glass-file-manager/backend/internal/prefs"
	"github.com/liquid-glass-file-manager/backend/internal/preview"
	"github.com/liquid-glass-file-manager/backend/internal/volumes"
)

func TestPhase3ThumbnailsFavoritesAndPanes(t *testing.T) {
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })

	media := filepath.Join(root, "media")
	_ = os.MkdirAll(media, 0o755)
	writeTestPNG(t, filepath.Join(media, "pic.png"), 40, 30)

	cfgPath := filepath.Join(root, "volumes.yaml")
	_ = os.WriteFile(cfgPath, []byte(`
volumes:
  - id: media
    name: Media
    path: `+media+`
    readOnly: false
    showHiddenFiles: false
    thumbnails: true
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
	fsSvc := appfs.New(reg)
	srv := &api.Server{
		Auth:    authSvc,
		FS:      fsSvc,
		Volumes: reg,
		Preview: preview.New(fsSvc, preview.Defaults()),
		Prefs:   prefs.New(database),
		Ready:   true,
	}
	h := srv.Handler()

	rr := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "secret-pass"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("login %d", rr.Code)
	}
	var loginResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &loginResp)
	csrf, _ := loginResp["csrfToken"].(string)
	cookie := rr.Result().Cookies()

	authed := func(method, url string, body io.Reader) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, url, body)
		for _, c := range cookie {
			req.AddCookie(c)
		}
		if method != http.MethodGet && method != http.MethodHead {
			req.Header.Set("X-CSRF-Token", csrf)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", "http://example.com")
			req.Host = "example.com"
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// Volumes include available=true
	rr = authed(http.MethodGet, "/api/volumes", nil)
	if rr.Code != 200 {
		t.Fatalf("volumes %d", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte(`"available":true`)) {
		t.Fatalf("expected available: %s", rr.Body.String())
	}

	// Thumbnail
	rr = authed(http.MethodGet, "/api/volumes/media/thumbnail?path=pic.png", nil)
	if rr.Code != 200 {
		t.Fatalf("thumb %d %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("ct %s", ct)
	}

	// Preview with range
	rr = authed(http.MethodGet, "/api/volumes/media/preview?path=pic.png", nil)
	if rr.Code != 200 {
		t.Fatalf("preview %d", rr.Code)
	}
	full := rr.Body.Bytes()
	req = httptest.NewRequest(http.MethodGet, "/api/volumes/media/preview?path=pic.png", nil)
	for _, c := range cookie {
		req.AddCookie(c)
	}
	req.Header.Set("Range", "bytes=0-9")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusPartialContent {
		t.Fatalf("range status %d", rr.Code)
	}
	if len(rr.Body.Bytes()) != 10 {
		t.Fatalf("range len %d", len(rr.Body.Bytes()))
	}
	if !bytes.Equal(rr.Body.Bytes(), full[:10]) {
		t.Fatal("range mismatch")
	}

	// Favorites
	body, _ = json.Marshal(map[string]string{"volumeId": "media", "path": "", "label": "Media root"})
	rr = authed(http.MethodPost, "/api/favorites", bytes.NewReader(body))
	if rr.Code != 201 {
		t.Fatalf("fav create %d %s", rr.Code, rr.Body.String())
	}
	rr = authed(http.MethodGet, "/api/favorites", nil)
	if rr.Code != 200 || !bytes.Contains(rr.Body.Bytes(), []byte("Media root")) {
		t.Fatalf("fav list %s", rr.Body.String())
	}

	// Recent + pane state
	body, _ = json.Marshal(map[string]string{"volumeId": "media", "path": ""})
	rr = authed(http.MethodPost, "/api/recent", bytes.NewReader(body))
	if rr.Code != 200 {
		t.Fatalf("recent %d", rr.Code)
	}
	pane := map[string]any{
		"left":          map[string]string{"volumeId": "media", "path": "", "view": "grid"},
		"right":         map[string]string{"volumeId": "media", "path": "", "view": "list"},
		"inspectorOpen": true,
		"activePane":    "left",
	}
	body, _ = json.Marshal(pane)
	rr = authed(http.MethodPut, "/api/preferences/panes", bytes.NewReader(body))
	if rr.Code != 200 {
		t.Fatalf("panes put %d %s", rr.Code, rr.Body.String())
	}
	rr = authed(http.MethodGet, "/api/preferences/panes", nil)
	if rr.Code != 200 || !bytes.Contains(rr.Body.Bytes(), []byte(`"view":"grid"`)) {
		t.Fatalf("panes get %s", rr.Body.String())
	}

	// Unavailable mount after remove
	_ = os.RemoveAll(media)
	rr = authed(http.MethodGet, "/api/volumes", nil)
	if rr.Code != 200 || !bytes.Contains(rr.Body.Bytes(), []byte(`"available":false`)) {
		t.Fatalf("unavailable volumes %s", rr.Body.String())
	}
	rr = authed(http.MethodGet, "/api/volumes/media/list?path=", nil)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("list unavailable status %d %s", rr.Code, rr.Body.String())
	}
}

func writeTestPNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 10, G: 20, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	_ = os.WriteFile(path, buf.Bytes(), 0o644)
}

func TestTextPreviewAPI(t *testing.T) {
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })

	media := filepath.Join(root, "media")
	_ = os.MkdirAll(media, 0o755)
	_ = os.WriteFile(filepath.Join(media, "data.json"), []byte(`{"z":1,"a":2}`), 0o644)
	_ = os.WriteFile(filepath.Join(media, "notes.md"), []byte("# Hi"), 0o644)
	_ = os.WriteFile(filepath.Join(media, "secret.bin.txt"), []byte("a\x00b"), 0o644)

	cfgPath := filepath.Join(root, "volumes.yaml")
	_ = os.WriteFile(cfgPath, []byte(`
volumes:
  - id: media
    name: Media
    path: `+media+`
    readOnly: true
    showHiddenFiles: false
    thumbnails: true
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
	fsSvc := appfs.New(reg)
	srv := &api.Server{
		Auth:    authSvc,
		FS:      fsSvc,
		Volumes: reg,
		Preview: preview.New(fsSvc, preview.Defaults()),
		Prefs:   prefs.New(database),
		Ready:   true,
	}
	h := srv.Handler()

	// Unauthenticated blocked
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/volumes/media/preview?path=data.json", nil)
	h.ServeHTTP(rr, req)
	if rr.Code == 200 {
		t.Fatal("expected auth required")
	}

	rr = httptest.NewRecorder()
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "secret-pass"})
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("login %d", rr.Code)
	}
	cookie := rr.Result().Cookies()

	authed := func(url string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, url, nil)
		for _, c := range cookie {
			req.AddCookie(c)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	rr = authed("/api/volumes/media/preview?path=data.json")
	if rr.Code != 200 {
		t.Fatalf("json preview %d %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("ct %s", ct)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte(`"kind":"json"`)) {
		t.Fatalf("body %s", rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("\n")) {
		t.Fatal("expected pretty-printed JSON text")
	}

	rr = authed("/api/volumes/media/preview?path=notes.md&kind=markdown")
	if rr.Code != 200 || !bytes.Contains(rr.Body.Bytes(), []byte(`"kind":"markdown"`)) {
		t.Fatalf("md %d %s", rr.Code, rr.Body.String())
	}

	rr = authed("/api/volumes/media/preview?path=../etc/passwd")
	if rr.Code == 200 {
		t.Fatal("expected path rejection")
	}

	rr = authed("/api/volumes/media/preview?path=secret.bin.txt")
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("binary status %d %s", rr.Code, rr.Body.String())
	}
}
