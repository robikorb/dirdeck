package fs_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	appfs "github.com/robikorb/dirdeck/backend/internal/fs"
	"github.com/robikorb/dirdeck/backend/internal/volumes"
)

func TestListTruncationAndUnavailable(t *testing.T) {
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })

	vol := filepath.Join(root, "big")
	_ = os.MkdirAll(vol, 0o755)
	const n = 50
	for i := 0; i < n; i++ {
		_ = os.WriteFile(filepath.Join(vol, fmt.Sprintf("file-%d.txt", i)), []byte("x"), 0o644)
	}

	cfg := filepath.Join(root, "volumes.yaml")
	_ = os.WriteFile(cfg, []byte(`
volumes:
  - id: big
    name: Big
    path: `+vol+`
    readOnly: true
    showHiddenFiles: false
    thumbnails: false
`), 0o600)
	reg, err := volumes.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	svc := appfs.New(reg)
	detailed, err := svc.ListDetailed("big", "")
	if err != nil {
		t.Fatal(err)
	}
	if detailed.Truncated {
		t.Fatal("unexpected truncation for small dir")
	}
	if len(detailed.Entries) < n {
		t.Fatalf("got %d entries", len(detailed.Entries))
	}

	if err := os.RemoveAll(vol); err != nil {
		t.Fatal(err)
	}
	if svc.Available("big") {
		t.Fatal("expected unavailable")
	}
	_, err = svc.ListDetailed("big", "")
	if err != appfs.ErrUnavailable {
		t.Fatalf("got %v", err)
	}
}

func TestListTruncatedFlag(t *testing.T) {
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })

	prev := appfs.MaxListEntries
	appfs.MaxListEntries = 20
	t.Cleanup(func() { appfs.MaxListEntries = prev })

	vol := filepath.Join(root, "huge")
	_ = os.MkdirAll(vol, 0o755)
	for i := 0; i < 25; i++ {
		_ = os.WriteFile(filepath.Join(vol, fmt.Sprintf("e-%05d.txt", i)), []byte("x"), 0o644)
	}
	cfg := filepath.Join(root, "volumes.yaml")
	_ = os.WriteFile(cfg, []byte(`
volumes:
  - id: huge
    name: Huge
    path: `+vol+`
    readOnly: true
    showHiddenFiles: false
    thumbnails: false
`), 0o600)
	reg, err := volumes.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	detailed, err := appfs.New(reg).ListDetailed("huge", "")
	if err != nil {
		t.Fatal(err)
	}
	if !detailed.Truncated {
		t.Fatal("expected truncated")
	}
	if len(detailed.Entries) != 20 {
		t.Fatalf("got %d entries", len(detailed.Entries))
	}
}
