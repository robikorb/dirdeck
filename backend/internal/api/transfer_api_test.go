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
	"time"

	"github.com/liquid-glass-file-manager/backend/internal/api"
	"github.com/liquid-glass-file-manager/backend/internal/auth"
	"github.com/liquid-glass-file-manager/backend/internal/db"
	appfs "github.com/liquid-glass-file-manager/backend/internal/fs"
	"github.com/liquid-glass-file-manager/backend/internal/transfer"
	"github.com/liquid-glass-file-manager/backend/internal/volumes"
)

func TestTransferCopyAPI(t *testing.T) {
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })

	ro := filepath.Join(root, "ro")
	rw := filepath.Join(root, "rw")
	_ = os.MkdirAll(ro, 0o755)
	_ = os.MkdirAll(rw, 0o755)
	_ = os.WriteFile(filepath.Join(ro, "a.txt"), []byte("abc"), 0o644)
	_ = os.WriteFile(filepath.Join(ro, "b.txt"), []byte("batch"), 0o644)

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
	_ = authSvc.BootstrapAdmin("admin", "secret-pass")
	fsys := appfs.New(reg)
	xfer, err := transfer.New(database, fsys)
	if err != nil {
		t.Fatal(err)
	}
	srv := &api.Server{Auth: authSvc, FS: fsys, Volumes: reg, Transfers: xfer, Ready: true}
	h := srv.Handler()

	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "secret-pass"})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("login: %d", rr.Code)
	}
	var loginResp map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &loginResp)
	csrf, _ := loginResp["csrfToken"].(string)
	cookies := rr.Result().Cookies()

	authed := func(method, url string, body io.Reader) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, url, body)
		for _, c := range cookies {
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

	// RO destination rejected
	_ = os.WriteFile(filepath.Join(rw, "notes.txt"), []byte("n"), 0o644)
	payload, _ := json.Marshal(map[string]any{
		"kind": "copy", "sourceVolumeId": "fixture-rw", "sourcePath": "notes.txt",
		"destVolumeId": "fixture-ro", "destDir": "", "conflictPolicy": "skip",
	})
	rr = authed(http.MethodPost, "/api/transfers", bytes.NewReader(payload))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for RO dest, got %d %s", rr.Code, rr.Body.String())
	}

	payload, _ = json.Marshal(map[string]any{
		"kind": "copy", "sourceVolumeId": "fixture-ro", "sourcePath": "a.txt",
		"destVolumeId": "fixture-rw", "destDir": "", "conflictPolicy": "replace",
	})
	rr = authed(http.MethodPost, "/api/transfers", bytes.NewReader(payload))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body.String())
	}
	var job transfer.Job
	_ = json.Unmarshal(rr.Body.Bytes(), &job)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rr = authed(http.MethodGet, "/api/transfers/"+job.ID, nil)
		_ = json.Unmarshal(rr.Body.Bytes(), &job)
		if job.Status == transfer.StatusCompleted {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.Status != transfer.StatusCompleted {
		t.Fatalf("job not completed: %+v", job)
	}
	b, err := os.ReadFile(filepath.Join(rw, "a.txt"))
	if err != nil || string(b) != "abc" {
		t.Fatalf("copied content: %v %q", err, b)
	}

	payload, _ = json.Marshal(map[string]any{
		"kind": "copy", "sourceVolumeId": "fixture-ro",
		"sourcePaths":  []string{"a.txt", "b.txt"},
		"destVolumeId": "fixture-rw", "destDir": "", "conflictPolicy": "replace",
	})
	rr = authed(http.MethodPost, "/api/transfers", bytes.NewReader(payload))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create batch: %d %s", rr.Code, rr.Body.String())
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &job)
	if len(job.SourcePaths) != 2 {
		t.Fatalf("batch sources missing: %+v", job)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rr = authed(http.MethodGet, "/api/transfers/"+job.ID, nil)
		_ = json.Unmarshal(rr.Body.Bytes(), &job)
		if job.Status == transfer.StatusCompleted || job.Status == transfer.StatusFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.Status != transfer.StatusCompleted {
		t.Fatalf("batch not completed: %+v", job)
	}
	if copied, err := os.ReadFile(filepath.Join(rw, "b.txt")); err != nil || string(copied) != "batch" {
		t.Fatalf("batch copied content: %v %q", err, copied)
	}

	// Move from RO source must be rejected (source delete would be required).
	payload, _ = json.Marshal(map[string]any{
		"kind": "move", "sourceVolumeId": "fixture-ro", "sourcePath": "a.txt",
		"destVolumeId": "fixture-rw", "destDir": "", "conflictPolicy": "replace",
	})
	rr = authed(http.MethodPost, "/api/transfers", bytes.NewReader(payload))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for move from RO source, got %d %s", rr.Code, rr.Body.String())
	}

	_ = os.WriteFile(filepath.Join(rw, "movable.txt"), []byte("go"), 0o644)
	_ = os.MkdirAll(filepath.Join(rw, "dest"), 0o755)
	payload, _ = json.Marshal(map[string]any{
		"kind": "move", "sourceVolumeId": "fixture-rw", "sourcePath": "movable.txt",
		"destVolumeId": "fixture-rw", "destDir": "dest", "conflictPolicy": "replace",
	})
	rr = authed(http.MethodPost, "/api/transfers", bytes.NewReader(payload))
	if rr.Code != http.StatusCreated {
		t.Fatalf("create move: %d %s", rr.Code, rr.Body.String())
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &job)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rr = authed(http.MethodGet, "/api/transfers/"+job.ID, nil)
		_ = json.Unmarshal(rr.Body.Bytes(), &job)
		if job.Status == transfer.StatusCompleted || job.Status == transfer.StatusFailed {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.Status != transfer.StatusCompleted {
		t.Fatalf("move not completed: %+v", job)
	}
	if _, err := os.Stat(filepath.Join(rw, "movable.txt")); err == nil {
		t.Fatal("source should be gone after move")
	}
	moved, err := os.ReadFile(filepath.Join(rw, "dest", "movable.txt"))
	if err != nil || string(moved) != "go" {
		t.Fatalf("moved content: %v %q", err, moved)
	}
}
