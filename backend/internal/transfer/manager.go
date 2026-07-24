package transfer

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	appfs "github.com/liquid-glass-file-manager/backend/internal/fs"
)

// Job statuses.
const (
	StatusQueued     = "queued"
	StatusRunning    = "running"
	StatusCancelling = "cancelling"
	StatusCancelled  = "cancelled"
	StatusFailed     = "failed"
	StatusCompleted  = "completed"
	StatusConflict   = "conflict"
)

// Conflict policies.
const (
	PolicyPrompt  = "prompt"
	PolicySkip    = "skip"
	PolicyReplace = "replace"
	PolicyRename  = "rename"
)

// KindCopy is a durable copy job (Phase 1).
const KindCopy = "copy"

var (
	ErrNotFound          = errors.New("transfer not found")
	ErrInvalidRequest    = errors.New("invalid transfer request")
	ErrConflictWait      = errors.New("waiting for conflict resolution")
	ErrCancelled         = errors.New("transfer cancelled")
	ErrInsufficientSpace = errors.New("insufficient destination free space")
)

// Job is the public transfer job snapshot.
type Job struct {
	ID             string     `json:"id"`
	Kind           string     `json:"kind"`
	Status         string     `json:"status"`
	SourceVolumeID string     `json:"sourceVolumeId"`
	SourcePath     string     `json:"sourcePath"`
	SourcePaths    []string   `json:"sourcePaths"`
	DestVolumeID   string     `json:"destVolumeId"`
	DestDir        string     `json:"destDir"`
	DestName       string     `json:"destName"`
	ConflictPolicy string     `json:"conflictPolicy"`
	ApplyToAll     bool       `json:"applyToAll"`
	BytesTotal     int64      `json:"bytesTotal"`
	BytesDone      int64      `json:"bytesDone"`
	FilesTotal     int64      `json:"filesTotal"`
	FilesDone      int64      `json:"filesDone"`
	BytesPerSecond float64    `json:"bytesPerSecond"`
	BytesRemaining int64      `json:"bytesRemaining"`
	CurrentPath    string     `json:"currentPath"`
	StagingName    string     `json:"stagingName"`
	ConflictName   *string    `json:"conflictName"`
	ErrorMessage   *string    `json:"errorMessage"`
	FreeSpaceKnown bool       `json:"freeSpaceKnown"`
	FreeSpaceBytes *int64     `json:"freeSpaceBytes"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	StartedAt      *time.Time `json:"startedAt"`
	FinishedAt     *time.Time `json:"finishedAt"`
}

// CreateRequest creates a copy or move job.
type CreateRequest struct {
	Kind           string   `json:"kind"`
	SourceVolumeID string   `json:"sourceVolumeId"`
	SourcePath     string   `json:"sourcePath"`
	SourcePaths    []string `json:"sourcePaths"`
	DestVolumeID   string   `json:"destVolumeId"`
	DestDir        string   `json:"destDir"`
	ConflictPolicy string   `json:"conflictPolicy"`
}

// ConflictResolution resolves a prompt conflict.
type ConflictResolution struct {
	Action     string `json:"action"` // skip|replace|rename
	ApplyToAll bool   `json:"applyToAll"`
	RenameTo   string `json:"renameTo"`
}

type conflictReply struct {
	resolution ConflictResolution
}

type liveJob struct {
	cancel context.CancelFunc
	wait   chan conflictReply
}

// Manager runs durable copy and move jobs.
type Manager struct {
	db *sql.DB
	fs *appfs.Service

	mu     sync.Mutex
	live   map[string]*liveJob
	wg     sync.WaitGroup
	closed bool
	subsMu sync.RWMutex
	subs   map[chan Job]struct{}

	// Test-only hooks (nil in production).
	failHook func(stage string) error
	renameFn appfs.RenameFunc
}

// New creates a transfer manager and reconciles interrupted jobs.
func New(database *sql.DB, fsys *appfs.Service) (*Manager, error) {
	m := &Manager{
		db:   database,
		fs:   fsys,
		live: make(map[string]*liveJob),
		subs: make(map[chan Job]struct{}),
	}
	if err := m.Reconcile(); err != nil {
		return nil, err
	}
	return m, nil
}

// Reconcile marks interrupted running/cancelling jobs as failed (never completed).
func (m *Manager) Reconcile() error {
	now := time.Now().UTC()
	msg := "interrupted by process restart; partial files retain .lgfm-partial-* names"
	res, err := m.db.Exec(`
		UPDATE transfer_jobs
		SET status = ?, error_message = ?, updated_at = ?, finished_at = ?
		WHERE status IN (?, ?)
	`, StatusFailed, msg, now.Format(time.RFC3339), now.Format(time.RFC3339), StatusRunning, StatusCancelling)
	if err != nil {
		return err
	}
	_, _ = res.RowsAffected()

	// Resume queued jobs that survived a crash before the worker started.
	rows, err := m.db.Query(`SELECT id FROM transfer_jobs WHERE status = ? ORDER BY created_at`, StatusQueued)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		m.startWorker(id)
	}
	return nil
}

// Subscribe receives job snapshots. Caller must Unsubscribe.
func (m *Manager) Subscribe() chan Job {
	ch := make(chan Job, 16)
	m.subsMu.Lock()
	m.subs[ch] = struct{}{}
	m.subsMu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber.
func (m *Manager) Unsubscribe(ch chan Job) {
	m.subsMu.Lock()
	delete(m.subs, ch)
	m.subsMu.Unlock()
	close(ch)
}

func (m *Manager) publish(job Job) {
	m.subsMu.RLock()
	defer m.subsMu.RUnlock()
	for ch := range m.subs {
		select {
		case ch <- job:
		default:
			// Drop if subscriber is slow; they can REST-refresh.
		}
	}
}

func newID() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Create enqueues a copy or move job.
func (m *Manager) Create(req CreateRequest) (*Job, error) {
	kind := req.Kind
	if kind == "" {
		kind = KindCopy
	}
	if kind != KindCopy && kind != KindMove {
		return nil, fmt.Errorf("%w: unsupported kind", ErrInvalidRequest)
	}
	if req.SourceVolumeID == "" || req.DestVolumeID == "" {
		return nil, fmt.Errorf("%w: volumes required", ErrInvalidRequest)
	}
	sourcePaths := req.SourcePaths
	if len(sourcePaths) == 0 {
		sourcePaths = []string{req.SourcePath}
	}
	if len(sourcePaths) == 0 || len(sourcePaths) > 500 {
		return nil, fmt.Errorf("%w: select between 1 and 500 source items", ErrInvalidRequest)
	}
	seenPaths := make(map[string]struct{}, len(sourcePaths))
	uniquePaths := make([]string, 0, len(sourcePaths))
	for _, sourcePath := range sourcePaths {
		src, err := m.fs.Stat(req.SourceVolumeID, sourcePath)
		if err != nil {
			return nil, err
		}
		canonicalPath := src.Path
		if _, exists := seenPaths[canonicalPath]; exists {
			continue
		}
		seenPaths[canonicalPath] = struct{}{}
		uniquePaths = append(uniquePaths, canonicalPath)
	}
	sourcePaths = uniquePaths
	req.SourcePath = sourcePaths[0]
	policy := req.ConflictPolicy
	if policy == "" {
		policy = PolicyPrompt
	}
	switch policy {
	case PolicyPrompt, PolicySkip, PolicyReplace, PolicyRename:
	default:
		return nil, fmt.Errorf("%w: invalid conflict policy", ErrInvalidRequest)
	}

	// Destination must be writable (RO fixture rejection).
	if _, err := m.fs.EnsureWritableVolume(req.DestVolumeID); err != nil {
		return nil, err
	}
	// Move deletes the source; read-only sources are rejected.
	if kind == KindMove {
		if _, err := m.fs.EnsureWritableVolume(req.SourceVolumeID); err != nil {
			return nil, err
		}
	}
	// Destination directory must exist.
	destDirMeta, err := m.fs.Stat(req.DestVolumeID, req.DestDir)
	if err != nil {
		return nil, err
	}
	if !destDirMeta.IsDir {
		return nil, fmt.Errorf("%w: destination is not a directory", ErrInvalidRequest)
	}

	destName := fmt.Sprintf("%d items", len(sourcePaths))
	for index, sourcePath := range sourcePaths {
		src, err := m.fs.Stat(req.SourceVolumeID, sourcePath)
		if err != nil {
			return nil, err
		}
		if src.IsSymlink {
			return nil, fmt.Errorf("%w: cannot %s symlink", ErrInvalidRequest, kind)
		}
		if kind == KindMove && src.Path == "" {
			return nil, fmt.Errorf("%w: cannot move volume root", ErrInvalidRequest)
		}
		itemName := filepath.Base(src.Path)
		if src.Path == "" {
			itemName = src.Name
		}
		if itemName == "" || itemName == "." {
			itemName = kind
		}
		if err := m.validateTransferTarget(
			req.SourceVolumeID,
			sourcePath,
			src.IsDir,
			req.DestVolumeID,
			req.DestDir,
			itemName,
		); err != nil {
			return nil, err
		}
		if len(sourcePaths) == 1 && index == 0 {
			destName = itemName
		}
	}

	id, err := newID()
	if err != nil {
		return nil, err
	}
	staging := ".lgfm-partial-" + id
	now := time.Now().UTC()

	sourcePathsJSON, err := json.Marshal(sourcePaths)
	if err != nil {
		return nil, err
	}

	freeBytes, freeKnown, _ := m.fs.FreeSpace(req.DestVolumeID)
	var freeSpace any
	freeKnownInt := 0
	if freeKnown {
		freeKnownInt = 1
		freeSpace = freeBytes
	}

	_, err = m.db.Exec(`
		INSERT INTO transfer_jobs (
			id, kind, status, source_volume_id, source_path, source_paths, dest_volume_id, dest_dir, dest_name,
			conflict_policy, apply_to_all, bytes_total, bytes_done, files_total, files_done,
			bytes_per_second, current_path, staging_name, free_space_known, free_space_bytes,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, 0, ?, 0, 0, '', ?, ?, ?, ?, ?)
	`, id, kind, StatusQueued, req.SourceVolumeID, req.SourcePath, string(sourcePathsJSON), req.DestVolumeID, req.DestDir, destName,
		policy, 0, 0, staging, freeKnownInt, freeSpace,
		now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return nil, err
	}

	job, err := m.Get(id)
	if err != nil {
		return nil, err
	}
	m.publish(*job)
	m.startWorker(id)
	return job, nil
}

func (m *Manager) validateTransferTarget(
	srcVolumeID, srcRel string,
	srcIsDir bool,
	dstVolumeID, dstDir, destName string,
) error {
	src, err := m.fs.Resolve(srcVolumeID, srcRel)
	if err != nil {
		return err
	}
	srcAbs := filepath.Clean(src.AbsPath)
	_ = src.Close()
	dst, err := m.fs.Resolve(dstVolumeID, dstDir)
	if err != nil {
		return err
	}
	dstAbs := filepath.Clean(filepath.Join(dst.AbsPath, destName))
	_ = dst.Close()

	if srcAbs == dstAbs {
		return fmt.Errorf("%w: source and destination are the same path", ErrInvalidRequest)
	}
	if srcIsDir {
		rel, err := filepath.Rel(srcAbs, dstAbs)
		if err == nil && rel != "." && rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator)) &&
			!filepath.IsAbs(rel) {
			return fmt.Errorf("%w: destination cannot be inside the source directory", ErrInvalidRequest)
		}
	}
	return nil
}

func (m *Manager) planTotals(volumeID, rel string, isDir bool) (files, bytes int64, err error) {
	return m.planTotalsContext(context.Background(), volumeID, rel, isDir)
}

func (m *Manager) planTotalsContext(ctx context.Context, volumeID, rel string, isDir bool) (files, bytes int64, err error) {
	if !isDir {
		meta, err := m.fs.Stat(volumeID, rel)
		if err != nil {
			return 0, 0, err
		}
		return 1, meta.Size, nil
	}
	err = m.walkFilesContext(ctx, volumeID, rel, func(path string, size int64) error {
		files++
		bytes += size
		return nil
	})
	return files, bytes, err
}

func (m *Manager) walkFiles(volumeID, rel string, fn func(path string, size int64) error) error {
	return m.walkFilesContext(context.Background(), volumeID, rel, fn)
}

func (m *Manager) walkFilesContext(ctx context.Context, volumeID, rel string, fn func(path string, size int64) error) error {
	if err := ctx.Err(); err != nil {
		return ErrCancelled
	}
	entries, err := m.fs.ListForTransfer(volumeID, rel)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsSymlink {
			continue // never follow or copy symlink targets in Phase 1
		}
		if e.IsDir {
			if err := m.walkFilesContext(ctx, volumeID, e.Path, fn); err != nil {
				return err
			}
			continue
		}
		if err := fn(e.Path, e.Size); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) prepareJob(ctx context.Context, id string) error {
	job, err := m.Get(id)
	if err != nil {
		return err
	}
	var filesTotal, bytesTotal int64
	for _, sourcePath := range job.SourcePaths {
		src, err := m.fs.Stat(job.SourceVolumeID, sourcePath)
		if err != nil {
			return err
		}
		files, bytes, err := m.planTotalsContext(ctx, job.SourceVolumeID, sourcePath, src.IsDir)
		if err != nil {
			return err
		}
		filesTotal += files
		bytesTotal += bytes
	}
	if job.FreeSpaceKnown && job.FreeSpaceBytes != nil &&
		(job.Kind == KindCopy || job.SourceVolumeID != job.DestVolumeID) &&
		bytesTotal > *job.FreeSpaceBytes {
		return fmt.Errorf(
			"%w: need %d bytes, only %d bytes available",
			ErrInsufficientSpace,
			bytesTotal,
			*job.FreeSpaceBytes,
		)
	}
	_, err = m.db.Exec(
		`UPDATE transfer_jobs
		 SET bytes_total=?, files_total=?, current_path='', updated_at=?
		 WHERE id=?`,
		bytesTotal,
		filesTotal,
		time.Now().UTC().Format(time.RFC3339),
		id,
	)
	return err
}

// Get returns a job by ID.
func (m *Manager) Get(id string) (*Job, error) {
	row := m.db.QueryRow(`SELECT
		id, kind, status, source_volume_id, source_path, source_paths, dest_volume_id, dest_dir, dest_name,
		conflict_policy, apply_to_all, bytes_total, bytes_done, files_total, files_done,
		bytes_per_second, current_path, staging_name, conflict_name, error_message,
		free_space_known, free_space_bytes, created_at, updated_at, started_at, finished_at
		FROM transfer_jobs WHERE id = ?`, id)
	return scanJob(row)
}

// List returns recent jobs (newest first).
func (m *Manager) List(limit int) ([]Job, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := m.db.Query(`SELECT
		id, kind, status, source_volume_id, source_path, source_paths, dest_volume_id, dest_dir, dest_name,
		conflict_policy, apply_to_all, bytes_total, bytes_done, files_total, files_done,
		bytes_per_second, current_path, staging_name, conflict_name, error_message,
		free_space_known, free_space_bytes, created_at, updated_at, started_at, finished_at
		FROM transfer_jobs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJobRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

type scannable interface {
	Scan(dest ...any) error
}

func scanJob(row scannable) (*Job, error) {
	var (
		j                                       Job
		apply                                   int
		sourcePathsJSON                         string
		conflictName, errMsg, started, finished sql.NullString
		freeKnown                               int
		freeBytes                               sql.NullInt64
		created, updated                        string
	)
	err := row.Scan(
		&j.ID, &j.Kind, &j.Status, &j.SourceVolumeID, &j.SourcePath, &sourcePathsJSON, &j.DestVolumeID, &j.DestDir, &j.DestName,
		&j.ConflictPolicy, &apply, &j.BytesTotal, &j.BytesDone, &j.FilesTotal, &j.FilesDone,
		&j.BytesPerSecond, &j.CurrentPath, &j.StagingName, &conflictName, &errMsg,
		&freeKnown, &freeBytes, &created, &updated, &started, &finished,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	j.ApplyToAll = apply != 0
	if err := json.Unmarshal([]byte(sourcePathsJSON), &j.SourcePaths); err != nil || len(j.SourcePaths) == 0 {
		j.SourcePaths = []string{j.SourcePath}
	}
	j.BytesRemaining = j.BytesTotal - j.BytesDone
	if j.BytesRemaining < 0 {
		j.BytesRemaining = 0
	}
	if conflictName.Valid {
		s := conflictName.String
		j.ConflictName = &s
	}
	if errMsg.Valid {
		s := errMsg.String
		j.ErrorMessage = &s
	}
	j.FreeSpaceKnown = freeKnown != 0
	if freeBytes.Valid {
		v := freeBytes.Int64
		j.FreeSpaceBytes = &v
	}
	j.CreatedAt, _ = time.Parse(time.RFC3339, created)
	j.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	if started.Valid {
		t, _ := time.Parse(time.RFC3339, started.String)
		j.StartedAt = &t
	}
	if finished.Valid {
		t, _ := time.Parse(time.RFC3339, finished.String)
		j.FinishedAt = &t
	}
	return &j, nil
}

func scanJobRows(rows *sql.Rows) (*Job, error) {
	return scanJob(rows)
}

// Cancel requests cancellation.
func (m *Manager) Cancel(id string) (*Job, error) {
	job, err := m.Get(id)
	if err != nil {
		return nil, err
	}
	switch job.Status {
	case StatusCompleted, StatusCancelled, StatusFailed:
		return job, nil
	case StatusQueued:
		now := time.Now().UTC()
		_, err = m.db.Exec(`UPDATE transfer_jobs SET status=?, updated_at=?, finished_at=? WHERE id=?`,
			StatusCancelled, now.Format(time.RFC3339), now.Format(time.RFC3339), id)
		if err != nil {
			return nil, err
		}
	default:
		now := time.Now().UTC()
		_, err = m.db.Exec(`UPDATE transfer_jobs SET status=?, updated_at=? WHERE id=? AND status IN (?, ?, ?)`,
			StatusCancelling, now.Format(time.RFC3339), id, StatusRunning, StatusConflict, StatusCancelling)
		if err != nil {
			return nil, err
		}
		m.mu.Lock()
		if live, ok := m.live[id]; ok {
			if live.cancel != nil {
				live.cancel()
			}
			// Unblock conflict wait with cancel-like skip signal via closed wait after cancel.
			select {
			case live.wait <- conflictReply{resolution: ConflictResolution{Action: "cancel"}}:
			default:
			}
		}
		m.mu.Unlock()
	}
	job, err = m.Get(id)
	if err != nil {
		return nil, err
	}
	m.publish(*job)
	return job, nil
}

// ResolveConflict continues a job paused in conflict.
func (m *Manager) ResolveConflict(id string, res ConflictResolution) (*Job, error) {
	switch res.Action {
	case PolicySkip, PolicyReplace, PolicyRename:
	default:
		return nil, fmt.Errorf("%w: invalid conflict action", ErrInvalidRequest)
	}
	job, err := m.Get(id)
	if err != nil {
		return nil, err
	}
	if job.Status != StatusConflict {
		return nil, fmt.Errorf("%w: job is not waiting on conflict", ErrInvalidRequest)
	}
	m.mu.Lock()
	live, ok := m.live[id]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: no active worker", ErrInvalidRequest)
	}
	select {
	case live.wait <- conflictReply{resolution: res}:
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("conflict resolution timed out")
	}
	// Worker will update; briefly poll for status change.
	for i := 0; i < 50; i++ {
		time.Sleep(20 * time.Millisecond)
		job, err = m.Get(id)
		if err != nil {
			return nil, err
		}
		if job.Status != StatusConflict {
			break
		}
	}
	return job, nil
}

func (m *Manager) startWorker(id string) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	if _, exists := m.live[id]; exists {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	live := &liveJob{cancel: cancel, wait: make(chan conflictReply, 1)}
	m.live[id] = live
	m.wg.Add(1)
	m.mu.Unlock()

	go func() {
		defer func() {
			m.mu.Lock()
			delete(m.live, id)
			m.mu.Unlock()
			m.wg.Done()
		}()
		m.runJob(ctx, id, live)
	}()
}

// Shutdown cancels active workers and waits for their staging files and durable
// status to be reconciled before the process exits.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	for _, live := range m.live {
		if live.cancel != nil {
			live.cancel()
		}
		select {
		case live.wait <- conflictReply{resolution: ConflictResolution{Action: "cancel"}}:
		default:
		}
	}
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) runJob(ctx context.Context, id string, live *liveJob) {
	job, err := m.Get(id)
	if err != nil {
		return
	}
	if job.Status == StatusCancelled || job.Status == StatusFailed || job.Status == StatusCompleted {
		return
	}
	now := time.Now().UTC()
	_, _ = m.db.Exec(`UPDATE transfer_jobs SET status=?, started_at=COALESCE(started_at, ?), updated_at=? WHERE id=?`,
		StatusRunning, now.Format(time.RFC3339), now.Format(time.RFC3339), id)
	job, _ = m.Get(id)
	if job != nil {
		m.publish(*job)
	}

	if job != nil {
		err = m.prepareJob(ctx, id)
	}
	if err == nil && job != nil && job.Kind == KindMove {
		err = m.executeMove(ctx, id, live)
	} else if err == nil {
		err = m.executeCopy(ctx, id, live)
	}
	now = time.Now().UTC()
	if errors.Is(err, ErrCancelled) || m.isCancelling(id) {
		_ = m.cleanupPartials(id)
		_, _ = m.db.Exec(`UPDATE transfer_jobs SET status=?, updated_at=?, finished_at=?, error_message=? WHERE id=?`,
			StatusCancelled, now.Format(time.RFC3339), now.Format(time.RFC3339), "cancelled", id)
	} else if err != nil {
		// Never remove a verified final destination when source delete (or
		// post-final) failed — that would destroy the only complete copy.
		if !isDestPreservingFailure(err) {
			_ = m.cleanupPartials(id)
		}
		_, _ = m.db.Exec(`UPDATE transfer_jobs SET status=?, updated_at=?, finished_at=?, error_message=? WHERE id=?`,
			StatusFailed, now.Format(time.RFC3339), now.Format(time.RFC3339), truncateErr(err), id)
	} else {
		_, _ = m.db.Exec(`UPDATE transfer_jobs SET status=?, updated_at=?, finished_at=?, bytes_done=bytes_total, files_done=files_total, bytes_per_second=0, current_path='', conflict_name=NULL WHERE id=?`,
			StatusCompleted, now.Format(time.RFC3339), now.Format(time.RFC3339), id)
	}
	if job, err := m.Get(id); err == nil {
		m.publish(*job)
	}
}

func truncateErr(err error) string {
	s := err.Error()
	if len(s) > 500 {
		return s[:500]
	}
	return s
}

func (m *Manager) isCancelling(id string) bool {
	job, err := m.Get(id)
	if err != nil {
		return false
	}
	return job.Status == StatusCancelling || job.Status == StatusCancelled
}

func (m *Manager) cleanupPartials(id string) error {
	job, err := m.Get(id)
	if err != nil {
		return err
	}
	if job.StagingName != "" {
		// staging_name is a full volume-relative path when a file copy is in flight.
		if strings.Contains(job.StagingName, "/") || strings.HasPrefix(job.StagingName, ".lgfm-partial-") {
			_ = m.fs.Remove(job.DestVolumeID, job.StagingName)
		} else if rel, joinErr := appfs.JoinRel(job.DestDir, job.StagingName); joinErr == nil {
			_ = m.fs.Remove(job.DestVolumeID, rel)
		}
	}
	return m.removePartialTree(job.DestVolumeID, job.DestDir, id)
}

func (m *Manager) removePartialTree(volumeID, dir, jobID string) error {
	prefix := ".lgfm-partial-" + jobID
	var walk func(string) error
	walk = func(rel string) error {
		res, err := m.fs.Resolve(volumeID, rel)
		if err != nil || res.File == nil {
			return nil
		}
		names, err := res.File.Readdirnames(-1)
		_ = res.Close()
		if err != nil {
			return nil
		}
		for _, name := range names {
			child, jerr := appfs.JoinRel(rel, name)
			if jerr != nil {
				continue
			}
			info, serr := m.fs.Stat(volumeID, child)
			if serr != nil {
				continue
			}
			if info.IsDir {
				_ = walk(child)
				continue
			}
			if strings.HasPrefix(name, prefix) {
				_ = m.fs.Remove(volumeID, child)
			}
		}
		return nil
	}
	return walk(dir)
}

func (m *Manager) executeCopy(ctx context.Context, id string, live *liveJob) error {
	job, err := m.Get(id)
	if err != nil {
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
		base := filepath.Base(src.Path)
		if src.Path == "" {
			base = src.Name
		}
		if len(job.SourcePaths) == 1 {
			base = job.DestName
		}
		if src.IsDir {
			if err := m.copyDir(ctx, id, live, job.SourceVolumeID, sourcePath, job.DestVolumeID, job.DestDir, base); err != nil {
				return err
			}
			continue
		}
		if err := m.copyOneFile(ctx, id, live, job.SourceVolumeID, sourcePath, job.DestVolumeID, job.DestDir, base); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) copyDir(ctx context.Context, id string, live *liveJob, srcVol, srcRel, dstVol, dstParent, destBase string) error {
	// Create (or reuse) destination root directory after conflict handling.
	finalRel, err := m.ensureDestDir(ctx, id, live, dstVol, dstParent, destBase)
	if err != nil {
		return err
	}
	return m.copyDirContents(ctx, id, live, srcVol, srcRel, dstVol, finalRel)
}

func (m *Manager) ensureDestDir(ctx context.Context, id string, live *liveJob, dstVol, dstParent, name string) (string, error) {
	rel, err := appfs.JoinRel(dstParent, name)
	if err != nil {
		return "", err
	}
	exists, err := m.fs.Exists(dstVol, rel)
	if err != nil {
		return "", err
	}
	if !exists {
		return m.fs.Mkdir(dstVol, dstParent, name)
	}
	action, renameTo, err := m.resolveConflict(ctx, id, live, name)
	if err != nil {
		return "", err
	}
	switch action {
	case PolicySkip:
		return rel, nil // copy into existing
	case PolicyReplace:
		if err := m.fs.RemoveAllRel(dstVol, rel); err != nil {
			return "", err
		}
		return m.fs.Mkdir(dstVol, dstParent, name)
	case PolicyRename:
		newName := renameTo
		if newName == "" {
			newName, err = m.uniqueName(dstVol, dstParent, name)
			if err != nil {
				return "", err
			}
		}
		return m.fs.Mkdir(dstVol, dstParent, newName)
	default:
		return "", fmt.Errorf("unknown conflict action %q", action)
	}
}

func (m *Manager) copyDirContents(ctx context.Context, id string, live *liveJob, srcVol, srcRel, dstVol, dstRel string) error {
	entries, err := m.fs.ListForTransfer(srcVol, srcRel)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return ErrCancelled
		}
		if m.isCancelling(id) {
			return ErrCancelled
		}
		if e.IsSymlink {
			continue
		}
		if e.IsDir {
			childDst, err := m.fs.Mkdir(dstVol, dstRel, e.Name)
			if err != nil {
				return err
			}
			if err := m.copyDirContents(ctx, id, live, srcVol, e.Path, dstVol, childDst); err != nil {
				return err
			}
			continue
		}
		if err := m.copyOneFile(ctx, id, live, srcVol, e.Path, dstVol, dstRel, e.Name); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) copyOneFile(ctx context.Context, id string, live *liveJob, srcVol, srcRel, dstVol, dstDir, destName string) error {
	if err := ctx.Err(); err != nil {
		return ErrCancelled
	}
	if m.isCancelling(id) {
		return ErrCancelled
	}
	if _, err := m.fs.EnsureWritableVolume(dstVol); err != nil {
		return err
	}

	finalName := destName
	destRel, err := appfs.JoinRel(dstDir, finalName)
	if err != nil {
		return err
	}
	exists, err := m.fs.Exists(dstVol, destRel)
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
			meta, _ := m.fs.Stat(srcVol, srcRel)
			var size int64
			if meta != nil {
				size = meta.Size
			}
			_ = m.bumpProgress(id, size, true, srcRel)
			return nil
		case PolicyReplace:
			if err := m.fs.RemoveAllRel(dstVol, destRel); err != nil {
				return err
			}
		case PolicyRename:
			if renameTo != "" {
				finalName = renameTo
			} else {
				finalName, err = m.uniqueName(dstVol, dstDir, finalName)
				if err != nil {
					return err
				}
			}
			destRel, err = appfs.JoinRel(dstDir, finalName)
			if err != nil {
				return err
			}
		}
	}

	stagingName := fmt.Sprintf(".lgfm-partial-%s", id)
	// If staging name collides within a recursive copy of many files, uniquify.
	if ok, _ := m.fs.Exists(dstVol, mustJoin(dstDir, stagingName)); ok {
		stagingName = fmt.Sprintf(".lgfm-partial-%s-%s", id, sanitizeStaging(finalName))
	}
	src, err := m.fs.OpenFile(srcVol, srcRel)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := m.fs.CreateFile(dstVol, dstDir, stagingName, true)
	if err != nil {
		// Retry after removing leftover partial from same job.
		_ = m.fs.Remove(dstVol, mustJoin(dstDir, stagingName))
		dst, err = m.fs.CreateFile(dstVol, dstDir, stagingName, true)
		if err != nil {
			return err
		}
	}
	stagingRel := dst.RelPath
	_, _ = m.db.Exec(`UPDATE transfer_jobs SET staging_name=?, current_path=?, updated_at=? WHERE id=?`,
		stagingRel, srcRel, time.Now().UTC().Format(time.RFC3339), id)
	if job, err := m.Get(id); err == nil {
		m.publish(*job)
	}

	buf := make([]byte, appfs.CopyBufferSize)
	start := time.Now()
	var windowBytes int64
	var pendingProgress int64
	windowStart := start

	written, err := appfs.StreamCopy(dst.File, src.File, buf, func(n int) error {
		if ctx.Err() != nil {
			return ErrCancelled
		}
		windowBytes += int64(n)
		pendingProgress += int64(n)
		if time.Since(windowStart) >= 200*time.Millisecond {
			if err := m.bumpProgress(id, pendingProgress, false, srcRel); err != nil {
				return err
			}
			pendingProgress = 0
			sec := time.Since(windowStart).Seconds()
			speed := float64(windowBytes) / sec
			_, _ = m.db.Exec(`UPDATE transfer_jobs SET bytes_per_second=?, updated_at=? WHERE id=?`,
				speed, time.Now().UTC().Format(time.RFC3339), id)
			if job, err := m.Get(id); err == nil {
				m.publish(*job)
			}
			windowBytes = 0
			windowStart = time.Now()
		}
		return nil
	})
	if pendingProgress > 0 {
		if progressErr := m.bumpProgress(id, pendingProgress, false, srcRel); err == nil {
			err = progressErr
		}
	}
	closeErr := dst.File.Sync()
	_ = dst.Close()
	if err != nil {
		_ = m.fs.Remove(dstVol, stagingRel)
		return err
	}
	if closeErr != nil {
		_ = m.fs.Remove(dstVol, stagingRel)
		return closeErr
	}
	if err := m.checkFail(StageAfterCopyClose); err != nil {
		_ = m.fs.Remove(dstVol, stagingRel)
		return err
	}

	// Verify size.
	st, err := m.fs.Stat(dstVol, stagingRel)
	if err != nil {
		_ = m.fs.Remove(dstVol, stagingRel)
		return err
	}
	if st.Size != written || st.Size != src.Info.Size() {
		_ = m.fs.Remove(dstVol, stagingRel)
		return fmt.Errorf("size verification failed: wrote %d staged %d source %d", written, st.Size, src.Info.Size())
	}
	if err := m.checkFail(StageAfterVerify); err != nil {
		_ = m.fs.Remove(dstVol, stagingRel)
		return err
	}
	if ctx.Err() != nil {
		_ = m.fs.Remove(dstVol, stagingRel)
		return ErrCancelled
	}

	// Preserve modtime best-effort.
	_ = os.Chtimes(dst.AbsPath, src.Info.ModTime(), src.Info.ModTime())

	if err := m.checkFail(StageBeforeDestFinal); err != nil {
		_ = m.fs.Remove(dstVol, stagingRel)
		return err
	}

	// Atomic rename to final name.
	if err := m.fs.RenameWithin(dstVol, stagingRel, destRel); err != nil {
		_ = m.fs.Remove(dstVol, stagingRel)
		return err
	}
	if ctx.Err() != nil {
		_ = m.fs.RemoveAllRel(dstVol, destRel)
		return ErrCancelled
	}
	_ = m.bumpProgress(id, 0, true, srcRel)
	return nil
}

func mustJoin(dir, name string) string {
	rel, err := appfs.JoinRel(dir, name)
	if err != nil {
		return name
	}
	return rel
}

func sanitizeStaging(name string) string {
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return -1
		}
		return r
	}, name)
	if len(name) > 40 {
		name = name[:40]
	}
	if name == "" {
		return "f"
	}
	return name
}

func (m *Manager) bumpProgress(id string, addBytes int64, fileDone bool, current string) error {
	if fileDone {
		_, err := m.db.Exec(`UPDATE transfer_jobs SET bytes_done = bytes_done + ?, files_done = files_done + 1, current_path=?, updated_at=? WHERE id=?`,
			addBytes, current, time.Now().UTC().Format(time.RFC3339), id)
		return err
	}
	_, err := m.db.Exec(`UPDATE transfer_jobs SET bytes_done = bytes_done + ?, current_path=?, updated_at=? WHERE id=?`,
		addBytes, current, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (m *Manager) bumpProgressCount(id string, addBytes, addFiles int64, current string) error {
	_, err := m.db.Exec(
		`UPDATE transfer_jobs
		 SET bytes_done = bytes_done + ?, files_done = files_done + ?, current_path=?, updated_at=?
		 WHERE id=?`,
		addBytes,
		addFiles,
		current,
		time.Now().UTC().Format(time.RFC3339),
		id,
	)
	return err
}

func (m *Manager) resolveConflict(ctx context.Context, id string, live *liveJob, name string) (action, renameTo string, err error) {
	job, err := m.Get(id)
	if err != nil {
		return "", "", err
	}
	policy := job.ConflictPolicy
	if job.ApplyToAll && policy != PolicyPrompt {
		return policy, "", nil
	}
	if policy != PolicyPrompt {
		return policy, "", nil
	}

	now := time.Now().UTC()
	_, _ = m.db.Exec(`UPDATE transfer_jobs SET status=?, conflict_name=?, updated_at=? WHERE id=?`,
		StatusConflict, name, now.Format(time.RFC3339), id)
	if j, err := m.Get(id); err == nil {
		m.publish(*j)
	}

	select {
	case <-ctx.Done():
		return "", "", ErrCancelled
	case reply := <-live.wait:
		if reply.resolution.Action == "cancel" {
			return "", "", ErrCancelled
		}
		action = reply.resolution.Action
		renameTo = reply.resolution.RenameTo
		apply := 0
		if reply.resolution.ApplyToAll {
			apply = 1
		}
		_, _ = m.db.Exec(`UPDATE transfer_jobs SET status=?, conflict_policy=?, apply_to_all=?, conflict_name=NULL, updated_at=? WHERE id=?`,
			StatusRunning, action, apply, time.Now().UTC().Format(time.RFC3339), id)
		if j, err := m.Get(id); err == nil {
			m.publish(*j)
		}
		return action, renameTo, nil
	}
}

func (m *Manager) uniqueName(volumeID, dir, name string) (string, error) {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 10000; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, i, ext)
		rel, err := appfs.JoinRel(dir, candidate)
		if err != nil {
			return "", err
		}
		ok, err := m.fs.Exists(volumeID, rel)
		if err != nil {
			return "", err
		}
		if !ok {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not find unique name for %q", name)
}
