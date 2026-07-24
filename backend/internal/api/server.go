package api

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/liquid-glass-file-manager/backend/internal/auth"
	appfs "github.com/liquid-glass-file-manager/backend/internal/fs"
	"github.com/liquid-glass-file-manager/backend/internal/prefs"
	"github.com/liquid-glass-file-manager/backend/internal/preview"
	"github.com/liquid-glass-file-manager/backend/internal/transfer"
	"github.com/liquid-glass-file-manager/backend/internal/volumes"
)

// Server wires HTTP routes.
type Server struct {
	Auth      *auth.Service
	FS        *appfs.Service
	Volumes   *volumes.Registry
	Transfers *transfer.Manager
	Preview   *preview.Service
	Prefs     *prefs.Store
	Static    fs.FS
	Ready     bool
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/ready", s.handleReady)
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.requireAuth(s.requireCSRF(s.handleLogout)))
	mux.HandleFunc("GET /api/auth/session", s.handleSession)
	mux.HandleFunc("GET /api/volumes", s.requireAuth(s.handleVolumes))
	mux.HandleFunc("GET /api/volumes/{id}/list", s.requireAuth(s.handleList))
	mux.HandleFunc("GET /api/volumes/{id}/stat", s.requireAuth(s.handleStat))
	mux.HandleFunc("GET /api/volumes/{id}/download", s.requireAuth(s.handleDownload))
	mux.HandleFunc("GET /api/volumes/{id}/thumbnail", s.requireAuth(s.handleThumbnail))
	mux.HandleFunc("GET /api/volumes/{id}/preview", s.requireAuth(s.handlePreview))
	mux.HandleFunc("GET /api/volumes/{id}/content", s.requireAuth(s.handleContentGet))
	mux.HandleFunc("PUT /api/volumes/{id}/content", s.requireAuth(s.requireCSRF(s.handleContentPut)))
	mux.HandleFunc("POST /api/volumes/{id}/rename", s.requireAuth(s.requireCSRF(s.handleRename)))
	mux.HandleFunc("DELETE /api/volumes/{id}/entry", s.requireAuth(s.requireCSRF(s.handleDeleteEntry)))

	mux.HandleFunc("GET /api/favorites", s.requireAuth(s.handleFavoritesList))
	mux.HandleFunc("POST /api/favorites", s.requireAuth(s.requireCSRF(s.handleFavoriteCreate)))
	mux.HandleFunc("DELETE /api/favorites/{id}", s.requireAuth(s.requireCSRF(s.handleFavoriteDelete)))

	mux.HandleFunc("GET /api/recent", s.requireAuth(s.handleRecentList))
	mux.HandleFunc("POST /api/recent", s.requireAuth(s.requireCSRF(s.handleRecentRecord)))
	mux.HandleFunc("DELETE /api/recent", s.requireAuth(s.requireCSRF(s.handleRecentClear)))

	mux.HandleFunc("GET /api/preferences/panes", s.requireAuth(s.handlePaneStateGet))
	mux.HandleFunc("PUT /api/preferences/panes", s.requireAuth(s.requireCSRF(s.handlePaneStatePut)))

	mux.HandleFunc("GET /api/transfers", s.requireAuth(s.handleTransfersList))
	mux.HandleFunc("POST /api/transfers", s.requireAuth(s.requireCSRF(s.handleTransferCreate)))
	mux.HandleFunc("GET /api/transfers/events", s.requireAuth(s.handleTransferEvents))
	mux.HandleFunc("GET /api/transfers/{id}", s.requireAuth(s.handleTransferGet))
	mux.HandleFunc("POST /api/transfers/{id}/cancel", s.requireAuth(s.requireCSRF(s.handleTransferCancel)))
	mux.HandleFunc("POST /api/transfers/{id}/conflict", s.requireAuth(s.requireCSRF(s.handleTransferConflict)))

	if s.Static != nil {
		fileServer := http.FileServer(http.FS(s.Static))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				http.NotFound(w, r)
				return
			}
			p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
			if p == "" {
				p = "index.html"
			}
			if f, err := s.Static.Open(p); err == nil {
				_ = f.Close()
				setStaticCacheHeaders(w, p)
				fileServer.ServeHTTP(w, r)
				return
			}
			// SPA fallback: always serve a fresh index.html so rebuilds pick up new asset hashes.
			r.URL.Path = "/"
			setStaticCacheHeaders(w, "index.html")
			fileServer.ServeHTTP(w, r)
		})
	}

	return withSecurityHeaders(mux)
}

func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// setStaticCacheHeaders keeps the HTML shell revalidating after rebuilds while allowing
// long-lived caching of Vite content-hashed assets under /assets/.
func setStaticCacheHeaders(w http.ResponseWriter, p string) {
	if p == "index.html" || !strings.HasPrefix(p, "assets/") {
		w.Header().Set("Cache-Control", "no-cache")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReady(w http.ResponseWriter, _ *http.Request) {
	if !s.Ready {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	sess, err := s.Auth.Login(w, r, req.Username, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrRateLimited):
			writeError(w, http.StatusTooManyRequests, "too many login attempts")
		default:
			writeError(w, http.StatusUnauthorized, "invalid credentials")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"username":  sess.Username,
		"csrfToken": sess.CSRFToken,
		"expiresAt": sess.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	_ = s.Auth.Logout(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	sess, err := s.Auth.SessionFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"username":      sess.Username,
		"csrfToken":     sess.CSRFToken,
		"expiresAt":     sess.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleStat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rel := r.URL.Query().Get("path")
	meta, err := s.FS.Stat(id, rel)
	if err != nil {
		writeFSError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rel := r.URL.Query().Get("path")
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
	name := path.Base(res.RelPath)
	if name == "" || name == "." {
		name = "download"
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+sanitizeFilename(name)+`"`)
	http.ServeContent(w, r, name, res.Info.ModTime(), res.File)
}

func (s *Server) handleTransfersList(w http.ResponseWriter, r *http.Request) {
	if s.Transfers == nil {
		writeError(w, http.StatusServiceUnavailable, "transfers unavailable")
		return
	}
	jobs, err := s.Transfers.List(50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if jobs == nil {
		jobs = []transfer.Job{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"transfers": jobs})
}

func (s *Server) handleTransferGet(w http.ResponseWriter, r *http.Request) {
	if s.Transfers == nil {
		writeError(w, http.StatusServiceUnavailable, "transfers unavailable")
		return
	}
	job, err := s.Transfers.Get(r.PathValue("id"))
	if err != nil {
		writeTransferError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleTransferCreate(w http.ResponseWriter, r *http.Request) {
	if s.Transfers == nil {
		writeError(w, http.StatusServiceUnavailable, "transfers unavailable")
		return
	}
	var req transfer.CreateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	job, err := s.Transfers.Create(req)
	if err != nil {
		writeTransferError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, job)
}

func (s *Server) handleTransferCancel(w http.ResponseWriter, r *http.Request) {
	if s.Transfers == nil {
		writeError(w, http.StatusServiceUnavailable, "transfers unavailable")
		return
	}
	job, err := s.Transfers.Cancel(r.PathValue("id"))
	if err != nil {
		writeTransferError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleTransferConflict(w http.ResponseWriter, r *http.Request) {
	if s.Transfers == nil {
		writeError(w, http.StatusServiceUnavailable, "transfers unavailable")
		return
	}
	var res transfer.ConflictResolution
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&res); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	job, err := s.Transfers.ResolveConflict(r.PathValue("id"), res)
	if err != nil {
		writeTransferError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleTransferEvents(w http.ResponseWriter, r *http.Request) {
	if s.Transfers == nil {
		writeError(w, http.StatusServiceUnavailable, "transfers unavailable")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	sub := s.Transfers.Subscribe()
	defer s.Transfers.Unsubscribe(sub)

	// Initial snapshot so reconnecting clients catch up without relying on missed events.
	if jobs, err := s.Transfers.List(20); err == nil {
		for _, job := range jobs {
			writeSSE(w, flusher, "transfer", job)
		}
	}

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case job, ok := <-sub:
			if !ok {
				return
			}
			writeSSE(w, flusher, "transfer", job)
		case <-ticker.C:
			writeSSE(w, flusher, "ping", map[string]string{})
		}
	}
}

func writeSSE(w http.ResponseWriter, flusher http.Flusher, event string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	_, _ = w.Write([]byte("event: " + event + "\n"))
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n\n"))
	flusher.Flush()
}

func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, `"`, "")
	name = strings.ReplaceAll(name, "\n", "")
	name = strings.ReplaceAll(name, "\r", "")
	return name
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.Auth.SessionFromRequest(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r.WithContext(auth.WithSession(r.Context(), sess)))
	}
}

func (s *Server) requireCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := auth.SessionFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if err := s.Auth.RequireCSRF(r, sess); err != nil {
			writeError(w, http.StatusForbidden, "csrf validation failed")
			return
		}
		next(w, r)
	}
}

func writeTransferError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, transfer.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, transfer.ErrInvalidRequest):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, transfer.ErrInsufficientSpace):
		writeError(w, http.StatusInsufficientStorage, err.Error())
	case errors.Is(err, appfs.ErrReadOnly):
		writeError(w, http.StatusForbidden, "volume is read-only")
	case errors.Is(err, appfs.ErrExists):
		writeError(w, http.StatusConflict, "destination already exists")
	case errors.Is(err, appfs.ErrChanged):
		writeError(w, http.StatusConflict, "file changed since it was opened")
	case errors.Is(err, appfs.ErrTooLarge):
		writeError(w, http.StatusRequestEntityTooLarge, "file exceeds editor size limit")
	case errors.Is(err, appfs.ErrInvalidText):
		writeError(w, http.StatusUnsupportedMediaType, "file is not valid UTF-8 text")
	case errors.Is(err, appfs.ErrNotFile):
		writeError(w, http.StatusBadRequest, "not a regular file")
	case errors.Is(err, appfs.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, appfs.ErrInvalidPath), errors.Is(err, appfs.ErrEscape), errors.Is(err, appfs.ErrSymlinkFollow):
		writeError(w, http.StatusBadRequest, "invalid path")
	default:
		writeFSError(w, err)
	}
}

func writeFSError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, appfs.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, appfs.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "volume unavailable")
	case errors.Is(err, appfs.ErrInvalidPath), errors.Is(err, appfs.ErrEscape), errors.Is(err, appfs.ErrSymlinkFollow):
		writeError(w, http.StatusBadRequest, "invalid path")
	case errors.Is(err, appfs.ErrNotDirectory):
		writeError(w, http.StatusBadRequest, "not a directory")
	case errors.Is(err, appfs.ErrIsDirectory):
		writeError(w, http.StatusBadRequest, "is a directory")
	case errors.Is(err, appfs.ErrReadOnly):
		writeError(w, http.StatusForbidden, "volume is read-only")
	case errors.Is(err, appfs.ErrChanged):
		writeError(w, http.StatusConflict, "file changed during operation")
	case errors.Is(err, appfs.ErrMountBoundary):
		writeError(w, http.StatusConflict, "refusing to delete a mounted filesystem")
	case os.IsPermission(err):
		writeError(w, http.StatusForbidden, "permission denied")
	default:
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ParseByteRange is kept for clarity; ServeContent handles Range.
var _ = strconv.Atoi
