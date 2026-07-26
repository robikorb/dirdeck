package api_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/robikorb/dirdeck/backend/internal/api"
	"github.com/robikorb/dirdeck/backend/internal/auth"
	"github.com/robikorb/dirdeck/backend/internal/db"
	appfs "github.com/robikorb/dirdeck/backend/internal/fs"
	"github.com/robikorb/dirdeck/backend/internal/volumes"
)

func TestEditorAndRenameAPI(t *testing.T) {
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })
	rw := filepath.Join(root, "rw")
	ro := filepath.Join(root, "ro")
	_ = os.MkdirAll(rw, 0o755)
	_ = os.MkdirAll(ro, 0o755)
	_ = os.WriteFile(filepath.Join(rw, "config.json"), []byte("{\"ok\":true}\n"), 0o640)
	_ = os.WriteFile(filepath.Join(ro, "locked.md"), []byte("# Locked\n"), 0o644)
	cfg := filepath.Join(root, "volumes.yaml")
	_ = os.WriteFile(cfg, []byte(`
volumes:
  - id: rw
    name: RW
    path: `+rw+`
    readOnly: false
    showHiddenFiles: false
    thumbnails: false
  - id: ro
    name: RO
    path: `+ro+`
    readOnly: true
    showHiddenFiles: false
    thumbnails: false
`), 0o600)
	reg, err := volumes.Load(cfg)
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
	handler := (&api.Server{Auth: authSvc, FS: appfs.New(reg), Volumes: reg, Ready: true}).Handler()

	loginBody, _ := json.Marshal(map[string]string{"username": "admin", "password": "secret-pass"})
	login := httptest.NewRecorder()
	handler.ServeHTTP(login, httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody)))
	var session map[string]any
	_ = json.Unmarshal(login.Body.Bytes(), &session)
	csrf, _ := session["csrfToken"].(string)
	cookies := login.Result().Cookies()

	authed := func(method, url string, body io.Reader, withCSRF bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, url, body)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		if withCSRF {
			req.Header.Set("X-CSRF-Token", csrf)
			req.Header.Set("Origin", "http://example.com")
			req.Host = "example.com"
		}
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}

	// An editable extension holding non-UTF-8 bytes must answer 415, not 500.
	// These errors were mapped only in writeTransferError, so the editor showed
	// "internal error" for every binary file with a text-looking name.
	_ = os.WriteFile(filepath.Join(rw, "binary.md"), []byte{0xff, 0xfe, 0x00, 0x41}, 0o640)
	binary := authed(http.MethodGet, "/api/volumes/rw/content?path=binary.md", nil, false)
	if binary.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("binary content: got %d, want 415; body %s", binary.Code, binary.Body.String())
	}

	get := authed(http.MethodGet, "/api/volumes/rw/content?path=config.json", nil, false)
	if get.Code != http.StatusOK {
		t.Fatalf("get content: %d %s", get.Code, get.Body.String())
	}
	var opened struct {
		Content string `json:"content"`
		ModTime string `json:"modTime"`
	}
	_ = json.Unmarshal(get.Body.Bytes(), &opened)
	if opened.Content != "{\"ok\":true}\n" || opened.ModTime == "" {
		t.Fatalf("opened %+v", opened)
	}

	payload, _ := json.Marshal(map[string]string{
		"content":         "{\n  \"ok\": false\n}\n",
		"expectedModTime": opened.ModTime,
	})
	if noCSRF := authed(http.MethodPut, "/api/volumes/rw/content?path=config.json", bytes.NewReader(payload), false); noCSRF.Code != http.StatusForbidden {
		t.Fatalf("expected csrf rejection, got %d", noCSRF.Code)
	}
	saved := authed(http.MethodPut, "/api/volumes/rw/content?path=config.json", bytes.NewReader(payload), true)
	if saved.Code != http.StatusOK {
		t.Fatalf("save: %d %s", saved.Code, saved.Body.String())
	}
	if stale := authed(http.MethodPut, "/api/volumes/rw/content?path=config.json", bytes.NewReader(payload), true); stale.Code != http.StatusConflict {
		t.Fatalf("expected stale conflict, got %d %s", stale.Code, stale.Body.String())
	}
	onDisk, _ := os.ReadFile(filepath.Join(rw, "config.json"))
	if string(onDisk) != "{\n  \"ok\": false\n}\n" {
		t.Fatalf("saved content %q", onDisk)
	}

	renameBody, _ := json.Marshal(map[string]string{"path": "config.json", "newName": "settings.json"})
	renamed := authed(http.MethodPost, "/api/volumes/rw/rename", bytes.NewReader(renameBody), true)
	if renamed.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", renamed.Code, renamed.Body.String())
	}
	if _, err := os.Stat(filepath.Join(rw, "settings.json")); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}

	deleteDir := filepath.Join(rw, "delete-dir")
	_ = os.MkdirAll(filepath.Join(deleteDir, "nested"), 0o755)
	_ = os.WriteFile(filepath.Join(deleteDir, "nested", "file.txt"), []byte("delete"), 0o644)
	deleteBody, _ := json.Marshal(map[string]string{"path": "delete-dir"})
	if noCSRF := authed(http.MethodDelete, "/api/volumes/rw/entry", bytes.NewReader(deleteBody), false); noCSRF.Code != http.StatusForbidden {
		t.Fatalf("expected delete csrf rejection, got %d", noCSRF.Code)
	}
	deleted := authed(http.MethodDelete, "/api/volumes/rw/entry", bytes.NewReader(deleteBody), true)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body.String())
	}
	if _, err := os.Lstat(deleteDir); !os.IsNotExist(err) {
		t.Fatalf("deleted directory still exists: %v", err)
	}

	_ = os.WriteFile(filepath.Join(rw, "batch-one.txt"), []byte("one"), 0o644)
	_ = os.WriteFile(filepath.Join(rw, "batch-two.txt"), []byte("two"), 0o644)
	deleteBatchBody, _ := json.Marshal(map[string]any{
		"paths": []string{"batch-one.txt", "batch-two.txt"},
	})
	deleteBatch := authed(http.MethodDelete, "/api/volumes/rw/entry", bytes.NewReader(deleteBatchBody), true)
	if deleteBatch.Code != http.StatusNoContent {
		t.Fatalf("batch delete: %d %s", deleteBatch.Code, deleteBatch.Body.String())
	}
	for _, name := range []string{"batch-one.txt", "batch-two.txt"} {
		if _, err := os.Lstat(filepath.Join(rw, name)); !os.IsNotExist(err) {
			t.Fatalf("batch item %s still exists: %v", name, err)
		}
	}

	rootBody, _ := json.Marshal(map[string]string{"path": ""})
	rootDelete := authed(http.MethodDelete, "/api/volumes/rw/entry", bytes.NewReader(rootBody), true)
	if rootDelete.Code != http.StatusBadRequest {
		t.Fatalf("expected root delete rejection, got %d %s", rootDelete.Code, rootDelete.Body.String())
	}

	roPayload, _ := json.Marshal(map[string]string{"content": "# Changed\n"})
	roSave := authed(http.MethodPut, "/api/volumes/ro/content?path=locked.md", bytes.NewReader(roPayload), true)
	if roSave.Code != http.StatusForbidden {
		t.Fatalf("expected readonly rejection, got %d %s", roSave.Code, roSave.Body.String())
	}
	roDeleteBody, _ := json.Marshal(map[string]string{"path": "locked.md"})
	roDelete := authed(http.MethodDelete, "/api/volumes/ro/entry", bytes.NewReader(roDeleteBody), true)
	if roDelete.Code != http.StatusForbidden {
		t.Fatalf("expected readonly delete rejection, got %d %s", roDelete.Code, roDelete.Body.String())
	}
}
