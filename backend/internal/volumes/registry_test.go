package volumes_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/robikorb/dirdeck/backend/internal/volumes"
)

func TestLoadRejectsOverlappingAndInvalid(t *testing.T) {
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })

	a := filepath.Join(root, "a")
	b := filepath.Join(root, "a", "nested")
	c := filepath.Join(root, "c")
	mustMkdir(t, a)
	mustMkdir(t, b)
	mustMkdir(t, c)

	writeYAML(t, filepath.Join(root, "ok.yaml"), `
volumes:
  - id: one
    name: One
    path: `+a+`
    readOnly: true
    showHiddenFiles: false
    thumbnails: false
  - id: two
    name: Two
    path: `+c+`
    readOnly: false
    showHiddenFiles: false
    thumbnails: false
`)
	reg, err := volumes.Load(filepath.Join(root, "ok.yaml"))
	if err != nil {
		t.Fatalf("expected ok load: %v", err)
	}
	pub := reg.Public()
	if len(pub) != 2 {
		t.Fatalf("expected 2 volumes, got %d", len(pub))
	}
	if pub[0].Path != "" {
		t.Fatalf("public volume must not expose path, got %q", pub[0].Path)
	}

	writeYAML(t, filepath.Join(root, "overlap.yaml"), `
volumes:
  - id: one
    name: One
    path: `+a+`
    readOnly: true
    showHiddenFiles: false
    thumbnails: false
  - id: nested
    name: Nested
    path: `+b+`
    readOnly: false
    showHiddenFiles: false
    thumbnails: false
`)
	if _, err := volumes.Load(filepath.Join(root, "overlap.yaml")); err == nil {
		t.Fatal("expected overlapping roots to fail")
	}

	writeYAML(t, filepath.Join(root, "dup.yaml"), `
volumes:
  - id: one
    name: One
    path: `+a+`
    readOnly: true
    showHiddenFiles: false
    thumbnails: false
  - id: one
    name: Also One
    path: `+c+`
    readOnly: false
    showHiddenFiles: false
    thumbnails: false
`)
	if _, err := volumes.Load(filepath.Join(root, "dup.yaml")); err == nil {
		t.Fatal("expected duplicate id to fail")
	}

	outside := filepath.Join(t.TempDir(), "outside")
	mustMkdir(t, outside)
	writeYAML(t, filepath.Join(root, "outside.yaml"), `
volumes:
  - id: bad
    name: Bad
    path: `+outside+`
    readOnly: true
    showHiddenFiles: false
    thumbnails: false
`)
	if _, err := volumes.Load(filepath.Join(root, "outside.yaml")); err == nil {
		t.Fatal("expected path outside VolumesRoot to fail")
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeYAML(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverDefaultsToReadOnly(t *testing.T) {
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })

	for _, name := range []string{"media", "docs", "my-photos"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Hidden entries and plain files are not volumes.
	if err := os.MkdirAll(filepath.Join(root, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "stray.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	reg, err := volumes.Discover(nil)
	if err != nil {
		t.Fatal(err)
	}
	got := reg.Public()
	if len(got) != 3 {
		t.Fatalf("expected 3 discovered volumes, got %d: %+v", len(got), got)
	}
	for _, v := range got {
		if !v.ReadOnly {
			t.Fatalf("discovered volume %q must default to read-only", v.ID)
		}
	}
	if _, ok := reg.Get("my-photos"); !ok {
		t.Fatal("expected my-photos to be discovered")
	}
	if v, _ := reg.Get("my-photos"); v.Name != "My photos" {
		t.Fatalf("unexpected display name: %q", v.Name)
	}
}

func TestDiscoverWritableOptIn(t *testing.T) {
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })
	for _, name := range []string{"media", "docs"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	reg, err := volumes.Discover([]string{"media"})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := reg.Get("media"); v.ReadOnly {
		t.Fatal("media was opted in and must be writable")
	}
	if v, _ := reg.Get("docs"); !v.ReadOnly {
		t.Fatal("docs was not opted in and must stay read-only")
	}

	all, err := volumes.Discover([]string{"*"})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range all.Public() {
		if v.ReadOnly {
			t.Fatalf("wildcard opt-in left %q read-only", v.ID)
		}
	}
}

func TestDiscoverRejectsEmptyRoot(t *testing.T) {
	root := t.TempDir()
	volumes.VolumesRoot = root
	t.Cleanup(func() { volumes.VolumesRoot = "/mnt/volumes" })
	if _, err := volumes.Discover(nil); err == nil {
		t.Fatal("expected an actionable error when nothing is mounted")
	}
}
