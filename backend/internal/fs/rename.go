package fs

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// ErrCrossDevice indicates rename must fall back to copy+delete.
var ErrCrossDevice = errors.New("invalid cross-device link")

// RenameFunc is used by TryRename; tests may inject EXDEV.
type RenameFunc func(oldpath, newpath string) error

// IsCrossDeviceOrUnsupported reports whether rename failed for an expected
// cross-filesystem or unsupported-operation reason (safe to fall back).
func IsCrossDeviceOrUnsupported(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrCrossDevice) {
		return true
	}
	if errors.Is(err, syscall.EXDEV) {
		return true
	}
	if errors.Is(err, syscall.ENOTSUP) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		if errno == syscall.EXDEV || errno == syscall.ENOTSUP {
			return true
		}
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return IsCrossDeviceOrUnsupported(linkErr.Err)
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return IsCrossDeviceOrUnsupported(pathErr.Err)
	}
	return false
}

// TryRename attempts an atomic rename from source to destination across
// configured volumes. Callers must not decide same- vs cross-filesystem from
// path strings alone; attempt rename and inspect the error.
//
// Both volumes must be writable (move removes the source path).
// renameFn may be nil to use os.Rename.
func (s *Service) TryRename(srcVolID, srcRel, dstVolID, dstRel string, renameFn RenameFunc) error {
	if _, err := s.EnsureWritableVolume(srcVolID); err != nil {
		return err
	}
	dstVol, err := s.EnsureWritableVolume(dstVolID)
	if err != nil {
		return err
	}
	srcRes, err := s.Resolve(srcVolID, srcRel)
	if err != nil {
		return err
	}
	srcAbs := srcRes.AbsPath
	_ = srcRes.Close()

	dstRelNorm, err := normalizeRel(dstRel)
	if err != nil {
		return err
	}
	if dstRelNorm == "" {
		return ErrInvalidPath
	}
	parentRel := filepath.Dir(dstRelNorm)
	if parentRel == "." {
		parentRel = ""
	}
	parent, err := s.Resolve(dstVolID, parentRel)
	if err != nil {
		return err
	}
	_ = parent.Close()

	dstAbs := filepath.Join(dstVol.Path, filepath.FromSlash(dstRelNorm))
	if !beneath(dstVol.Path, dstAbs) {
		return ErrEscape
	}
	// Refuse no-op / identical path.
	if filepath.Clean(srcAbs) == filepath.Clean(dstAbs) {
		return ErrInvalidPath
	}

	if renameFn == nil {
		renameFn = os.Rename
	}
	if err := renameFn(srcAbs, dstAbs); err != nil {
		if IsCrossDeviceOrUnsupported(err) {
			return ErrCrossDevice
		}
		return err
	}
	return nil
}
