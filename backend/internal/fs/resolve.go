package fs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liquid-glass-file-manager/backend/internal/volumes"
	"golang.org/x/sys/unix"
)

var (
	ErrInvalidPath   = errors.New("invalid path")
	ErrNotFound      = errors.New("not found")
	ErrEscape        = errors.New("path escapes volume root")
	ErrIsDirectory   = errors.New("is a directory")
	ErrNotDirectory  = errors.New("not a directory")
	ErrReadOnly      = errors.New("volume is read-only")
	ErrSymlinkFollow = errors.New("symlink traversal is not allowed")
	ErrExists        = errors.New("already exists")
	ErrNotFile       = errors.New("not a regular file")
	ErrUnavailable   = errors.New("volume unavailable")
	ErrChanged       = errors.New("file changed since it was opened")
	ErrTooLarge      = errors.New("file exceeds size limit")
	ErrInvalidText   = errors.New("file is not valid UTF-8 text")
	ErrMountBoundary = errors.New("refusing to cross filesystem boundary")
)

// MaxListEntries caps directory listings to protect memory on huge NAS trees.
// When truncated, ListResult.Truncated is true. Tests may lower this.
var MaxListEntries = 10000

// Entry is a directory listing entry (public metadata only).
type Entry struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	IsDir     bool      `json:"isDir"`
	IsSymlink bool      `json:"isSymlink"`
	Size      int64     `json:"size"`
	ModTime   time.Time `json:"modTime"`
	Mode      string    `json:"mode"`
}

// Meta is file/directory metadata.
type Meta struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	IsDir     bool      `json:"isDir"`
	IsSymlink bool      `json:"isSymlink"`
	Size      int64     `json:"size"`
	ModTime   time.Time `json:"modTime"`
	Mode      string    `json:"mode"`
}

// Resolved is an open path beneath a volume root.
type Resolved struct {
	Volume  volumes.Volume
	RelPath string
	AbsPath string // internal only — never serialize to clients
	Info    os.FileInfo
	File    *os.File
}

// Close closes the underlying file descriptor.
func (r *Resolved) Close() error {
	if r.File != nil {
		return r.File.Close()
	}
	return nil
}

// Service resolves paths safely within configured volumes.
type Service struct {
	reg *volumes.Registry
}

// New creates a filesystem service.
func New(reg *volumes.Registry) *Service {
	return &Service{reg: reg}
}

// Volume returns a configured volume by ID.
func (s *Service) Volume(id string) (volumes.Volume, bool) {
	return s.reg.Get(id)
}

// Available reports whether the volume root is currently reachable.
func (s *Service) Available(volumeID string) bool {
	vol, ok := s.reg.Get(volumeID)
	if !ok {
		return false
	}
	info, err := os.Stat(vol.Path)
	return err == nil && info.IsDir()
}

// Resolve opens a path relative to a volume without following symlinks for traversal.
func (s *Service) Resolve(volumeID, rel string) (*Resolved, error) {
	vol, ok := s.reg.Get(volumeID)
	if !ok {
		return nil, ErrNotFound
	}
	rel, err := normalizeRel(rel)
	if err != nil {
		return nil, err
	}

	root, err := os.Open(vol.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrUnavailable
		}
		// Common when a NAS share disconnects after startup.
		if errors.Is(err, os.ErrPermission) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer root.Close()

	rootInfo, err := root.Stat()
	if err != nil {
		return nil, err
	}
	if !rootInfo.IsDir() {
		return nil, ErrNotDirectory
	}

	if rel == "." || rel == "" {
		f, err := os.Open(vol.Path)
		if err != nil {
			return nil, err
		}
		info, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return nil, err
		}
		return &Resolved{Volume: vol, RelPath: "", AbsPath: vol.Path, Info: info, File: f}, nil
	}

	parts := strings.Split(rel, "/")
	currentAbs := vol.Path
	for i, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return nil, ErrEscape
		}
		if strings.ContainsRune(part, 0) {
			return nil, ErrInvalidPath
		}
		next := filepath.Join(currentAbs, part)
		if !beneath(vol.Path, next) {
			return nil, ErrEscape
		}
		info, err := os.Lstat(next)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, ErrNotFound
			}
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if i < len(parts)-1 {
				return nil, ErrSymlinkFollow
			}
			return &Resolved{
				Volume:  vol,
				RelPath: rel,
				AbsPath: next,
				Info:    info,
				File:    nil,
			}, nil
		}
		currentAbs = next
	}

	f, err := os.Open(currentAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	real, err := filepath.EvalSymlinks(currentAbs)
	if err == nil {
		rootReal, rerr := filepath.EvalSymlinks(vol.Path)
		if rerr == nil && !beneath(rootReal, real) {
			_ = f.Close()
			return nil, ErrEscape
		}
	}
	return &Resolved{Volume: vol, RelPath: rel, AbsPath: currentAbs, Info: info, File: f}, nil
}

// ListResult is a directory listing that may be truncated for huge trees.
type ListResult struct {
	Entries   []Entry `json:"entries"`
	Truncated bool    `json:"truncated"`
}

// List lists a directory. Symlink entries are included but not followed.
// Results are capped at MaxListEntries (truncated=true when more exist).
func (s *Service) List(volumeID, rel string) ([]Entry, error) {
	result, err := s.ListDetailed(volumeID, rel)
	if err != nil {
		return nil, err
	}
	return result.Entries, nil
}

// ListDetailed is List with truncation metadata for large directories.
func (s *Service) ListDetailed(volumeID, rel string) (*ListResult, error) {
	return s.listDirectory(volumeID, rel, true, MaxListEntries)
}

// ListForTransfer returns a complete, unfiltered directory listing for byte-level
// operations. Display preferences and UI listing limits must never alter what a
// copy or move transfers.
func (s *Service) ListForTransfer(volumeID, rel string) ([]Entry, error) {
	result, err := s.listDirectory(volumeID, rel, false, 0)
	if err != nil {
		return nil, err
	}
	return result.Entries, nil
}

func (s *Service) listDirectory(volumeID, rel string, displayRules bool, limit int) (*ListResult, error) {
	res, err := s.Resolve(volumeID, rel)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	if !res.Info.IsDir() {
		return nil, ErrNotDirectory
	}
	if res.File == nil {
		return nil, ErrNotDirectory
	}
	names, err := res.File.Readdirnames(-1)
	if err != nil {
		// I/O errors on slow/disconnected NAS surfaces as unavailable when possible.
		if os.IsNotExist(err) {
			return nil, ErrUnavailable
		}
		return nil, err
	}
	capacity := len(names)
	if limit > 0 {
		capacity = min(capacity, limit)
	}
	entries := make([]Entry, 0, capacity)
	truncated := false
	for _, name := range names {
		if displayRules && !res.Volume.ShowHiddenFiles && strings.HasPrefix(name, ".") {
			continue
		}
		if limit > 0 && len(entries) >= limit {
			truncated = true
			break
		}
		childRel := name
		if res.RelPath != "" {
			childRel = res.RelPath + "/" + name
		}
		childPath := filepath.Join(res.AbsPath, name)
		info, err := os.Lstat(childPath)
		if err != nil {
			if displayRules {
				continue
			}
			return nil, fmt.Errorf("inspect transfer entry %q: %w", childRel, err)
		}
		isLink := info.Mode()&os.ModeSymlink != 0
		isDir := info.IsDir()
		size := info.Size()
		if isLink {
			isDir = false
		}
		entries = append(entries, Entry{
			Name:      name,
			Path:      childRel,
			IsDir:     isDir && !isLink,
			IsSymlink: isLink,
			Size:      size,
			ModTime:   info.ModTime().UTC(),
			Mode:      info.Mode().String(),
		})
	}
	return &ListResult{Entries: entries, Truncated: truncated}, nil
}

// Stat returns metadata for a path.
func (s *Service) Stat(volumeID, rel string) (*Meta, error) {
	res, err := s.Resolve(volumeID, rel)
	if err != nil {
		return nil, err
	}
	defer res.Close()
	name := filepath.Base(res.AbsPath)
	if res.RelPath == "" {
		name = res.Volume.Name
	}
	isLink := res.Info.Mode()&os.ModeSymlink != 0
	return &Meta{
		Name:      name,
		Path:      res.RelPath,
		IsDir:     res.Info.IsDir() && !isLink,
		IsSymlink: isLink,
		Size:      res.Info.Size(),
		ModTime:   res.Info.ModTime().UTC(),
		Mode:      res.Info.Mode().String(),
	}, nil
}

// OpenFile opens a regular file for reading (streaming).
func (s *Service) OpenFile(volumeID, rel string) (*Resolved, error) {
	res, err := s.Resolve(volumeID, rel)
	if err != nil {
		return nil, err
	}
	if res.Info.Mode()&os.ModeSymlink != 0 {
		_ = res.Close()
		return nil, ErrSymlinkFollow
	}
	if res.Info.IsDir() {
		_ = res.Close()
		return nil, ErrIsDirectory
	}
	return res, nil
}

// Exists reports whether a relative path exists (symlink leaf counts as exists).
func (s *Service) Exists(volumeID, rel string) (bool, error) {
	_, err := s.Resolve(volumeID, rel)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return false, err
}

// JoinRel joins a parent relative path and a single name component.
func JoinRel(parent, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return "", ErrInvalidPath
	}
	parent, err := normalizeRel(parent)
	if err != nil {
		return "", err
	}
	if parent == "" {
		return name, nil
	}
	return parent + "/" + name, nil
}

// EnsureWritableVolume returns the volume or ErrReadOnly / ErrNotFound.
func (s *Service) EnsureWritableVolume(volumeID string) (volumes.Volume, error) {
	vol, ok := s.reg.Get(volumeID)
	if !ok {
		return volumes.Volume{}, ErrNotFound
	}
	if err := EnsureReadWrite(vol); err != nil {
		return volumes.Volume{}, err
	}
	return vol, nil
}

// CreateFile creates a new regular file under parentRel with the given name.
// If exclusive is true, fails if the name already exists.
func (s *Service) CreateFile(volumeID, parentRel, name string, exclusive bool) (*Resolved, error) {
	vol, err := s.EnsureWritableVolume(volumeID)
	if err != nil {
		return nil, err
	}
	parent, err := s.Resolve(volumeID, parentRel)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	if !parent.Info.IsDir() || parent.File == nil {
		return nil, ErrNotDirectory
	}
	rel, err := JoinRel(parent.RelPath, name)
	if err != nil {
		return nil, err
	}
	abs := filepath.Join(parent.AbsPath, name)
	if !beneath(vol.Path, abs) {
		return nil, ErrEscape
	}
	flags := os.O_WRONLY | os.O_CREATE
	if exclusive {
		flags |= os.O_EXCL
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(abs, flags, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return nil, ErrExists
		}
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		_ = os.Remove(abs)
		return nil, err
	}
	return &Resolved{Volume: vol, RelPath: rel, AbsPath: abs, Info: info, File: f}, nil
}

// Mkdir creates a directory under parentRel.
func (s *Service) Mkdir(volumeID, parentRel, name string) (string, error) {
	vol, err := s.EnsureWritableVolume(volumeID)
	if err != nil {
		return "", err
	}
	parent, err := s.Resolve(volumeID, parentRel)
	if err != nil {
		return "", err
	}
	defer parent.Close()
	if !parent.Info.IsDir() || parent.File == nil {
		return "", ErrNotDirectory
	}
	rel, err := JoinRel(parent.RelPath, name)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(parent.AbsPath, name)
	if !beneath(vol.Path, abs) {
		return "", ErrEscape
	}
	if err := os.Mkdir(abs, 0o755); err != nil {
		if os.IsExist(err) {
			// Existing directory is OK for recursive copy.
			info, lerr := os.Lstat(abs)
			if lerr != nil {
				return "", lerr
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return "", ErrExists
			}
			return rel, nil
		}
		return "", err
	}
	return rel, nil
}

// Remove deletes a non-directory path (file or empty staging file). Directories
// must be removed with RemoveAllRel after contents are cleared.
func (s *Service) Remove(volumeID, rel string) error {
	if _, err := s.EnsureWritableVolume(volumeID); err != nil {
		return err
	}
	res, err := s.Resolve(volumeID, rel)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	defer res.Close()
	if res.Info.IsDir() && res.Info.Mode()&os.ModeSymlink == 0 {
		return ErrIsDirectory
	}
	return os.Remove(res.AbsPath)
}

// RemoveAllRel removes a file or directory tree beneath the volume after path validation.
func (s *Service) RemoveAllRel(volumeID, rel string) error {
	err := s.DeleteEntry(volumeID, rel)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// RenameWithin moves/renames within the same volume without crossing roots.
func (s *Service) RenameWithin(volumeID, oldRel, newRel string) error {
	vol, err := s.EnsureWritableVolume(volumeID)
	if err != nil {
		return err
	}
	oldRes, err := s.Resolve(volumeID, oldRel)
	if err != nil {
		return err
	}
	oldAbs := oldRes.AbsPath
	_ = oldRes.Close()

	newRelNorm, err := normalizeRel(newRel)
	if err != nil {
		return err
	}
	if newRelNorm == "" {
		return ErrInvalidPath
	}
	// Ensure parent of new path exists and stays under root.
	parentRel := filepath.Dir(newRelNorm)
	if parentRel == "." {
		parentRel = ""
	}
	parent, err := s.Resolve(volumeID, parentRel)
	if err != nil {
		return err
	}
	_ = parent.Close()
	newAbs := filepath.Join(vol.Path, filepath.FromSlash(newRelNorm))
	if !beneath(vol.Path, newAbs) {
		return ErrEscape
	}
	return os.Rename(oldAbs, newAbs)
}

// FreeSpace returns available bytes for a volume when known.
func (s *Service) FreeSpace(volumeID string) (available int64, known bool, err error) {
	vol, ok := s.reg.Get(volumeID)
	if !ok {
		return 0, false, ErrNotFound
	}
	var st unix.Statfs_t
	if err := unix.Statfs(vol.Path, &st); err != nil {
		// Unknown free space must not be treated as failure of the caller’s job.
		return 0, false, nil
	}
	if st.Bsize <= 0 || st.Bavail == 0 && st.Blocks == 0 {
		return 0, false, nil
	}
	// Guard against overflow / nonsense values from some virtualized mounts.
	const maxReasonable int64 = 1 << 60 // 1 EiB ceiling
	if st.Bavail > uint64(maxReasonable)/uint64(st.Bsize) {
		return 0, false, nil
	}
	avail := int64(st.Bavail) * int64(st.Bsize)
	if avail < 0 {
		return 0, false, nil
	}
	return avail, true, nil
}

// EnsureReadWrite returns ErrReadOnly if the volume cannot be written.
func EnsureReadWrite(vol volumes.Volume) error {
	if vol.ReadOnly {
		return ErrReadOnly
	}
	return nil
}

func normalizeRel(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" || rel == "." || rel == "/" {
		return "", nil
	}
	if strings.ContainsRune(rel, 0) {
		return "", ErrInvalidPath
	}
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || strings.Contains(rel, `\`) {
		return "", ErrInvalidPath
	}
	clean := filepath.ToSlash(filepath.Clean(rel))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", ErrEscape
	}
	for _, p := range strings.Split(clean, "/") {
		if p == ".." {
			return "", ErrEscape
		}
		if p == "" {
			return "", ErrInvalidPath
		}
	}
	return clean, nil
}

func beneath(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if root == path {
		return true
	}
	sep := string(os.PathSeparator)
	return strings.HasPrefix(path, root+sep)
}

// CopyBufferSize is the bounded buffer used by transfer streaming.
const CopyBufferSize = 256 * 1024

// StreamCopy copies src to dst using a bounded buffer and optional progress callback.
func StreamCopy(dst io.Writer, src io.Reader, buf []byte, onProgress func(n int) error) (int64, error) {
	if len(buf) == 0 {
		buf = make([]byte, CopyBufferSize)
	}
	var written int64
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw < 0 || nr < nw {
				nw = 0
				if ew == nil {
					ew = errors.New("invalid write result")
				}
			}
			written += int64(nw)
			if onProgress != nil {
				if err := onProgress(nw); err != nil {
					return written, err
				}
			}
			if ew != nil {
				return written, ew
			}
			if nr != nw {
				return written, io.ErrShortWrite
			}
		}
		if er != nil {
			if er == io.EOF {
				return written, nil
			}
			return written, er
		}
	}
}
