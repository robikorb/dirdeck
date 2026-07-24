package fs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// RenameEntry renames one file or directory within its existing parent.
// It never replaces an existing destination and never follows symlinks.
func (s *Service) RenameEntry(volumeID, rel, newName string) (*Meta, error) {
	if _, err := s.EnsureWritableVolume(volumeID); err != nil {
		return nil, err
	}
	source, err := s.Resolve(volumeID, rel)
	if err != nil {
		return nil, err
	}
	defer source.Close()
	if source.RelPath == "" {
		return nil, ErrInvalidPath
	}
	if source.Info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrSymlinkFollow
	}

	parentRel := path.Dir(source.RelPath)
	if parentRel == "." {
		parentRel = ""
	}
	destRel, err := JoinRel(parentRel, newName)
	if err != nil {
		return nil, err
	}
	if destRel == source.RelPath {
		return nil, ErrInvalidPath
	}
	parent, err := s.Resolve(volumeID, parentRel)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	destAbs := filepath.Join(parent.AbsPath, filepath.Base(destRel))
	if !beneath(source.Volume.Path, destAbs) {
		return nil, ErrEscape
	}

	// Linux containers support renameat2 on normal bind mounts. Use NOREPLACE so
	// a concurrent create cannot turn a rename into an accidental overwrite.
	err = unix.Renameat2(unix.AT_FDCWD, source.AbsPath, unix.AT_FDCWD, destAbs, unix.RENAME_NOREPLACE)
	if err != nil {
		if errors.Is(err, syscall.EEXIST) {
			return nil, ErrExists
		}
		if !errors.Is(err, syscall.ENOSYS) && !errors.Is(err, syscall.EINVAL) &&
			!errors.Is(err, syscall.ENOTSUP) {
			return nil, err
		}
		// Conservative fallback for filesystems that do not implement renameat2.
		if _, statErr := os.Lstat(destAbs); statErr == nil {
			return nil, ErrExists
		} else if !os.IsNotExist(statErr) {
			return nil, statErr
		}
		if err := os.Rename(source.AbsPath, destAbs); err != nil {
			return nil, err
		}
	}
	return s.Stat(volumeID, destRel)
}

// DeleteEntry permanently removes one file, symlink, or directory tree.
// The volume root is never deletable. Directory traversal is descriptor-based,
// does not follow symlinks, and refuses to cross into another filesystem.
func (s *Service) DeleteEntry(volumeID, rel string) error {
	return s.DeleteEntries(volumeID, []string{rel})
}

// DeleteEntries permanently removes a bounded selection. Every requested path
// is validated before the first deletion starts. Descendants are collapsed
// when their parent directory is also selected.
func (s *Service) DeleteEntries(volumeID string, rels []string) error {
	if _, err := s.EnsureWritableVolume(volumeID); err != nil {
		return err
	}
	if len(rels) == 0 || len(rels) > 500 {
		return ErrInvalidPath
	}
	seen := make(map[string]struct{}, len(rels))
	normalized := make([]string, 0, len(rels))
	for _, rel := range rels {
		clean, err := normalizeRel(rel)
		if err != nil || clean == "" {
			return ErrInvalidPath
		}
		if _, exists := seen[clean]; exists {
			continue
		}
		resolved, err := s.Resolve(volumeID, clean)
		if err != nil {
			return err
		}
		_ = resolved.Close()
		seen[clean] = struct{}{}
		normalized = append(normalized, clean)
	}
	sort.Slice(normalized, func(i, j int) bool {
		return strings.Count(normalized[i], "/") < strings.Count(normalized[j], "/")
	})
	collapsed := make([]string, 0, len(normalized))
	for _, candidate := range normalized {
		covered := false
		for _, parent := range collapsed {
			if strings.HasPrefix(candidate, parent+"/") {
				covered = true
				break
			}
		}
		if !covered {
			collapsed = append(collapsed, candidate)
		}
	}
	for _, clean := range collapsed {
		if err := s.deleteEntryValidated(volumeID, clean); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) deleteEntryValidated(volumeID, normalized string) error {
	parentRel := path.Dir(normalized)
	if parentRel == "." {
		parentRel = ""
	}
	parent, err := s.Resolve(volumeID, parentRel)
	if err != nil {
		return err
	}
	defer parent.Close()
	if parent.File == nil || !parent.Info.IsDir() {
		return ErrNotDirectory
	}

	var parentStat unix.Stat_t
	if err := unix.Fstat(int(parent.File.Fd()), &parentStat); err != nil {
		return err
	}
	return removeTreeAt(int(parent.File.Fd()), path.Base(normalized), uint64(parentStat.Dev))
}

func removeTreeAt(parentFD int, name string, rootDev uint64) error {
	var stat unix.Stat_t
	if err := unix.Fstatat(parentFD, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return ErrNotFound
		}
		return err
	}

	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		if err := unix.Unlinkat(parentFD, name, 0); err != nil {
			if errors.Is(err, syscall.ENOENT) {
				return ErrNotFound
			}
			if errors.Is(err, syscall.EISDIR) {
				return ErrChanged
			}
			return err
		}
		return nil
	}
	if uint64(stat.Dev) != rootDev {
		return ErrMountBoundary
	}

	childFD, err := unix.Openat(
		parentFD,
		name,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return ErrNotFound
		}
		if errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.ENOTDIR) {
			return ErrChanged
		}
		return err
	}
	child := os.NewFile(uintptr(childFD), name)
	if child == nil {
		_ = unix.Close(childFD)
		return errors.New("could not open directory")
	}
	for {
		names, readErr := child.Readdirnames(128)
		for _, childName := range names {
			if err := removeTreeAt(childFD, childName, rootDev); err != nil {
				_ = child.Close()
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = child.Close()
			return readErr
		}
	}
	if err := child.Close(); err != nil {
		return err
	}
	if err := unix.Unlinkat(parentFD, name, unix.AT_REMOVEDIR); err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return ErrNotFound
		}
		if errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.ENOTDIR) {
			return ErrChanged
		}
		return err
	}
	return nil
}

// ReadTextFile returns a bounded raw UTF-8 file for the editor.
func (s *Service) ReadTextFile(volumeID, rel string, maxBytes int64) ([]byte, *Meta, error) {
	if maxBytes <= 0 {
		return nil, nil, ErrTooLarge
	}
	res, err := s.OpenFile(volumeID, rel)
	if err != nil {
		return nil, nil, err
	}
	defer res.Close()
	if !res.Info.Mode().IsRegular() {
		return nil, nil, ErrNotFile
	}
	if res.Info.Size() > maxBytes {
		return nil, nil, ErrTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(res.File, maxBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, nil, ErrTooLarge
	}
	if !utf8.Valid(data) {
		return nil, nil, ErrInvalidText
	}
	meta := metaFromResolved(res)
	return data, meta, nil
}

// WriteTextFile atomically replaces a regular UTF-8 file through a same-folder
// staging file. expectedModTime prevents silently overwriting a newer version.
func (s *Service) WriteTextFile(
	volumeID, rel string,
	data []byte,
	maxBytes int64,
	expectedModTime time.Time,
) (*Meta, error) {
	if _, err := s.EnsureWritableVolume(volumeID); err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, ErrTooLarge
	}
	if !utf8.Valid(data) {
		return nil, ErrInvalidText
	}
	target, err := s.Resolve(volumeID, rel)
	if err != nil {
		return nil, err
	}
	if target.RelPath == "" || target.Info.IsDir() || !target.Info.Mode().IsRegular() {
		_ = target.Close()
		return nil, ErrNotFile
	}
	if target.Info.Mode()&os.ModeSymlink != 0 {
		_ = target.Close()
		return nil, ErrSymlinkFollow
	}
	if !expectedModTime.IsZero() && !target.Info.ModTime().UTC().Equal(expectedModTime.UTC()) {
		_ = target.Close()
		return nil, ErrChanged
	}
	targetAbs := target.AbsPath
	mode := target.Info.Mode().Perm()
	parentAbs := filepath.Dir(targetAbs)
	parentRel := path.Dir(target.RelPath)
	if parentRel == "." {
		parentRel = ""
	}
	_ = target.Close()

	token := make([]byte, 8)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	tempName := ".lgfm-edit-" + hex.EncodeToString(token)
	if _, err := JoinRel(parentRel, tempName); err != nil {
		return nil, err
	}
	tempAbs := filepath.Join(parentAbs, tempName)
	staged, err := os.OpenFile(tempAbs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		_ = staged.Close()
		if cleanup {
			_ = os.Remove(tempAbs)
		}
	}()
	if _, err := staged.Write(data); err != nil {
		return nil, err
	}
	if err := staged.Sync(); err != nil {
		return nil, err
	}
	if err := staged.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tempAbs, targetAbs); err != nil {
		return nil, err
	}
	cleanup = false

	// Best effort directory sync makes the rename durable on supporting mounts.
	if dir, err := os.Open(parentAbs); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return s.Stat(volumeID, rel)
}

func metaFromResolved(res *Resolved) *Meta {
	name := filepath.Base(res.RelPath)
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
	}
}
