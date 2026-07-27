package fs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// UploadStagingPrefix marks in-flight uploads. Partial data must never appear
// under the final name, so an interrupted upload leaves an unmistakably
// temporary file instead.
const UploadStagingPrefix = ".dirdeck-upload-"

// ErrUploadIncomplete means the client stopped sending before the declared
// length arrived. The staging file is removed; nothing reaches the final name.
var ErrUploadIncomplete = errors.New("upload ended before the declared length")

// SaveUpload streams src into parentRel/name through a staging file in the same
// directory and promotes it with a single rename once the bytes are on disk.
//
// expectedSize may be -1 when the length is unknown. When it is known the
// written total must match exactly, so a truncated connection cannot produce a
// short file under the real name.
//
// replaceExisting selects the rename mode: false refuses an existing
// destination through RENAME_NOREPLACE so a concurrent create cannot be
// clobbered, true overwrites deliberately.
func (s *Service) SaveUpload(
	ctx context.Context,
	volumeID, parentRel, name string,
	src io.Reader,
	expectedSize int64,
	maxBytes int64,
	replaceExisting bool,
) (*Meta, error) {
	vol, err := s.EnsureWritableVolume(volumeID)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && expectedSize > maxBytes {
		return nil, ErrTooLarge
	}

	parent, err := s.Resolve(volumeID, parentRel)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	if !parent.Info.IsDir() || parent.File == nil {
		return nil, ErrNotDirectory
	}
	parentAbs := parent.AbsPath

	// Validates the name: rejects separators, dot segments, and NUL bytes.
	destRel, err := JoinRel(parent.RelPath, name)
	if err != nil {
		return nil, err
	}
	destAbs := filepath.Join(parentAbs, path.Base(destRel))
	if !beneath(vol.Path, destAbs) {
		return nil, ErrEscape
	}

	token := make([]byte, 8)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	stagingName := UploadStagingPrefix + hex.EncodeToString(token)
	stagingRel, err := JoinRel(parent.RelPath, stagingName)
	if err != nil {
		return nil, err
	}
	stagingAbs := filepath.Join(parentAbs, stagingName)

	staged, err := os.OpenFile(stagingAbs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = staged.Close()
			_ = os.Remove(stagingAbs)
		}
	}()

	limit := maxBytes
	reader := src
	if limit > 0 {
		// One byte past the limit is enough to detect an oversized body.
		reader = io.LimitReader(src, limit+1)
	}

	buf := make([]byte, CopyBufferSize)
	written, err := StreamCopy(staged, reader, buf, func(int) error {
		return ctx.Err()
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, ErrUploadIncomplete
		}
		return nil, err
	}
	if limit > 0 && written > limit {
		return nil, ErrTooLarge
	}
	if expectedSize >= 0 && written != expectedSize {
		return nil, ErrUploadIncomplete
	}

	if err := staged.Sync(); err != nil {
		return nil, err
	}
	if err := staged.Close(); err != nil {
		return nil, err
	}

	info, err := os.Lstat(stagingAbs)
	if err != nil {
		return nil, err
	}
	if info.Size() != written {
		return nil, ErrUploadIncomplete
	}

	if replaceExisting {
		if err := os.Rename(stagingAbs, destAbs); err != nil {
			return nil, err
		}
	} else {
		err = unix.Renameat2(unix.AT_FDCWD, stagingAbs, unix.AT_FDCWD, destAbs, unix.RENAME_NOREPLACE)
		if err != nil {
			if errors.Is(err, syscall.EEXIST) {
				return nil, ErrExists
			}
			if !errors.Is(err, syscall.ENOSYS) && !errors.Is(err, syscall.EINVAL) &&
				!errors.Is(err, syscall.ENOTSUP) {
				return nil, err
			}
			// Filesystems without renameat2 fall back to a checked rename.
			if _, statErr := os.Lstat(destAbs); statErr == nil {
				return nil, ErrExists
			} else if !os.IsNotExist(statErr) {
				return nil, statErr
			}
			if err := os.Rename(stagingAbs, destAbs); err != nil {
				return nil, err
			}
		}
	}
	cleanup = false

	// Best effort: makes the rename durable on filesystems that support it.
	if dir, err := os.Open(parentAbs); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	_ = stagingRel

	return s.Stat(volumeID, destRel)
}

// SweepUploadStaging removes abandoned upload staging files from a directory.
// A killed process cannot run its own cleanup, so orphans are collected the
// next time the folder is used as an upload destination.
func (s *Service) SweepUploadStaging(volumeID, parentRel string, olderThan time.Duration) {
	entries, err := s.listDirectory(volumeID, parentRel, false, 0)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-olderThan)
	for _, e := range entries.Entries {
		if !e.IsDir && len(e.Name) > len(UploadStagingPrefix) &&
			e.Name[:len(UploadStagingPrefix)] == UploadStagingPrefix &&
			(olderThan <= 0 || e.ModTime.Before(cutoff)) {
			_ = s.Remove(volumeID, e.Path)
		}
	}
}

// MkdirAllRel creates a relative directory chain beneath parentRel, validating
// every component through the same resolver used for single-level Mkdir.
//
// Folder upload needs this: the browser sends a file's path inside the dropped
// tree, and each level has to be created before the file can land. Existing
// directories are reused; an existing non-directory is an error rather than
// something to overwrite.
func (s *Service) MkdirAllRel(volumeID, parentRel, relDir string) (string, error) {
	if _, err := s.EnsureWritableVolume(volumeID); err != nil {
		return "", err
	}
	current, err := normalizeRel(parentRel)
	if err != nil {
		return "", err
	}
	clean, err := normalizeRel(relDir)
	if err != nil {
		return "", err
	}
	if clean == "" {
		return current, nil
	}
	for _, part := range strings.Split(clean, "/") {
		created, err := s.Mkdir(volumeID, current, part)
		if err != nil {
			return "", err
		}
		current = created
	}
	return current, nil
}
