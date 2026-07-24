package fs_test

import (
	"os"
	"path/filepath"
	"testing"

	appfs "github.com/liquid-glass-file-manager/backend/internal/fs"
	"github.com/liquid-glass-file-manager/backend/internal/volumes"
)

func setupVol(t *testing.T) (*appfs.Service, string) {
	t.Helper()
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })

	ro := filepath.Join(root, "ro")
	rw := filepath.Join(root, "rw")
	mustMkdir(t, filepath.Join(ro, "photos"))
	mustMkdir(t, filepath.Join(rw, "projects"))
	mustWrite(t, filepath.Join(ro, "readme.txt"), "hello ro")
	mustWrite(t, filepath.Join(ro, "photos", "sample.txt"), "photo")
	mustWrite(t, filepath.Join(rw, "notes.txt"), "hello rw")

	// Symlink that escapes the volume
	escapeTarget := filepath.Join(t.TempDir(), "secret.txt")
	mustWrite(t, escapeTarget, "secret")
	if err := os.Symlink(escapeTarget, filepath.Join(ro, "escape.link")); err != nil {
		t.Fatal(err)
	}
	// Symlink inside volume (leaf OK to list)
	if err := os.Symlink("readme.txt", filepath.Join(ro, "alias.link")); err != nil {
		t.Fatal(err)
	}
	// Nested dir with symlink used for traversal attempt
	mustMkdir(t, filepath.Join(ro, "via"))
	if err := os.Symlink("..", filepath.Join(ro, "via", "up.link")); err != nil {
		t.Fatal(err)
	}

	cfg := filepath.Join(root, "volumes.yaml")
	content := `
volumes:
  - id: fixture-ro
    name: RO
    path: ` + ro + `
    readOnly: true
    showHiddenFiles: false
    thumbnails: false
  - id: fixture-rw
    name: RW
    path: ` + rw + `
    readOnly: false
    showHiddenFiles: false
    thumbnails: false
`
	mustWrite(t, cfg, content)
	reg, err := volumes.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return appfs.New(reg), ro
}

func TestRejectTraversalAndAbsolute(t *testing.T) {
	svc, _ := setupVol(t)
	for _, p := range []string{"..", "../", "photos/../../readme.txt", "/etc/passwd", `..\windows`} {
		if _, err := svc.List("fixture-ro", p); err == nil {
			t.Fatalf("expected reject for %q", p)
		}
	}
	if _, err := svc.Stat("fixture-ro", "/etc/passwd"); err == nil {
		t.Fatal("expected absolute path reject")
	}
}

func TestSymlinkEscapeAndNoFollow(t *testing.T) {
	svc, _ := setupVol(t)
	entries, err := svc.List("fixture-ro", "")
	if err != nil {
		t.Fatal(err)
	}
	var sawEscape, sawAlias bool
	for _, e := range entries {
		if e.Name == "escape.link" {
			sawEscape = true
			if !e.IsSymlink {
				t.Fatal("escape.link should be symlink")
			}
		}
		if e.Name == "alias.link" {
			sawAlias = true
		}
	}
	if !sawEscape || !sawAlias {
		t.Fatal("expected symlink entries in listing")
	}
	// Must not open/follow escape symlink for download
	if _, err := svc.OpenFile("fixture-ro", "escape.link"); err == nil {
		t.Fatal("must not follow/open escape symlink")
	}
	// Must not traverse through symlink directory
	if _, err := svc.List("fixture-ro", "via/up.link"); err == nil {
		t.Fatal("must not treat symlink as directory")
	}
	if _, err := svc.List("fixture-ro", "via/up.link/readme.txt"); err == nil {
		t.Fatal("must not traverse through symlink")
	}
}

func TestListAndStat(t *testing.T) {
	svc, _ := setupVol(t)
	entries, err := svc.List("fixture-ro", "photos")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "sample.txt" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
	meta, err := svc.Stat("fixture-ro", "readme.txt")
	if err != nil {
		t.Fatal(err)
	}
	if meta.IsDir || meta.Size == 0 {
		t.Fatalf("unexpected meta: %+v", meta)
	}
}

func TestReadOnlyFlag(t *testing.T) {
	svc, _ := setupVol(t)
	res, err := svc.Resolve("fixture-ro", "")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Close()
	if err := appfs.EnsureReadWrite(res.Volume); err == nil {
		t.Fatal("expected read-only enforcement")
	}
	res2, err := svc.Resolve("fixture-rw", "")
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Close()
	if err := appfs.EnsureReadWrite(res2.Volume); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
