package volumes

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// VolumesRoot is the required parent for all configured mount paths.
// Tests may override this.
var VolumesRoot = "/mnt/volumes"

// Volume is a configured storage root.
type Volume struct {
	ID              string `yaml:"id" json:"id"`
	Name            string `yaml:"name" json:"name"`
	Path            string `yaml:"path" json:"-"`
	ReadOnly        bool   `yaml:"readOnly" json:"readOnly"`
	ShowHiddenFiles bool   `yaml:"showHiddenFiles" json:"showHiddenFiles"`
	Thumbnails      bool   `yaml:"thumbnails" json:"thumbnails"`
	// Available is runtime-only (JSON). False when the mount root is unreachable.
	Available bool `yaml:"-" json:"available"`
}

type fileShape struct {
	Volumes []Volume `yaml:"volumes"`
}

// Registry is an immutable set of validated volumes.
type Registry struct {
	byID  map[string]Volume
	order []Volume
}

// Public returns browser-safe volume metadata (no container paths).
// Available is left false; callers should probe with ProbePublic.
func (r *Registry) Public() []Volume {
	out := make([]Volume, len(r.order))
	for i, v := range r.order {
		v.Path = ""
		v.Available = false
		out[i] = v
	}
	return out
}

// ProbePublic returns public metadata with live availability probes.
func (r *Registry) ProbePublic(probe func(id string) bool) []Volume {
	out := make([]Volume, len(r.order))
	for i, v := range r.order {
		id := v.ID
		v.Path = ""
		if probe != nil {
			v.Available = probe(id)
		}
		out[i] = v
	}
	return out
}

// Get returns a volume by ID.
func (r *Registry) Get(id string) (Volume, bool) {
	v, ok := r.byID[id]
	return v, ok
}

// Discover builds a registry from the directories mounted under VolumesRoot.
//
// This is the zero-configuration path: bind-mount a host directory to
// /mnt/volumes/<name> and it appears without writing a config file. Discovered
// volumes are read-only unless their id is listed in writable, matching the
// read-only-first procedure in docs/STORAGE-MOUNTS.md. Passing "*" makes every
// discovered volume writable.
func Discover(writable []string) (*Registry, error) {
	root := filepath.Clean(VolumesRoot)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", root, err)
	}

	allWritable := false
	writableSet := make(map[string]struct{}, len(writable))
	for _, id := range writable {
		id = strings.TrimSpace(id)
		if id == "*" {
			allWritable = true
			continue
		}
		if id != "" {
			writableSet[id] = struct{}{}
		}
	}

	reg := &Registry{byID: make(map[string]Volume), order: make([]Volume, 0, len(entries))}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(root, name)
		// Resolve so a symlinked mount still registers as a directory.
		info, err := os.Stat(full)
		if err != nil || !info.IsDir() {
			continue
		}
		vol := Volume{
			ID:              name,
			Name:            displayName(name),
			Path:            full,
			ReadOnly:        true,
			ShowHiddenFiles: false,
			Thumbnails:      true,
		}
		if allWritable {
			vol.ReadOnly = false
		} else if _, ok := writableSet[name]; ok {
			vol.ReadOnly = false
		}
		if err := validateVolume(vol); err != nil {
			return nil, fmt.Errorf("discovered volume %q: %w", name, err)
		}
		if _, exists := reg.byID[vol.ID]; exists {
			continue
		}
		reg.byID[vol.ID] = vol
		reg.order = append(reg.order, vol)
	}

	if len(reg.order) == 0 {
		return nil, fmt.Errorf("no directories found under %s: bind-mount at least one host folder there", root)
	}
	return reg, nil
}

// displayName turns a mount directory name into readable UI text.
func displayName(id string) string {
	cleaned := strings.NewReplacer("-", " ", "_", " ").Replace(id)
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return id
	}
	return strings.ToUpper(cleaned[:1]) + cleaned[1:]
}

// Load reads and validates a volumes YAML file.
func Load(path string) (*Registry, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read volumes file: %w", err)
	}
	var shape fileShape
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&shape); err != nil {
		return nil, fmt.Errorf("parse volumes file: %w", err)
	}
	if len(shape.Volumes) == 0 {
		return nil, fmt.Errorf("volumes file contains no volumes")
	}

	reg := &Registry{
		byID:  make(map[string]Volume, len(shape.Volumes)),
		order: make([]Volume, 0, len(shape.Volumes)),
	}
	roots := make([]string, 0, len(shape.Volumes))

	for i, v := range shape.Volumes {
		if err := validateVolume(v); err != nil {
			return nil, fmt.Errorf("volume[%d]: %w", i, err)
		}
		if _, exists := reg.byID[v.ID]; exists {
			return nil, fmt.Errorf("duplicate volume id %q", v.ID)
		}
		info, err := os.Stat(v.Path)
		if err != nil {
			return nil, fmt.Errorf("volume %q path %q: %w", v.ID, v.Path, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("volume %q path %q is not a directory", v.ID, v.Path)
		}
		clean := filepath.Clean(v.Path)
		for _, other := range roots {
			if overlaps(clean, other) {
				return nil, fmt.Errorf("volume %q path %q overlaps with %q", v.ID, clean, other)
			}
		}
		v.Path = clean
		reg.byID[v.ID] = v
		reg.order = append(reg.order, v)
		roots = append(roots, clean)
	}
	return reg, nil
}

func validateVolume(v Volume) error {
	if strings.TrimSpace(v.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if strings.ContainsAny(v.ID, "/\\") {
		return fmt.Errorf("id must not contain path separators")
	}
	if strings.TrimSpace(v.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if !filepath.IsAbs(v.Path) {
		return fmt.Errorf("path must be absolute")
	}
	clean := filepath.Clean(v.Path)
	if clean != v.Path && filepath.Clean(v.Path) != filepath.Clean(filepath.Clean(v.Path)) {
		// still use cleaned comparison below
	}
	volRoot := filepath.Clean(VolumesRoot)
	rel, err := filepath.Rel(volRoot, clean)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("path must be under %s", VolumesRoot)
	}
	if clean == volRoot {
		return fmt.Errorf("path must be a subdirectory of %s", VolumesRoot)
	}
	return nil
}

func overlaps(a, b string) bool {
	if a == b {
		return true
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(a+sep, b+sep) || strings.HasPrefix(b+sep, a+sep)
}
