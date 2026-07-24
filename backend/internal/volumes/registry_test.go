package volumes_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liquid-glass-file-manager/backend/internal/volumes"
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
