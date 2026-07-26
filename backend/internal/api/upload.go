package api

import (
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"

	appfs "github.com/robikorb/dirdeck/backend/internal/fs"
)

// Upload conflict policies, mirroring the transfer job vocabulary.
const (
	uploadConflictError   = "error"
	uploadConflictSkip    = "skip"
	uploadConflictReplace = "replace"
	uploadConflictRename  = "rename"
)

// maxUploadBytes bounds a single uploaded file. Zero disables the limit; free
// space is checked separately and is the more useful guard on a NAS.
const maxUploadBytes int64 = 0

type uploadResponse struct {
	Skipped  bool        `json:"skipped"`
	Name     string      `json:"name"`
	Meta     *appfs.Meta `json:"meta,omitempty"`
	Conflict bool        `json:"conflict"`
}

// handleUpload streams one file into a destination directory.
//
// One request per file keeps the body a plain stream: no multipart buffering,
// natural per-file progress in the browser, and a cancelled request aborts
// exactly one file.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	volumeID := r.PathValue("id")
	destDir := r.URL.Query().Get("path")
	// Relative subdirectory inside the dropped tree; empty for a plain file drop.
	subDir := strings.TrimSpace(r.URL.Query().Get("dir"))
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	policy := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("conflict")))
	if policy == "" {
		policy = uploadConflictError
	}

	switch policy {
	case uploadConflictError, uploadConflictSkip, uploadConflictReplace, uploadConflictRename:
	default:
		writeError(w, http.StatusBadRequest, "invalid conflict policy")
		return
	}

	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	// Browsers send only the basename, but a crafted client can send anything.
	if name != path.Base(name) || name == "." || name == ".." {
		writeError(w, http.StatusBadRequest, "invalid file name")
		return
	}
	if strings.HasPrefix(name, appfs.UploadStagingPrefix) {
		writeError(w, http.StatusBadRequest, "reserved file name")
		return
	}

	if _, err := s.FS.EnsureWritableVolume(volumeID); err != nil {
		writeFSError(w, err)
		return
	}

	// Folder upload: create the chain the file lives in before writing it. Each
	// component is validated by the same resolver used everywhere else, so a
	// crafted "dir" cannot escape the volume.
	if subDir != "" {
		created, err := s.FS.MkdirAllRel(volumeID, destDir, subDir)
		if err != nil {
			writeFSError(w, err)
			return
		}
		destDir = created
	}

	destRel, err := appfs.JoinRel(destDir, name)
	if err != nil {
		writeFSError(w, err)
		return
	}

	finalName := name
	replace := false
	exists, err := s.FS.Exists(volumeID, destRel)
	if err != nil {
		writeFSError(w, err)
		return
	}
	if exists {
		switch policy {
		case uploadConflictError:
			writeJSON(w, http.StatusConflict, uploadResponse{Name: name, Conflict: true})
			return
		case uploadConflictSkip:
			writeJSON(w, http.StatusOK, uploadResponse{Name: name, Skipped: true})
			return
		case uploadConflictReplace:
			replace = true
		case uploadConflictRename:
			finalName, err = s.uniqueUploadName(volumeID, destDir, name)
			if err != nil {
				writeFSError(w, err)
				return
			}
		}
	}

	// Reject before writing when the declared size cannot fit.
	declared := r.ContentLength
	if declared > 0 {
		if avail, known, ferr := s.FS.FreeSpace(volumeID); ferr == nil && known && declared > avail {
			writeError(w, http.StatusInsufficientStorage,
				fmt.Sprintf("need %d bytes, only %d bytes available", declared, avail))
			return
		}
	}

	// Collect staging files left by a previously killed process.
	s.FS.SweepUploadStaging(volumeID, destDir)

	meta, err := s.FS.SaveUpload(
		r.Context(), volumeID, destDir, finalName,
		r.Body, declared, maxUploadBytes, replace,
	)
	if err != nil {
		writeUploadError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, uploadResponse{Name: finalName, Meta: meta})
}

// uniqueUploadName appends a counter before the extension until the name is free.
func (s *Server) uniqueUploadName(volumeID, dir, name string) (string, error) {
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 10000; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, i, ext)
		rel, err := appfs.JoinRel(dir, candidate)
		if err != nil {
			return "", err
		}
		taken, err := s.FS.Exists(volumeID, rel)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not find a free name for %q", name)
}

func writeUploadError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, appfs.ErrUploadIncomplete):
		// The client went away; nothing was promoted to the final name.
		writeError(w, http.StatusBadRequest, "upload ended before it was complete")
	case errors.Is(err, appfs.ErrExists):
		writeError(w, http.StatusConflict, "destination already exists")
	case errors.Is(err, appfs.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds the upload size limit")
	default:
		writeFSError(w, err)
	}
}
