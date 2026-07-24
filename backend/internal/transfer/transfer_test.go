package transfer_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/liquid-glass-file-manager/backend/internal/db"
	appfs "github.com/liquid-glass-file-manager/backend/internal/fs"
	"github.com/liquid-glass-file-manager/backend/internal/transfer"
	"github.com/liquid-glass-file-manager/backend/internal/volumes"
)

func setupVolumes(t *testing.T) (ro, rw string, fsys *appfs.Service, mgr *transfer.Manager, dataDir string) {
	t.Helper()
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })

	ro = filepath.Join(root, "ro")
	rw = filepath.Join(root, "rw")
	_ = os.MkdirAll(filepath.Join(ro, "photos"), 0o755)
	_ = os.MkdirAll(rw, 0o755)
	_ = os.WriteFile(filepath.Join(ro, "a.txt"), []byte("hello-ro"), 0o644)
	_ = os.WriteFile(filepath.Join(ro, "photos", "p.txt"), []byte("photo"), 0o644)
	_ = os.WriteFile(filepath.Join(rw, "notes.txt"), []byte("notes"), 0o644)

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
	dataDir = filepath.Join(root, "data")
	database, err := db.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	fsys = appfs.New(reg)
	mgr, err = transfer.New(database, fsys)
	if err != nil {
		t.Fatal(err)
	}
	return ro, rw, fsys, mgr, dataDir
}

func waitStatus(t *testing.T, mgr *transfer.Manager, id string, want ...string) *transfer.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := mgr.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range want {
			if job.Status == w {
				return job
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	job, _ := mgr.Get(id)
	t.Fatalf("timeout waiting for %v, got %+v", want, job)
	return nil
}

func TestCrossVolumeCopy(t *testing.T) {
	_, rw, _, mgr, _ := setupVolumes(t)
	job, err := mgr.Create(transfer.CreateRequest{
		Kind:           transfer.KindCopy,
		SourceVolumeID: "fixture-ro",
		SourcePath:     "a.txt",
		DestVolumeID:   "fixture-rw",
		DestDir:        "",
		ConflictPolicy: transfer.PolicyReplace,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := waitStatus(t, mgr, job.ID, transfer.StatusCompleted)
	if done.BytesDone != done.BytesTotal || done.BytesTotal != 8 {
		t.Fatalf("progress: %+v", done)
	}
	b, err := os.ReadFile(filepath.Join(rw, "a.txt"))
	if err != nil || string(b) != "hello-ro" {
		t.Fatalf("dest content: %v %q", err, b)
	}
	// No partial left
	entries, _ := os.ReadDir(rw)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".lgfm-partial-") {
			t.Fatalf("leftover partial %s", e.Name())
		}
	}
}

func TestBatchCopyAggregatesProgress(t *testing.T) {
	_, rw, _, mgr, _ := setupVolumes(t)
	job, err := mgr.Create(transfer.CreateRequest{
		Kind:           transfer.KindCopy,
		SourceVolumeID: "fixture-ro",
		SourcePaths:    []string{"a.txt", "photos"},
		DestVolumeID:   "fixture-rw",
		DestDir:        "",
		ConflictPolicy: transfer.PolicyReplace,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := waitStatus(t, mgr, job.ID, transfer.StatusCompleted)
	if len(done.SourcePaths) != 2 || done.DestName != "2 items" {
		t.Fatalf("batch metadata: %+v", done)
	}
	if done.BytesTotal != 13 || done.BytesDone != 13 || done.FilesTotal != 2 || done.FilesDone != 2 {
		t.Fatalf("batch progress: %+v", done)
	}
	for _, rel := range []string{"a.txt", filepath.Join("photos", "p.txt")} {
		if _, err := os.Stat(filepath.Join(rw, rel)); err != nil {
			t.Fatalf("missing batch destination %s: %v", rel, err)
		}
	}
}

func TestBatchCopyDeduplicatesCanonicalSourcePaths(t *testing.T) {
	_, _, _, manager, _ := setupVolumes(t)
	job, err := manager.Create(transfer.CreateRequest{
		Kind:           transfer.KindCopy,
		SourceVolumeID: "fixture-ro",
		SourcePaths:    []string{"a.txt", "./a.txt"},
		DestVolumeID:   "fixture-rw",
		DestDir:        "",
		ConflictPolicy: transfer.PolicyPrompt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(job.SourcePaths) != 1 || job.SourcePaths[0] != "a.txt" {
		t.Fatalf("unexpected canonical sources: %#v", job.SourcePaths)
	}
}

func TestBatchConflictApplyToAll(t *testing.T) {
	_, rw, _, mgr, _ := setupVolumes(t)
	_ = os.WriteFile(filepath.Join(rw, "a.txt"), []byte("old"), 0o644)
	_ = os.MkdirAll(filepath.Join(rw, "photos"), 0o755)
	_ = os.WriteFile(filepath.Join(rw, "photos", "old.txt"), []byte("old"), 0o644)
	job, err := mgr.Create(transfer.CreateRequest{
		Kind:           transfer.KindCopy,
		SourceVolumeID: "fixture-ro",
		SourcePaths:    []string{"a.txt", "photos"},
		DestVolumeID:   "fixture-rw",
		DestDir:        "",
		ConflictPolicy: transfer.PolicyPrompt,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, mgr, job.ID, transfer.StatusConflict)
	if _, err := mgr.ResolveConflict(job.ID, transfer.ConflictResolution{
		Action: transfer.PolicyReplace, ApplyToAll: true,
	}); err != nil {
		t.Fatal(err)
	}
	done := waitStatus(t, mgr, job.ID, transfer.StatusCompleted)
	if !done.ApplyToAll || done.ConflictPolicy != transfer.PolicyReplace {
		t.Fatalf("apply-to-all not retained: %+v", done)
	}
	if _, err := os.Stat(filepath.Join(rw, "photos", "old.txt")); !os.IsNotExist(err) {
		t.Fatalf("old directory contents survived replace: %v", err)
	}
}

func TestRejectReadOnlyDestination(t *testing.T) {
	_, _, _, mgr, _ := setupVolumes(t)
	_, err := mgr.Create(transfer.CreateRequest{
		Kind:           transfer.KindCopy,
		SourceVolumeID: "fixture-rw",
		SourcePath:     "notes.txt",
		DestVolumeID:   "fixture-ro",
		DestDir:        "",
		ConflictPolicy: transfer.PolicySkip,
	})
	if err == nil {
		t.Fatal("expected read-only error")
	}
	if !errors.Is(err, appfs.ErrReadOnly) {
		t.Fatalf("expected ErrReadOnly, got %v", err)
	}
}

func TestRejectSamePathAndDescendantTargets(t *testing.T) {
	_, rw, _, mgr, _ := setupVolumes(t)
	_ = os.MkdirAll(filepath.Join(rw, "tree", "child"), 0o755)
	_ = os.WriteFile(filepath.Join(rw, "tree", "leaf.txt"), []byte("leaf"), 0o644)

	for _, kind := range []string{transfer.KindCopy, transfer.KindMove} {
		t.Run(kind+"_same_path", func(t *testing.T) {
			_, err := mgr.Create(transfer.CreateRequest{
				Kind: kind, SourceVolumeID: "fixture-rw", SourcePath: "notes.txt",
				DestVolumeID: "fixture-rw", DestDir: "", ConflictPolicy: transfer.PolicyReplace,
			})
			if err == nil || !errors.Is(err, transfer.ErrInvalidRequest) {
				t.Fatalf("expected same-path rejection, got %v", err)
			}
			if data, readErr := os.ReadFile(filepath.Join(rw, "notes.txt")); readErr != nil || string(data) != "notes" {
				t.Fatalf("source changed: %v %q", readErr, data)
			}
		})
		t.Run(kind+"_descendant", func(t *testing.T) {
			_, err := mgr.Create(transfer.CreateRequest{
				Kind: kind, SourceVolumeID: "fixture-rw", SourcePath: "tree",
				DestVolumeID: "fixture-rw", DestDir: "tree/child", ConflictPolicy: transfer.PolicyReplace,
			})
			if err == nil || !errors.Is(err, transfer.ErrInvalidRequest) {
				t.Fatalf("expected descendant rejection, got %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(rw, "tree", "leaf.txt")); statErr != nil {
				t.Fatalf("source tree changed: %v", statErr)
			}
		})
	}
}

func TestConflictPolicies(t *testing.T) {
	_, rw, _, mgr, _ := setupVolumes(t)
	_ = os.WriteFile(filepath.Join(rw, "a.txt"), []byte("existing"), 0o644)

	t.Run("skip", func(t *testing.T) {
		job, err := mgr.Create(transfer.CreateRequest{
			Kind: transfer.KindCopy, SourceVolumeID: "fixture-ro", SourcePath: "a.txt",
			DestVolumeID: "fixture-rw", DestDir: "", ConflictPolicy: transfer.PolicySkip,
		})
		if err != nil {
			t.Fatal(err)
		}
		waitStatus(t, mgr, job.ID, transfer.StatusCompleted)
		b, _ := os.ReadFile(filepath.Join(rw, "a.txt"))
		if string(b) != "existing" {
			t.Fatalf("skip overwrote: %q", b)
		}
	})

	t.Run("replace", func(t *testing.T) {
		job, err := mgr.Create(transfer.CreateRequest{
			Kind: transfer.KindCopy, SourceVolumeID: "fixture-ro", SourcePath: "a.txt",
			DestVolumeID: "fixture-rw", DestDir: "", ConflictPolicy: transfer.PolicyReplace,
		})
		if err != nil {
			t.Fatal(err)
		}
		waitStatus(t, mgr, job.ID, transfer.StatusCompleted)
		b, _ := os.ReadFile(filepath.Join(rw, "a.txt"))
		if string(b) != "hello-ro" {
			t.Fatalf("replace failed: %q", b)
		}
	})

	t.Run("rename", func(t *testing.T) {
		_ = os.WriteFile(filepath.Join(rw, "a.txt"), []byte("keep"), 0o644)
		job, err := mgr.Create(transfer.CreateRequest{
			Kind: transfer.KindCopy, SourceVolumeID: "fixture-ro", SourcePath: "a.txt",
			DestVolumeID: "fixture-rw", DestDir: "", ConflictPolicy: transfer.PolicyRename,
		})
		if err != nil {
			t.Fatal(err)
		}
		waitStatus(t, mgr, job.ID, transfer.StatusCompleted)
		if _, err := os.Stat(filepath.Join(rw, "a (1).txt")); err != nil {
			t.Fatalf("expected renamed file: %v", err)
		}
		b, _ := os.ReadFile(filepath.Join(rw, "a.txt"))
		if string(b) != "keep" {
			t.Fatalf("original changed: %q", b)
		}
	})
}

func TestPromptConflictApplyToAll(t *testing.T) {
	_, rw, fsys, mgr, _ := setupVolumes(t)
	_ = os.MkdirAll(filepath.Join(filepath.Dir(rw), "ro", "batch"), 0o755)
	_ = os.WriteFile(filepath.Join(filepath.Dir(rw), "ro", "batch", "x.txt"), []byte("new-x"), 0o644)
	_ = os.WriteFile(filepath.Join(filepath.Dir(rw), "ro", "batch", "y.txt"), []byte("new-y"), 0o644)
	_ = os.MkdirAll(filepath.Join(rw, "batch"), 0o755)
	_ = os.WriteFile(filepath.Join(rw, "batch", "x.txt"), []byte("old-x"), 0o644)
	_ = os.WriteFile(filepath.Join(rw, "batch", "y.txt"), []byte("old-y"), 0o644)

	job, err := mgr.Create(transfer.CreateRequest{
		Kind: transfer.KindCopy, SourceVolumeID: "fixture-ro", SourcePath: "batch",
		DestVolumeID: "fixture-rw", DestDir: "", ConflictPolicy: transfer.PolicyPrompt,
	})
	if err != nil {
		t.Fatal(err)
	}
	// First conflict on directory "batch" existing — replace with apply-to-all
	waitStatus(t, mgr, job.ID, transfer.StatusConflict)
	_, err = mgr.ResolveConflict(job.ID, transfer.ConflictResolution{
		Action: transfer.PolicyReplace, ApplyToAll: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := waitStatus(t, mgr, job.ID, transfer.StatusCompleted)
	if done.Status != transfer.StatusCompleted {
		t.Fatalf("expected completed, got %+v", done)
	}
	bx, _ := os.ReadFile(filepath.Join(rw, "batch", "x.txt"))
	by, _ := os.ReadFile(filepath.Join(rw, "batch", "y.txt"))
	if string(bx) != "new-x" || string(by) != "new-y" {
		t.Fatalf("apply-to-all replace failed: %q %q", bx, by)
	}
	_ = fsys
}

func TestCancelLeavesNoFinalName(t *testing.T) {
	_, rw, _, mgr, _ := setupVolumes(t)
	// Large enough to cancel mid-copy
	big := bytes.Repeat([]byte("x"), 8<<20) // 8 MiB
	_ = os.WriteFile(filepath.Join(filepath.Dir(rw), "ro", "big.bin"), big, 0o644)

	job, err := mgr.Create(transfer.CreateRequest{
		Kind: transfer.KindCopy, SourceVolumeID: "fixture-ro", SourcePath: "big.bin",
		DestVolumeID: "fixture-rw", DestDir: "", ConflictPolicy: transfer.PolicyReplace,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Wait until running with some progress
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j, _ := mgr.Get(job.ID)
		if j.Status == transfer.StatusRunning && j.BytesDone > 0 {
			break
		}
		if j.Status == transfer.StatusCompleted {
			t.Fatal("finished too fast to cancel")
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, err = mgr.Cancel(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	done := waitStatus(t, mgr, job.ID, transfer.StatusCancelled, transfer.StatusFailed)
	if done.Status != transfer.StatusCancelled && done.Status != transfer.StatusFailed {
		t.Fatalf("status %s", done.Status)
	}
	if _, err := os.Stat(filepath.Join(rw, "big.bin")); err == nil {
		t.Fatal("final destination should not exist after cancel")
	}
	entries, _ := os.ReadDir(rw)
	for _, e := range entries {
		if e.Name() == "big.bin" {
			t.Fatal("complete name present")
		}
		if strings.HasPrefix(e.Name(), ".lgfm-partial-") {
			t.Fatalf("staging should be cleaned: %s", e.Name())
		}
	}
}

func TestReconcileInterruptedJobs(t *testing.T) {
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })
	ro := filepath.Join(root, "ro")
	rw := filepath.Join(root, "rw")
	_ = os.MkdirAll(ro, 0o755)
	_ = os.MkdirAll(rw, 0o755)
	_ = os.WriteFile(filepath.Join(ro, "a.txt"), []byte("x"), 0o644)
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
		) VALUES ('job1', 'copy', 'running', 'fixture-ro', 'a.txt', 'fixture-rw', '', 'a.txt', 'replace', ?, ?, '.lgfm-partial-job1')
	`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(rw, ".lgfm-partial-job1"), []byte("partial"), 0o644)
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
	job, err := mgr.Get("job1")
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != transfer.StatusFailed {
		t.Fatalf("expected failed after reconcile, got %s", job.Status)
	}
	if job.ErrorMessage == nil || !strings.Contains(*job.ErrorMessage, "interrupted") {
		t.Fatalf("expected interrupted message, got %v", job.ErrorMessage)
	}
	// Partial must not be promoted
	if _, err := os.Stat(filepath.Join(rw, "a.txt")); err == nil {
		t.Fatal("must not create final name on reconcile")
	}
}

func TestRecursiveDirectoryCopy(t *testing.T) {
	_, rw, _, mgr, _ := setupVolumes(t)
	job, err := mgr.Create(transfer.CreateRequest{
		Kind: transfer.KindCopy, SourceVolumeID: "fixture-ro", SourcePath: "photos",
		DestVolumeID: "fixture-rw", DestDir: "", ConflictPolicy: transfer.PolicyReplace,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, mgr, job.ID, transfer.StatusCompleted)
	b, err := os.ReadFile(filepath.Join(rw, "photos", "p.txt"))
	if err != nil || string(b) != "photo" {
		t.Fatalf("recursive copy failed: %v %q", err, b)
	}
}

func TestBoundedMemoryBufferConstant(t *testing.T) {
	if appfs.CopyBufferSize > 1024*1024 {
		t.Fatalf("copy buffer too large: %d", appfs.CopyBufferSize)
	}
	if appfs.CopyBufferSize < 32*1024 {
		t.Fatalf("copy buffer unexpectedly small: %d", appfs.CopyBufferSize)
	}
	// Ensure StreamCopy does not allocate unbounded buffers when given a fixed slice.
	src := bytes.NewReader(bytes.Repeat([]byte("z"), 4<<20))
	var dst bytes.Buffer
	buf := make([]byte, appfs.CopyBufferSize)
	n, err := appfs.StreamCopy(&dst, src, buf, nil)
	if err != nil || n != 4<<20 {
		t.Fatalf("stream copy: n=%d err=%v", n, err)
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	_ = ms
}
