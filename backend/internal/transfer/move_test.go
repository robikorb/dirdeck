package transfer_test

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/liquid-glass-file-manager/backend/internal/db"
	appfs "github.com/liquid-glass-file-manager/backend/internal/fs"
	"github.com/liquid-glass-file-manager/backend/internal/transfer"
	"github.com/liquid-glass-file-manager/backend/internal/volumes"
)

func setupTwoRW(t *testing.T) (a, b string, fsys *appfs.Service, mgr *transfer.Manager, database *sql.DB) {
	t.Helper()
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })

	a = filepath.Join(root, "a")
	b = filepath.Join(root, "b")
	_ = os.MkdirAll(filepath.Join(a, "inbox"), 0o755)
	_ = os.MkdirAll(filepath.Join(b, "outbox"), 0o755)
	_ = os.WriteFile(filepath.Join(a, "move-me.txt"), []byte("payload"), 0o644)
	_ = os.WriteFile(filepath.Join(a, "inbox", "nested.txt"), []byte("nested"), 0o644)
	_ = os.MkdirAll(filepath.Join(a, "tree", "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(a, "tree", "sub", "leaf.txt"), []byte("leaf"), 0o644)

	cfgPath := filepath.Join(root, "volumes.yaml")
	_ = os.WriteFile(cfgPath, []byte(`
volumes:
  - id: vol-a
    name: A
    path: `+a+`
    readOnly: false
    showHiddenFiles: false
    thumbnails: false
  - id: vol-b
    name: B
    path: `+b+`
    readOnly: false
    showHiddenFiles: false
    thumbnails: false
  - id: fixture-ro
    name: RO
    path: `+filepath.Join(root, "ro")+`
    readOnly: true
    showHiddenFiles: false
    thumbnails: false
`), 0o600)
	_ = os.MkdirAll(filepath.Join(root, "ro"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "ro", "locked.txt"), []byte("no-touch"), 0o644)

	reg, err := volumes.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	database, err = db.Open(filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	fsys = appfs.New(reg)
	mgr, err = transfer.New(database, fsys)
	if err != nil {
		t.Fatal(err)
	}
	return a, b, fsys, mgr, database
}

func forceEXDEV(_old, _new string) error {
	return &os.LinkError{Op: "rename", Old: _old, New: _new, Err: syscall.EXDEV}
}

func TestSameFilesystemMoveRename(t *testing.T) {
	a, _, _, mgr, _ := setupTwoRW(t)
	job, err := mgr.Create(transfer.CreateRequest{
		Kind:           transfer.KindMove,
		SourceVolumeID: "vol-a",
		SourcePath:     "move-me.txt",
		DestVolumeID:   "vol-a",
		DestDir:        "inbox",
		ConflictPolicy: transfer.PolicyReplace,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := waitStatus(t, mgr, job.ID, transfer.StatusCompleted)
	if done.Status != transfer.StatusCompleted {
		t.Fatalf("expected completed: %+v", done)
	}
	if _, err := os.Stat(filepath.Join(a, "move-me.txt")); !os.IsNotExist(err) {
		t.Fatal("source should be gone after same-FS rename")
	}
	b, err := os.ReadFile(filepath.Join(a, "inbox", "move-me.txt"))
	if err != nil || string(b) != "payload" {
		t.Fatalf("dest: %v %q", err, b)
	}
}

func TestCrossFilesystemMovePreservesHiddenFilesBeforeDeletingSource(t *testing.T) {
	a, b, _, mgr, _ := setupTwoRW(t)
	source := filepath.Join(a, "hidden-tree")
	if err := os.MkdirAll(filepath.Join(source, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".env"), []byte("hidden"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".git", "HEAD"), []byte("main"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "visible.txt"), []byte("visible"), 0o600); err != nil {
		t.Fatal(err)
	}

	mgr.SetTestHooks(nil, forceEXDEV)
	job, err := mgr.Create(transfer.CreateRequest{
		Kind:           transfer.KindMove,
		SourceVolumeID: "vol-a",
		SourcePath:     "hidden-tree",
		DestVolumeID:   "vol-b",
		DestDir:        "outbox",
		ConflictPolicy: transfer.PolicyReplace,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := waitStatus(t, mgr, job.ID, transfer.StatusCompleted)
	if done.FilesTotal != 3 || done.FilesDone != 3 {
		t.Fatalf("unexpected progress: %+v", done)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source still exists or stat failed unexpectedly: %v", err)
	}
	for rel, want := range map[string]string{
		".env":        "hidden",
		".git/HEAD":   "main",
		"visible.txt": "visible",
	} {
		got, err := os.ReadFile(filepath.Join(b, "outbox", "hidden-tree", rel))
		if err != nil || string(got) != want {
			t.Fatalf("destination %s: err=%v got=%q", rel, err, got)
		}
	}
}

func TestBatchMove(t *testing.T) {
	a, b, _, mgr, _ := setupTwoRW(t)
	job, err := mgr.Create(transfer.CreateRequest{
		Kind:           transfer.KindMove,
		SourceVolumeID: "vol-a",
		SourcePaths:    []string{"move-me.txt", "tree"},
		DestVolumeID:   "vol-b",
		DestDir:        "outbox",
		ConflictPolicy: transfer.PolicyReplace,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := waitStatus(t, mgr, job.ID, transfer.StatusCompleted)
	if done.FilesTotal != 2 || done.FilesDone != 2 || done.BytesDone != done.BytesTotal {
		t.Fatalf("batch move progress: %+v", done)
	}
	for _, rel := range []string{"move-me.txt", "tree"} {
		if _, err := os.Stat(filepath.Join(a, rel)); !os.IsNotExist(err) {
			t.Fatalf("source %s still exists: %v", rel, err)
		}
	}
	for _, rel := range []string{
		filepath.Join("outbox", "move-me.txt"),
		filepath.Join("outbox", "tree", "sub", "leaf.txt"),
	} {
		if _, err := os.Stat(filepath.Join(b, rel)); err != nil {
			t.Fatalf("destination %s missing: %v", rel, err)
		}
	}
}

func TestCrossFilesystemMoveFallback(t *testing.T) {
	a, b, _, mgr, _ := setupTwoRW(t)
	mgr.SetTestHooks(nil, forceEXDEV)
	t.Cleanup(func() { mgr.SetTestHooks(nil, nil) })

	job, err := mgr.Create(transfer.CreateRequest{
		Kind:           transfer.KindMove,
		SourceVolumeID: "vol-a",
		SourcePath:     "move-me.txt",
		DestVolumeID:   "vol-b",
		DestDir:        "outbox",
		ConflictPolicy: transfer.PolicyReplace,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := waitStatus(t, mgr, job.ID, transfer.StatusCompleted)
	if done.Status != transfer.StatusCompleted {
		t.Fatalf("expected completed: %+v", done)
	}
	if _, err := os.Stat(filepath.Join(a, "move-me.txt")); !os.IsNotExist(err) {
		t.Fatal("source must be deleted after verified cross-FS move")
	}
	data, err := os.ReadFile(filepath.Join(b, "outbox", "move-me.txt"))
	if err != nil || string(data) != "payload" {
		t.Fatalf("dest: %v %q", err, data)
	}
	entries, _ := os.ReadDir(filepath.Join(b, "outbox"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".lgfm-partial-") {
			t.Fatalf("leftover partial %s", e.Name())
		}
	}
}

func TestMoveRejectReadOnlySourceAndDest(t *testing.T) {
	_, _, _, mgr, _ := setupTwoRW(t)

	_, err := mgr.Create(transfer.CreateRequest{
		Kind: transfer.KindMove, SourceVolumeID: "fixture-ro", SourcePath: "locked.txt",
		DestVolumeID: "vol-a", DestDir: "", ConflictPolicy: transfer.PolicyReplace,
	})
	if err == nil || !errors.Is(err, appfs.ErrReadOnly) {
		t.Fatalf("expected RO source rejection, got %v", err)
	}

	_, err = mgr.Create(transfer.CreateRequest{
		Kind: transfer.KindMove, SourceVolumeID: "vol-a", SourcePath: "move-me.txt",
		DestVolumeID: "fixture-ro", DestDir: "", ConflictPolicy: transfer.PolicyReplace,
	})
	if err == nil || !errors.Is(err, appfs.ErrReadOnly) {
		t.Fatalf("expected RO dest rejection, got %v", err)
	}
}

func TestMoveFailureInjectionPreservesSource(t *testing.T) {
	stages := []string{
		transfer.StageRenameAttempt,
		transfer.StageAfterCopyClose,
		transfer.StageAfterVerify,
		transfer.StageBeforeDestFinal,
	}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			a, b, _, mgr, _ := setupTwoRW(t)
			failStage := stage
			mgr.SetTestHooks(func(s string) error {
				if s == failStage {
					return errors.New("injected:" + s)
				}
				return nil
			}, forceEXDEV)
			job, err := mgr.Create(transfer.CreateRequest{
				Kind: transfer.KindMove, SourceVolumeID: "vol-a", SourcePath: "move-me.txt",
				DestVolumeID: "vol-b", DestDir: "outbox", ConflictPolicy: transfer.PolicyReplace,
			})
			if err != nil {
				t.Fatal(err)
			}
			done := waitStatus(t, mgr, job.ID, transfer.StatusFailed)
			if done.Status != transfer.StatusFailed {
				t.Fatalf("expected failed, got %+v", done)
			}
			// Source must still exist — never deleted before dest verification.
			if _, err := os.Stat(filepath.Join(a, "move-me.txt")); err != nil {
				t.Fatalf("source deleted prematurely at stage %s: %v", stage, err)
			}
			if _, err := os.Stat(filepath.Join(b, "outbox", "move-me.txt")); err == nil {
				t.Fatalf("final dest must not exist after failure at %s", stage)
			}
			entries, _ := os.ReadDir(filepath.Join(b, "outbox"))
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), ".lgfm-partial-") {
					t.Fatalf("staging left after failure at %s: %s", stage, e.Name())
				}
			}
		})
	}
}

func TestMoveFailureAfterDestFinalKeepsBoth(t *testing.T) {
	a, b, _, mgr, _ := setupTwoRW(t)
	mgr.SetTestHooks(func(s string) error {
		if s == transfer.StageAfterDestFinal {
			return errors.New("injected after dest final")
		}
		return nil
	}, forceEXDEV)

	job, err := mgr.Create(transfer.CreateRequest{
		Kind: transfer.KindMove, SourceVolumeID: "vol-a", SourcePath: "move-me.txt",
		DestVolumeID: "vol-b", DestDir: "outbox", ConflictPolicy: transfer.PolicyReplace,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := waitStatus(t, mgr, job.ID, transfer.StatusFailed)
	if done.Status == transfer.StatusCompleted {
		t.Fatal("must not report completed when source still present")
	}
	if _, err := os.Stat(filepath.Join(a, "move-me.txt")); err != nil {
		t.Fatal("source should remain")
	}
	data, err := os.ReadFile(filepath.Join(b, "outbox", "move-me.txt"))
	if err != nil || string(data) != "payload" {
		t.Fatalf("verified dest should remain: %v %q", err, data)
	}
}

func TestMoveSourceDeleteFailurePartialSuccess(t *testing.T) {
	a, b, _, mgr, _ := setupTwoRW(t)
	mgr.SetTestHooks(func(s string) error {
		if s == transfer.StageSourceDelete {
			return errors.New("cannot unlink source")
		}
		return nil
	}, forceEXDEV)

	job, err := mgr.Create(transfer.CreateRequest{
		Kind: transfer.KindMove, SourceVolumeID: "vol-a", SourcePath: "move-me.txt",
		DestVolumeID: "vol-b", DestDir: "outbox", ConflictPolicy: transfer.PolicyReplace,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := waitStatus(t, mgr, job.ID, transfer.StatusFailed)
	if done.Status == transfer.StatusCompleted {
		t.Fatal("partial move must not be completed")
	}
	if done.ErrorMessage == nil || !strings.Contains(*done.ErrorMessage, "source could not be deleted") {
		t.Fatalf("expected source-delete message, got %v", done.ErrorMessage)
	}
	if _, err := os.Stat(filepath.Join(a, "move-me.txt")); err != nil {
		t.Fatal("source remains on delete failure")
	}
	data, _ := os.ReadFile(filepath.Join(b, "outbox", "move-me.txt"))
	if string(data) != "payload" {
		t.Fatalf("dest should be complete: %q", data)
	}
}

func TestMoveDirectoryCrossFilesystem(t *testing.T) {
	a, b, _, mgr, _ := setupTwoRW(t)
	mgr.SetTestHooks(nil, forceEXDEV)
	job, err := mgr.Create(transfer.CreateRequest{
		Kind: transfer.KindMove, SourceVolumeID: "vol-a", SourcePath: "tree",
		DestVolumeID: "vol-b", DestDir: "outbox", ConflictPolicy: transfer.PolicyReplace,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, mgr, job.ID, transfer.StatusCompleted)
	if _, err := os.Stat(filepath.Join(a, "tree")); !os.IsNotExist(err) {
		t.Fatal("source tree should be removed")
	}
	data, err := os.ReadFile(filepath.Join(b, "outbox", "tree", "sub", "leaf.txt"))
	if err != nil || string(data) != "leaf" {
		t.Fatalf("moved tree: %v %q", err, data)
	}
}

func TestMoveReconcileInterruptedNeverCompletes(t *testing.T) {
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	_ = os.MkdirAll(a, 0o755)
	_ = os.MkdirAll(b, 0o755)
	_ = os.WriteFile(filepath.Join(a, "x.txt"), []byte("keep"), 0o644)
	cfgPath := filepath.Join(root, "volumes.yaml")
	_ = os.WriteFile(cfgPath, []byte(`
volumes:
  - id: vol-a
    name: A
    path: `+a+`
    readOnly: false
    showHiddenFiles: false
    thumbnails: false
  - id: vol-b
    name: B
    path: `+b+`
    readOnly: false
    showHiddenFiles: false
    thumbnails: false
`), 0o600)
	reg, _ := volumes.Load(cfgPath)
	dataDir := filepath.Join(root, "data")
	database, err := db.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	_, err = database.Exec(`
		INSERT INTO transfer_jobs (
			id, kind, status, source_volume_id, source_path, dest_volume_id, dest_dir, dest_name,
			conflict_policy, created_at, updated_at, staging_name
		) VALUES ('move1', 'move', 'running', 'vol-a', 'x.txt', 'vol-b', '', 'x.txt', 'replace', ?, ?, '.lgfm-partial-move1')
	`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(b, ".lgfm-partial-move1"), []byte("partial"), 0o644)
	_ = database.Close()

	database, err = db.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	mgr, err := transfer.New(database, appfs.New(reg))
	if err != nil {
		t.Fatal(err)
	}
	job, err := mgr.Get("move1")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != transfer.StatusFailed {
		t.Fatalf("expected failed, got %s", job.Status)
	}
	if _, err := os.Stat(filepath.Join(a, "x.txt")); err != nil {
		t.Fatal("source must survive interrupted move")
	}
	if _, err := os.Stat(filepath.Join(b, "x.txt")); err == nil {
		t.Fatal("must not promote partial to final on reconcile")
	}
}

func TestRenameUnexpectedErrorDoesNotFallback(t *testing.T) {
	a, b, _, mgr, _ := setupTwoRW(t)
	mgr.SetTestHooks(nil, func(old, new string) error {
		return errors.New("permission boom")
	})
	job, err := mgr.Create(transfer.CreateRequest{
		Kind: transfer.KindMove, SourceVolumeID: "vol-a", SourcePath: "move-me.txt",
		DestVolumeID: "vol-b", DestDir: "outbox", ConflictPolicy: transfer.PolicyReplace,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := waitStatus(t, mgr, job.ID, transfer.StatusFailed)
	if done.Status != transfer.StatusFailed {
		t.Fatalf("unexpected status %+v", done)
	}
	if _, err := os.Stat(filepath.Join(a, "move-me.txt")); err != nil {
		t.Fatal("source should remain when rename fails unexpectedly")
	}
	if _, err := os.Stat(filepath.Join(b, "outbox", "move-me.txt")); err == nil {
		t.Fatal("must not copy when rename error is not EXDEV")
	}
}
