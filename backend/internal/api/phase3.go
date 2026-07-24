package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

	appfs "github.com/liquid-glass-file-manager/backend/internal/fs"
	"github.com/liquid-glass-file-manager/backend/internal/prefs"
	"github.com/liquid-glass-file-manager/backend/internal/preview"
)

func (s *Server) handleVolumes(w http.ResponseWriter, _ *http.Request) {
	vols := s.Volumes.ProbePublic(func(id string) bool {
		if s.FS == nil {
			return false
		}
		return s.FS.Available(id)
	})
	writeJSON(w, http.StatusOK, map[string]any{"volumes": vols})
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rel := r.URL.Query().Get("path")
	result, err := s.FS.ListDetailed(id, rel)
	if err != nil {
		writeFSError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries":   result.Entries,
		"truncated": result.Truncated,
	})
}

func (s *Server) handleThumbnail(w http.ResponseWriter, r *http.Request) {
	if s.Preview == nil {
		writeError(w, http.StatusServiceUnavailable, "previews unavailable")
		return
	}
	id := r.PathValue("id")
	rel := r.URL.Query().Get("path")
	out, err := s.Preview.Thumbnail(r.Context(), id, rel)
	if err != nil {
		writePreviewError(w, err)
		return
	}
	w.Header().Set("Content-Type", out.ContentType)
	w.Header().Set("Cache-Control", "private, max-age=60")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out.Bytes)
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rel := r.URL.Query().Get("path")
	kindQ := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("kind")))

	if !preview.IsPreviewable(rel) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported media type")
		return
	}

	textLike := preview.IsTextLike(rel)
	wantText := textLike
	if kindQ != "" && kindQ != "auto" {
		switch kindQ {
		case "text", "markdown", "json", "code", "docx":
			if !textLike {
				writeError(w, http.StatusUnsupportedMediaType, "unsupported preview type")
				return
			}
			wantText = true
		case "media", "image":
			wantText = false
		default:
			writeError(w, http.StatusBadRequest, "invalid kind")
			return
		}
	}

	if wantText {
		if s.Preview == nil {
			writeError(w, http.StatusServiceUnavailable, "previews unavailable")
			return
		}
		out, err := s.Preview.TextPreview(r.Context(), id, rel)
		if err != nil {
			writePreviewError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "private, max-age=60")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Preview-Kind", out.Kind)
		if out.Truncated {
			w.Header().Set("X-Preview-Truncated", "true")
		}
		writeJSON(w, http.StatusOK, out)
		return
	}

	if !preview.IsMediaPreviewable(rel) {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported media type")
		return
	}

	res, err := s.FS.OpenFile(id, rel)
	if err != nil {
		writeFSError(w, err)
		return
	}
	defer res.Close()
	if res.File == nil {
		writeFSError(w, appfs.ErrNotFound)
		return
	}
	ct, ok := preview.ContentTypeForExt(path.Ext(res.RelPath))
	if !ok {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported media type")
		return
	}
	// Enforce the same encoded-byte ceiling as thumbnails for image previews.
	if preview.IsImageExt(path.Ext(res.RelPath)) && s.Preview != nil {
		if res.Info.Size() > s.Preview.Limits().MaxBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "image exceeds size limit")
			return
		}
	}
	name := path.Base(res.RelPath)
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", `inline; filename="`+sanitizeFilename(name)+`"`)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "private, max-age=60")
	http.ServeContent(w, r, name, res.Info.ModTime(), res.File)
}

func (s *Server) handleFavoritesList(w http.ResponseWriter, _ *http.Request) {
	if s.Prefs == nil {
		writeError(w, http.StatusServiceUnavailable, "preferences unavailable")
		return
	}
	list, err := s.Prefs.ListFavorites()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if list == nil {
		list = []prefs.Favorite{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"favorites": list})
}

type favoriteRequest struct {
	VolumeID string `json:"volumeId"`
	Path     string `json:"path"`
	Label    string `json:"label"`
}

func (s *Server) handleFavoriteCreate(w http.ResponseWriter, r *http.Request) {
	if s.Prefs == nil {
		writeError(w, http.StatusServiceUnavailable, "preferences unavailable")
		return
	}
	var req favoriteRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	// Ensure the location is within a known volume (path may be empty = root).
	if _, ok := s.Volumes.Get(req.VolumeID); !ok {
		writeError(w, http.StatusNotFound, "volume not found")
		return
	}
	if _, err := s.FS.Stat(req.VolumeID, req.Path); err != nil {
		// Allow favoring a path that was available; still reject escapes.
		if errors.Is(err, appfs.ErrInvalidPath) || errors.Is(err, appfs.ErrEscape) {
			writeFSError(w, err)
			return
		}
		if !errors.Is(err, appfs.ErrUnavailable) && !errors.Is(err, appfs.ErrNotFound) {
			writeFSError(w, err)
			return
		}
		// Not found / unavailable: still validate path shape via Stat on root+rel
		// by requiring normalize through a probe resolve of parent — reject bad paths only.
		if errors.Is(err, appfs.ErrNotFound) {
			writeError(w, http.StatusNotFound, "path not found")
			return
		}
	}
	fav, err := s.Prefs.AddFavorite(req.VolumeID, req.Path, req.Label)
	if err != nil {
		writePrefsError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, fav)
}

func (s *Server) handleFavoriteDelete(w http.ResponseWriter, r *http.Request) {
	if s.Prefs == nil {
		writeError(w, http.StatusServiceUnavailable, "preferences unavailable")
		return
	}
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.Prefs.RemoveFavorite(id); err != nil {
		writePrefsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRecentList(w http.ResponseWriter, _ *http.Request) {
	if s.Prefs == nil {
		writeError(w, http.StatusServiceUnavailable, "preferences unavailable")
		return
	}
	list, err := s.Prefs.ListRecent()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if list == nil {
		list = []prefs.RecentLocation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"recent": list})
}

type recentRequest struct {
	VolumeID string `json:"volumeId"`
	Path     string `json:"path"`
}

func (s *Server) handleRecentRecord(w http.ResponseWriter, r *http.Request) {
	if s.Prefs == nil {
		writeError(w, http.StatusServiceUnavailable, "preferences unavailable")
		return
	}
	var req recentRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if _, ok := s.Volumes.Get(req.VolumeID); !ok {
		writeError(w, http.StatusNotFound, "volume not found")
		return
	}
	if err := s.Prefs.RecordRecent(req.VolumeID, req.Path); err != nil {
		writePrefsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRecentClear(w http.ResponseWriter, _ *http.Request) {
	if s.Prefs == nil {
		writeError(w, http.StatusServiceUnavailable, "preferences unavailable")
		return
	}
	if err := s.Prefs.ClearRecent(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handlePaneStateGet(w http.ResponseWriter, _ *http.Request) {
	if s.Prefs == nil {
		writeError(w, http.StatusServiceUnavailable, "preferences unavailable")
		return
	}
	st, err := s.Prefs.GetPaneState()
	if err != nil {
		writePrefsError(w, err)
		return
	}
	if st == nil {
		writeJSON(w, http.StatusOK, map[string]any{"paneState": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"paneState": st})
}

func (s *Server) handlePaneStatePut(w http.ResponseWriter, r *http.Request) {
	if s.Prefs == nil {
		writeError(w, http.StatusServiceUnavailable, "preferences unavailable")
		return
	}
	var st prefs.PaneState
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&st); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	saved, err := s.Prefs.SavePaneState(st)
	if err != nil {
		writePrefsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"paneState": saved})
}

func writePreviewError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, preview.ErrDisabled):
		writeError(w, http.StatusForbidden, "thumbnails disabled for volume")
	case errors.Is(err, preview.ErrUnsupported):
		writeError(w, http.StatusUnsupportedMediaType, "unsupported preview type")
	case errors.Is(err, preview.ErrBinary):
		writeError(w, http.StatusUnsupportedMediaType, "file appears to be binary")
	case errors.Is(err, preview.ErrTooLarge), errors.Is(err, preview.ErrDocxHuge):
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds size limit")
	case errors.Is(err, preview.ErrTooManyPixels):
		writeError(w, http.StatusRequestEntityTooLarge, "image exceeds pixel limit")
	case errors.Is(err, preview.ErrTimeout):
		writeError(w, http.StatusGatewayTimeout, "image decode timed out")
	case errors.Is(err, preview.ErrBusy):
		writeError(w, http.StatusTooManyRequests, "thumbnail concurrency limit reached")
	case errors.Is(err, preview.ErrDocx):
		writeError(w, http.StatusUnprocessableEntity, "docx preview failed")
	default:
		writeFSError(w, err)
	}
}

func writePrefsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, prefs.ErrInvalid):
		writeError(w, http.StatusBadRequest, "invalid request")
	case errors.Is(err, prefs.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, prefs.ErrDuplicate):
		writeError(w, http.StatusConflict, "already exists")
	default:
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}