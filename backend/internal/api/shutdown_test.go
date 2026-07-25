package api_test

import (
	"bytes"
	"context"
	"encoding/json"
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

// newShutdownFixture builds a live server with one writable volume.
func newShutdownFixture(t *testing.T) (*api.Server, http.Handler) {
	t.Helper()
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })

	rw := filepath.Join(root, "rw")
	if err := os.MkdirAll(rw, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(root, "volumes.yaml")
	if err := os.WriteFile(cfgPath, []byte(`
volumes:
  - id: fixture-rw
    name: RW
    path: `+rw+`
    readOnly: false
    showHiddenFiles: false
    thumbnails: false
`), 0o600); err != nil {
		t.Fatal(err)
	}
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
	fsys := appfs.New(reg)
	xfer, err := transfer.New(database, fsys)
	if err != nil {
		t.Fatal(err)
	}
	srv := &api.Server{Auth: authSvc, FS: fsys, Volumes: reg, Transfers: xfer, Ready: true}
	return srv, srv.Handler()
}

func loginCookies(t *testing.T, baseURL string) []*http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "secret-pass"})
	resp, err := http.Post(baseURL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	return resp.Cookies()
}

// An open SSE stream must not keep http.Server.Shutdown blocked until its
// deadline. Before BeginShutdown existed this returned context.DeadlineExceeded
// after the full timeout, which then left no budget for transfer cleanup.
func TestShutdownReleasesOpenEventStream(t *testing.T) {
	srv, handler := newShutdownFixture(t)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	cookies := loginCookies(t, ts.URL)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/transfers/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	// Headers arriving proves the handler is running and the connection is
	// active, so the server cannot treat it as idle.
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != http.StatusOK {
		t.Fatalf("event stream: %d", stream.StatusCode)
	}

	srv.BeginShutdown()

	const budget = 3 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	start := time.Now()
	if err := ts.Config.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown returned %v after %s", err, time.Since(start))
	}
	if elapsed := time.Since(start); elapsed > budget/2 {
		t.Fatalf("shutdown took %s; the event stream did not release the connection", elapsed)
	}
}

func TestBeginShutdownIsIdempotent(t *testing.T) {
	srv, _ := newShutdownFixture(t)
	srv.BeginShutdown()
	srv.BeginShutdown()
}
