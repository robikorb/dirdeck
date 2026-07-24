package fs_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	appfs "github.com/liquid-glass-file-manager/backend/internal/fs"
	"github.com/liquid-glass-file-manager/backend/internal/volumes"
)

func setupMutableFS(t *testing.T) (string, *appfs.Service) {
	t.Helper()
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })
	rw := filepath.Join(root, "rw")
	_ = os.MkdirAll(filepath.Join(rw, "folder"), 0o755)
	_ = os.WriteFile(filepath.Join(rw, "notes.md"), []byte("# Original\n"), 0o640)
	_ = os.WriteFile(filepath.Join(rw, "taken.md"), []byte("taken"), 0o644)
	cfg := filepath.Join(root, "volumes.yaml")
	_ = os.WriteFile(cfg, []byte(`
volumes:
  - id: rw
    name: RW
    path: `+rw+`
    readOnly: false
    showHiddenFiles: false
    thumbnails: false
`), 0o600)
	reg, err := volumes.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return rw, appfs.New(reg)
}

func TestRenameEntryNoReplace(t *testing.T) {
	rw, svc := setupMutableFS(t)
	meta, err := svc.RenameEntry("rw", "notes.md", "renamed.md")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Path != "renamed.md" {
		t.Fatalf("unexpected path %q", meta.Path)
	}
	if _, err := os.Stat(filepath.Join(rw, "notes.md")); !os.IsNotExist(err) {
		t.Fatal("old name should be gone")
	}
	if _, err := svc.RenameEntry("rw", "renamed.md", "taken.md"); !errors.Is(err, appfs.ErrExists) {
		t.Fatalf("expected ErrExists, got %v", err)
	}
	if _, err := svc.RenameEntry("rw", "renamed.md", "../escape.md"); !errors.Is(err, appfs.ErrInvalidPath) {
		t.Fatalf("expected invalid path, got %v", err)
	}
}

func TestAtomicTextReadWriteAndConflict(t *testing.T) {
	rw, svc := setupMutableFS(t)
	data, meta, err := svc.ReadTextFile("rw", "notes.md", 1024)
	if err != nil || string(data) != "# Original\n" {
		t.Fatalf("read: %v %q", err, data)
	}
	updated, err := svc.WriteTextFile("rw", "notes.md", []byte("# Updated\n"), 1024, meta.ModTime)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Size != int64(len("# Updated\n")) {
		t.Fatalf("size %d", updated.Size)
	}
	onDisk, _ := os.ReadFile(filepath.Join(rw, "notes.md"))
	if string(onDisk) != "# Updated\n" {
		t.Fatalf("content %q", onDisk)
	}
	if mode := updated.Mode; mode != "-rw-r-----" {
		t.Fatalf("mode changed: %s", mode)
	}
	if _, err := svc.WriteTextFile("rw", "notes.md", []byte("stale"), 1024, meta.ModTime); !errors.Is(err, appfs.ErrChanged) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if _, err := svc.WriteTextFile("rw", "notes.md", []byte("too large"), 2, time.Time{}); !errors.Is(err, appfs.ErrTooLarge) {
		t.Fatalf("expected size rejection, got %v", err)
	}
}

func TestDeleteEntryRecursiveAndSymlinkSafe(t *testing.T) {
	rw, svc := setupMutableFS(t)
	tree := filepath.Join(rw, "delete-me")
	if err := os.MkdirAll(filepath.Join(tree, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "nested", "episode.mkv"), []byte("media"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "keep-me.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(tree, "outside-link")); err != nil {
		t.Fatal(err)
	}

	if err := svc.DeleteEntry("rw", "delete-me/outside-link"); err != nil {
		t.Fatalf("delete symlink: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("symlink target was touched: %v", err)
	}
	if err := svc.DeleteEntry("rw", "delete-me"); err != nil {
		t.Fatalf("delete tree: %v", err)
	}
	if _, err := os.Lstat(tree); !os.IsNotExist(err) {
		t.Fatalf("tree still exists: %v", err)
	}
	if err := svc.DeleteEntry("rw", ""); !errors.Is(err, appfs.ErrInvalidPath) {
		t.Fatalf("expected volume root rejection, got %v", err)
	}
}

func TestDeleteEntriesBatchAndCollapseDescendants(t *testing.T) {
	rw, svc := setupMutableFS(t)
	_ = os.MkdirAll(filepath.Join(rw, "parent", "child"), 0o755)
	_ = os.WriteFile(filepath.Join(rw, "parent", "child", "nested.txt"), []byte("nested"), 0o644)
	_ = os.WriteFile(filepath.Join(rw, "other.txt"), []byte("other"), 0o644)

	if err := svc.DeleteEntries("rw", []string{
		"parent/child/nested.txt",
		"parent",
		"other.txt",
		"other.txt",
	}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"parent", "other.txt"} {
		if _, err := os.Lstat(filepath.Join(rw, rel)); !os.IsNotExist(err) {
			t.Fatalf("%s still exists: %v", rel, err)
		}
	}
}
