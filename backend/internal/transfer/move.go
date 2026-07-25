package transfer

import (
	"context"
	"errors"
	"fmt"
	"time"

	appfs "github.com/robikorb/dirdeck/backend/internal/fs"
)

// KindMove is a Phase 2 durable move job.
const KindMove = "move"

// SourceDeleteError means the destination is verified complete but the source
// could not be removed. The job must not be reported as completed.
type SourceDeleteError struct {
	Err error
}

func (e *SourceDeleteError) Error() string {
	if e == nil || e.Err == nil {
		return "destination verified but source could not be deleted; both copies may remain"
	}
	return fmt.Sprintf("destination verified but source could not be deleted: %v", e.Err)
}

func (e *SourceDeleteError) Unwrap() error { return e.Err }

// Fail stages for test injection (move + shared copy path).
const (
	StageRenameAttempt   = "rename_attempt"
	StageAfterCopyClose  = "after_copy_close"
	StageAfterVerify     = "after_verify"
	StageBeforeDestFinal = "before_dest_final"
	StageAfterDestFinal  = "after_dest_final"
	StageSourceDelete    = "source_delete"
)

func (m *Manager) checkFail(stage string) error {
	m.mu.Lock()
	hook := m.failHook
	m.mu.Unlock()
	if hook == nil {
		return nil
	}
	return hook(stage)
}

func (m *Manager) renameFunc() appfs.RenameFunc {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.renameFn
}

// SetTestHooks installs failure-injection and rename overrides for tests.
// Pass nil to clear. Not for production use.
func (m *Manager) SetTestHooks(fail func(stage string) error, rename appfs.RenameFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failHook = fail
	m.renameFn = rename
}

func (m *Manager) executeMove(ctx context.Context, id string, live *liveJob) error {
	job, err := m.Get(id)
	if err != nil {
		return err
	}
	if _, err := m.fs.EnsureWritableVolume(job.SourceVolumeID); err != nil {
		return err
	}
	if _, err := m.fs.EnsureWritableVolume(job.DestVolumeID); err != nil {
		return err
	}
	for _, sourcePath := range job.SourcePaths {
		src, err := m.fs.Stat(job.SourceVolumeID, sourcePath)
		if err != nil {
			return err
		}
		itemFiles, itemBytes, err := m.planTotals(job.SourceVolumeID, sourcePath, src.IsDir)
		if err != nil {
			return err
		}
		finalName := src.Name
		if len(job.SourcePaths) == 1 {
			finalName = job.DestName
		}
		if err := m.executeMoveOne(ctx, id, live, job, sourcePath, finalName, itemFiles, itemBytes); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) executeMoveOne(
	ctx context.Context,
	id string,
	live *liveJob,
	job *Job,
	sourcePath, requestedName string,
	itemFiles, itemBytes int64,
) error {
	src, err := m.fs.Stat(job.SourceVolumeID, sourcePath)
	if err != nil {
		return err
	}
	if src.IsSymlink {
		return fmt.Errorf("%w: cannot move symlink", ErrInvalidRequest)
	}
	if src.Path == "" {
		return fmt.Errorf("%w: cannot move volume root", ErrInvalidRequest)
	}

	finalName := requestedName
	destRel, err := appfs.JoinRel(job.DestDir, finalName)
	if err != nil {
		return err
	}

	exists, err := m.fs.Exists(job.DestVolumeID, destRel)
	if err != nil {
		return err
	}
	if exists {
		action, renameTo, err := m.resolveConflict(ctx, id, live, finalName)
		if err != nil {
			return err
		}
		switch action {
		case PolicySkip:
			// Leave source and destination untouched; count as done without moving.
			_ = m.bumpProgressCount(id, itemBytes, itemFiles, sourcePath)
			return nil
		case PolicyReplace:
			if err := m.fs.RemoveAllRel(job.DestVolumeID, destRel); err != nil {
				return err
			}
		case PolicyRename:
			if renameTo != "" {
				finalName = renameTo
			} else {
				finalName, err = m.uniqueName(job.DestVolumeID, job.DestDir, finalName)
				if err != nil {
					return err
				}
			}
			destRel, err = appfs.JoinRel(job.DestDir, finalName)
			if err != nil {
				return err
			}
		}
	}

	if err := m.checkFail(StageRenameAttempt); err != nil {
		return err
	}

	// Attempt atomic rename first — never decide from path strings alone.
	err = m.fs.TryRename(job.SourceVolumeID, sourcePath, job.DestVolumeID, destRel, m.renameFunc())
	if err == nil {
		_ = m.bumpProgressCount(id, itemBytes, itemFiles, sourcePath)
		return nil
	}
	if !appfs.IsCrossDeviceOrUnsupported(err) {
		return err
	}

	// Cross-filesystem / unsupported: verified copy, then delete source.
	if src.IsDir {
		if err := m.copyDir(ctx, id, live, job.SourceVolumeID, sourcePath, job.DestVolumeID, job.DestDir, finalName); err != nil {
			return err
		}
		return m.deleteMoveSource(id, job.SourceVolumeID, sourcePath, true)
	}

	if err := m.copyOneFile(ctx, id, live, job.SourceVolumeID, sourcePath, job.DestVolumeID, job.DestDir, finalName); err != nil {
		return err
	}
	return m.deleteMoveSource(id, job.SourceVolumeID, sourcePath, false)
}

func (m *Manager) deleteMoveSource(id, srcVol, srcRel string, isDir bool) error {
	if err := m.checkFail(StageAfterDestFinal); err != nil {
		// Destination is already final-named; do not clean it up.
		return fmt.Errorf("%w: %v", errAfterDestFinal, err)
	}
	if err := m.checkFail(StageSourceDelete); err != nil {
		return &SourceDeleteError{Err: err}
	}
	var err error
	if isDir {
		err = m.fs.RemoveAllRel(srcVol, srcRel)
	} else {
		err = m.fs.Remove(srcVol, srcRel)
	}
	if err != nil {
		return &SourceDeleteError{Err: err}
	}
	_, _ = m.db.Exec(`UPDATE transfer_jobs SET current_path=?, updated_at=? WHERE id=?`,
		"", time.Now().UTC().Format(time.RFC3339), id)
	return nil
}

func isDestPreservingFailure(err error) bool {
	if err == nil {
		return false
	}
	var srcDel *SourceDeleteError
	if errors.As(err, &srcDel) {
		return true
	}
	// Failures after the destination final name exists must not remove it.
	return errors.Is(err, errAfterDestFinal)
}

// errAfterDestFinal is wrapped when StageAfterDestFinal injection fires.
var errAfterDestFinal = errors.New("injected failure after destination final rename")
