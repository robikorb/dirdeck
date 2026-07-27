package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	urlpkg "net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robikorb/dirdeck/backend/internal/api"
	"github.com/robikorb/dirdeck/backend/internal/auth"
	"github.com/robikorb/dirdeck/backend/internal/db"
	appfs "github.com/robikorb/dirdeck/backend/internal/fs"
	"github.com/robikorb/dirdeck/backend/internal/volumes"
)

type uploadFixture struct {
	handler http.Handler
	server  *api.Server
	ro, rw  string
	cookies []*http.Cookie
	csrf    string
}

func newUploadFixture(t *testing.T) *uploadFixture {
	t.Helper()
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })

	ro := filepath.Join(root, "ro")
	rw := filepath.Join(root, "rw")
	for _, d := range []string{ro, rw, filepath.Join(rw, "sub")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := filepath.Join(root, "volumes.yaml")
	if err := os.WriteFile(cfg, []byte(`
volumes:
  - id: ro
    name: RO
    path: `+ro+`
    readOnly: true
    showHiddenFiles: false
    thumbnails: false
  - id: rw
    name: RW
    path: `+rw+`
    readOnly: false
    showHiddenFiles: false
    thumbnails: false
`), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := volumes.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	authSvc := auth.New(database, false, 1, 50, 60)
	if err := authSvc.BootstrapAdmin("admin", "secret-pass"); err != nil {
		t.Fatal(err)
	}
	srv := &api.Server{Auth: authSvc, FS: appfs.New(reg), Volumes: reg, Ready: true}
	h := srv.Handler()

	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "secret-pass"})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("login: %d", rr.Code)
	}
	var login map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &login)
	csrf, _ := login["csrfToken"].(string)

	return &uploadFixture{handler: h, server: srv, ro: ro, rw: rw, cookies: rr.Result().Cookies(), csrf: csrf}
}

func (f *uploadFixture) upload(t *testing.T, volume, dir, name, conflict string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	url := fmt.Sprintf("/api/volumes/%s/upload?path=%s&name=%s", volume, dir, name)
	if conflict != "" {
		url += "&conflict=" + conflict
	}
	req := httptest.NewRequest(http.MethodPost, url, body)
	for _, c := range f.cookies {
		req.AddCookie(c)
	}
	req.Header.Set("X-CSRF-Token", f.csrf)
	req.Header.Set("Origin", "http://example.com")
	req.Host = "example.com"
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	return rr
}

func (f *uploadFixture) uploadInto(t *testing.T, volume, dir, subDir, name, conflict string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	url := fmt.Sprintf("/api/volumes/%s/upload?path=%s&dir=%s&name=%s",
		volume, dir, urlpkg.QueryEscape(subDir), name)
	if conflict != "" {
		url += "&conflict=" + conflict
	}
	req := httptest.NewRequest(http.MethodPost, url, body)
	for _, c := range f.cookies {
		req.AddCookie(c)
	}
	req.Header.Set("X-CSRF-Token", f.csrf)
	req.Header.Set("Origin", "http://example.com")
	req.Host = "example.com"
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	return rr
}

func TestUploadWritesFileAtomically(t *testing.T) {
	f := newUploadFixture(t)
	rr := f.upload(t, "rw", "sub", "notes.txt", "", strings.NewReader("hello upload"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(f.rw, "sub", "notes.txt"))
	if err != nil || string(got) != "hello upload" {
		t.Fatalf("uploaded content: err=%v got=%q", err, got)
	}
	// No staging file may survive a successful upload.
	entries, _ := os.ReadDir(filepath.Join(f.rw, "sub"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), appfs.UploadStagingPrefix) {
			t.Fatalf("staging file left behind: %s", e.Name())
		}
	}
}

func TestUploadAcceptsZeroByteFile(t *testing.T) {
	f := newUploadFixture(t)
	rr := f.upload(t, "rw", "", "empty.bin", "", strings.NewReader(""))
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	info, err := os.Stat(filepath.Join(f.rw, "empty.bin"))
	if err != nil || info.Size() != 0 {
		t.Fatalf("zero-byte upload: err=%v", err)
	}
}

func TestUploadRejectsReadOnlyVolume(t *testing.T) {
	f := newUploadFixture(t)
	rr := f.upload(t, "ro", "", "nope.txt", "", strings.NewReader("x"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d: %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(f.ro, "nope.txt")); !os.IsNotExist(err) {
		t.Fatal("read-only volume was written")
	}
}

func TestUploadRejectsHostileNames(t *testing.T) {
	f := newUploadFixture(t)
	for _, name := range []string{
		"..%2Fescape.txt",
		"..",
		".",
		"sub%2Fnested.txt",
		"%2Fabsolute.txt",
		".dirdeck-upload-abc",
	} {
		rr := f.upload(t, "rw", "", name, "", strings.NewReader("x"))
		if rr.Code < 400 {
			t.Fatalf("name %q was accepted with %d", name, rr.Code)
		}
	}
	// Nothing may have escaped the volume root.
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.rw), "escape.txt")); !os.IsNotExist(err) {
		t.Fatal("upload escaped the volume root")
	}
}

func TestUploadConflictPolicies(t *testing.T) {
	f := newUploadFixture(t)
	existing := filepath.Join(f.rw, "dup.txt")
	if err := os.WriteFile(existing, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Default refuses and reports the conflict without writing.
	rr := f.upload(t, "rw", "", "dup.txt", "", strings.NewReader("new"))
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rr.Code)
	}
	if b, _ := os.ReadFile(existing); string(b) != "original" {
		t.Fatal("conflicting upload overwrote the original")
	}

	// Skip leaves the original untouched and reports success.
	rr = f.upload(t, "rw", "", "dup.txt", "skip", strings.NewReader("new"))
	if rr.Code != http.StatusOK {
		t.Fatalf("skip: expected 200, got %d", rr.Code)
	}
	if b, _ := os.ReadFile(existing); string(b) != "original" {
		t.Fatal("skip overwrote the original")
	}

	// Rename keeps both.
	rr = f.upload(t, "rw", "", "dup.txt", "rename", strings.NewReader("renamed"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("rename: expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Name != "dup (1).txt" {
		t.Fatalf("unexpected renamed target: %q", resp.Name)
	}
	if b, _ := os.ReadFile(existing); string(b) != "original" {
		t.Fatal("rename touched the original")
	}

	// Replace overwrites deliberately.
	rr = f.upload(t, "rw", "", "dup.txt", "replace", strings.NewReader("replaced"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("replace: expected 201, got %d", rr.Code)
	}
	if b, _ := os.ReadFile(existing); string(b) != "replaced" {
		t.Fatalf("replace did not overwrite: %q", b)
	}
}

// A body shorter than Content-Length means the client vanished mid-upload.
// Partial data must never appear under the final name.
func TestUploadTruncatedBodyLeavesNoFile(t *testing.T) {
	f := newUploadFixture(t)
	req := httptest.NewRequest(http.MethodPost,
		"/api/volumes/rw/upload?path=&name=partial.bin", strings.NewReader("only-part"))
	req.ContentLength = 5000
	for _, c := range f.cookies {
		req.AddCookie(c)
	}
	req.Header.Set("X-CSRF-Token", f.csrf)
	req.Header.Set("Origin", "http://example.com")
	req.Host = "example.com"
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)

	if rr.Code < 400 {
		t.Fatalf("truncated upload reported %d", rr.Code)
	}
	if _, err := os.Stat(filepath.Join(f.rw, "partial.bin")); !os.IsNotExist(err) {
		t.Fatal("partial upload appeared under the final name")
	}
	entries, _ := os.ReadDir(f.rw)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), appfs.UploadStagingPrefix) {
			t.Fatalf("staging file left behind after failure: %s", e.Name())
		}
	}
}

func TestUploadRequiresCSRF(t *testing.T) {
	f := newUploadFixture(t)
	req := httptest.NewRequest(http.MethodPost,
		"/api/volumes/rw/upload?path=&name=nocsrf.txt", strings.NewReader("x"))
	for _, c := range f.cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF, got %d", rr.Code)
	}
}

func TestUploadRequiresAuth(t *testing.T) {
	f := newUploadFixture(t)
	req := httptest.NewRequest(http.MethodPost,
		"/api/volumes/rw/upload?path=&name=noauth.txt", strings.NewReader("x"))
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without a session, got %d", rr.Code)
	}
}

func TestUploadSweepsAbandonedStagingFiles(t *testing.T) {
	f := newUploadFixture(t)
	orphan := filepath.Join(f.rw, appfs.UploadStagingPrefix+"deadbeef")
	if err := os.WriteFile(orphan, []byte("abandoned"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(orphan, old, old); err != nil {
		t.Fatal(err)
	}
	rr := f.upload(t, "rw", "", "fresh.txt", "", strings.NewReader("ok"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatal("abandoned staging file was not swept")
	}
}

func TestUploadDoesNotSweepFreshConcurrentStagingFile(t *testing.T) {
	f := newUploadFixture(t)
	active := filepath.Join(f.rw, appfs.UploadStagingPrefix+"active")
	if err := os.WriteFile(active, []byte("still uploading"), 0o644); err != nil {
		t.Fatal(err)
	}
	rr := f.upload(t, "rw", "", "fresh.txt", "", strings.NewReader("ok"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rr.Code)
	}
	if got, err := os.ReadFile(active); err != nil || string(got) != "still uploading" {
		t.Fatalf("fresh staging file was swept: err=%v got=%q", err, got)
	}
}

func TestUploadHonorsConfiguredLimit(t *testing.T) {
	f := newUploadFixture(t)
	f.server.MaxUploadBytes = 4
	rr := f.upload(t, "rw", "", "too-large.bin", "", strings.NewReader("12345"))
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d: %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(f.rw, "too-large.bin")); !os.IsNotExist(err) {
		t.Fatal("oversized upload reached its final name")
	}
}

func TestUploadCreatesFolderTree(t *testing.T) {
	f := newUploadFixture(t)
	rr := f.uploadInto(t, "rw", "", "Season 1/Extras", "clip.txt", "", strings.NewReader("nested"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(f.rw, "Season 1", "Extras", "clip.txt"))
	if err != nil || string(got) != "nested" {
		t.Fatalf("nested upload: err=%v got=%q", err, got)
	}

	// A second file reuses the existing chain rather than failing on it.
	rr = f.uploadInto(t, "rw", "", "Season 1/Extras", "clip2.txt", "", strings.NewReader("second"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("second nested upload: got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestUploadFolderTreeRejectsEscape(t *testing.T) {
	f := newUploadFixture(t)
	for _, dir := range []string{"..", "../outside", "/abs", "ok/../../escape"} {
		rr := f.uploadInto(t, "rw", "", dir, "x.txt", "", strings.NewReader("x"))
		if rr.Code < 400 {
			t.Fatalf("dir %q accepted with %d", dir, rr.Code)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.rw), "escape")); !os.IsNotExist(err) {
		t.Fatal("folder upload escaped the volume root")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.rw), "x.txt")); !os.IsNotExist(err) {
		t.Fatal("folder upload wrote outside the volume root")
	}
}

func TestUploadFolderTreeRejectsReadOnlyVolume(t *testing.T) {
	f := newUploadFixture(t)
	rr := f.uploadInto(t, "ro", "", "New Folder", "x.txt", "", strings.NewReader("x"))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
	if _, err := os.Stat(filepath.Join(f.ro, "New Folder")); !os.IsNotExist(err) {
		t.Fatal("read-only volume got a new directory")
	}
}

func (f *uploadFixture) newFolder(t *testing.T, volume, dir, name string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"path": dir, "name": name})
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/volumes/%s/folder", volume), bytes.NewReader(body))
	for _, c := range f.cookies {
		req.AddCookie(c)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", f.csrf)
	req.Header.Set("Origin", "http://example.com")
	req.Host = "example.com"
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	return rr
}

func TestCreateFolder(t *testing.T) {
	f := newUploadFixture(t)
	rr := f.newFolder(t, "rw", "sub", "New Folder")
	if rr.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rr.Code, rr.Body.String())
	}
	info, err := os.Stat(filepath.Join(f.rw, "sub", "New Folder"))
	if err != nil || !info.IsDir() {
		t.Fatalf("directory not created: err=%v", err)
	}
}

// fs.Mkdir returns success for an existing directory because recursive copy
// reuses the chain. A user action must not silently do nothing instead.
func TestCreateFolderRejectsExistingName(t *testing.T) {
	f := newUploadFixture(t)
	if rr := f.newFolder(t, "rw", "", "Twice"); rr.Code != http.StatusCreated {
		t.Fatalf("first create: %d", rr.Code)
	}
	if rr := f.newFolder(t, "rw", "", "Twice"); rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 on duplicate, got %d", rr.Code)
	}
	// An existing *file* with that name must also collide.
	if err := os.WriteFile(filepath.Join(f.rw, "taken.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rr := f.newFolder(t, "rw", "", "taken.txt"); rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 against an existing file, got %d", rr.Code)
	}
}

func TestCreateFolderRejectsHostileNames(t *testing.T) {
	f := newUploadFixture(t)
	for _, name := range []string{"..", ".", "a/b", "/abs", ""} {
		if rr := f.newFolder(t, "rw", "", name); rr.Code < 400 {
			t.Fatalf("name %q accepted with %d", name, rr.Code)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(f.rw), "b")); !os.IsNotExist(err) {
		t.Fatal("folder creation escaped the volume root")
	}
}

func TestCreateFolderRejectsReadOnlyVolume(t *testing.T) {
	f := newUploadFixture(t)
	if rr := f.newFolder(t, "ro", "", "Nope"); rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
	if _, err := os.Stat(filepath.Join(f.ro, "Nope")); !os.IsNotExist(err) {
		t.Fatal("read-only volume got a new directory")
	}
}

func TestCreateFolderRequiresCSRF(t *testing.T) {
	f := newUploadFixture(t)
	body, _ := json.Marshal(map[string]string{"path": "", "name": "NoCSRF"})
	req := httptest.NewRequest(http.MethodPost, "/api/volumes/rw/folder", bytes.NewReader(body))
	for _, c := range f.cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	f.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF, got %d", rr.Code)
	}
}
