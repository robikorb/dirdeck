package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	appfs "github.com/robikorb/dirdeck/backend/internal/fs"
	"github.com/robikorb/dirdeck/backend/internal/preview"
)

const maxEditorBytes int64 = 2 << 20

type editorContent struct {
	Content  string    `json:"content"`
	Language string    `json:"language"`
	Size     int64     `json:"size"`
	ModTime  time.Time `json:"modTime"`
}

func editorLanguage(rel string) (string, bool) {
	kind, language := preview.ClassifyTextPath(rel)
	if kind == "" || kind == "docx" {
		return "", false
	}
	if language == "" {
		language = "plaintext"
	}
	return language, true
}

func (s *Server) handleContentGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rel := r.URL.Query().Get("path")
	language, ok := editorLanguage(rel)
	if !ok {
		writeError(w, http.StatusUnsupportedMediaType, "file type is not editable")
		return
	}
	data, meta, err := s.FS.ReadTextFile(id, rel, maxEditorBytes)
	if err != nil {
		writeFSError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, editorContent{
		Content:  string(data),
		Language: language,
		Size:     meta.Size,
		ModTime:  meta.ModTime,
	})
}

type contentPutRequest struct {
	Content         string `json:"content"`
	ExpectedModTime string `json:"expectedModTime"`
}

func (s *Server) handleContentPut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	rel := r.URL.Query().Get("path")
	if _, ok := editorLanguage(rel); !ok {
		writeError(w, http.StatusUnsupportedMediaType, "file type is not editable")
		return
	}
	var req contentPutRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxEditorBytes*8)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var expected time.Time
	if strings.TrimSpace(req.ExpectedModTime) != "" {
		parsed, err := time.Parse(time.RFC3339Nano, req.ExpectedModTime)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid expectedModTime")
			return
		}
		expected = parsed
	}
	meta, err := s.FS.WriteTextFile(id, rel, []byte(req.Content), maxEditorBytes, expected)
	if err != nil {
		writeFSError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

type renameRequest struct {
	Path    string `json:"path"`
	NewName string `json:"newName"`
}

func (s *Server) handleRename(w http.ResponseWriter, r *http.Request) {
	var req renameRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	meta, err := s.FS.RenameEntry(r.PathValue("id"), req.Path, req.NewName)
	if err != nil {
		writeFSError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

type deleteRequest struct {
	Path  string   `json:"path"`
	Paths []string `json:"paths"`
}

func (s *Server) handleDeleteEntry(w http.ResponseWriter, r *http.Request) {
	var req deleteRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	paths := req.Paths
	if len(paths) == 0 {
		paths = []string{req.Path}
	}
	if err := s.FS.DeleteEntries(r.PathValue("id"), paths); err != nil {
		writeFSError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type createFolderRequest struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// handleCreateFolder creates one directory inside an existing folder.
//
// fs.Mkdir treats an existing directory as success because recursive copy reuses
// the chain. For a user action that would be wrong: "New folder" on a name that
// is already taken would report success and appear to do nothing. The existence
// check makes the collision explicit.
func (s *Server) handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	volumeID := r.PathValue("id")
	var req createFolderRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if _, err := s.FS.EnsureWritableVolume(volumeID); err != nil {
		writeFSError(w, err)
		return
	}
	// Validates the name as a single component: separators, dot segments, and
	// NUL bytes are rejected here.
	rel, err := appfs.JoinRel(req.Path, req.Name)
	if err != nil {
		writeFSError(w, err)
		return
	}
	exists, err := s.FS.Exists(volumeID, rel)
	if err != nil {
		writeFSError(w, err)
		return
	}
	if exists {
		writeError(w, http.StatusConflict, "a file or folder with that name already exists")
		return
	}
	created, err := s.FS.Mkdir(volumeID, req.Path, req.Name)
	if err != nil {
		writeFSError(w, err)
		return
	}
	meta, err := s.FS.Stat(volumeID, created)
	if err != nil {
		writeFSError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, meta)
}
