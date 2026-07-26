// Package prefs stores favorites, recent locations, and pane UI state in SQLite.
package prefs

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrInvalid   = errors.New("invalid preference")
	ErrNotFound  = errors.New("not found")
	ErrDuplicate = errors.New("already exists")
)

const (
	maxFavorites = 50
	maxRecent    = 30
	paneStateKey = "pane_state"
)

// Favorite is a saved location.
type Favorite struct {
	ID        int64     `json:"id"`
	VolumeID  string    `json:"volumeId"`
	Path      string    `json:"path"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"createdAt"`
}

// RecentLocation is a recently visited directory.
type RecentLocation struct {
	VolumeID  string    `json:"volumeId"`
	Path      string    `json:"path"`
	VisitedAt time.Time `json:"visitedAt"`
}

// PaneSnapshot is the persisted state for one browser pane.
type PaneSnapshot struct {
	VolumeID string `json:"volumeId"`
	Path     string `json:"path"`
	View     string `json:"view"` // list | grid
	// Sort is a UI preference stored verbatim; unknown values are ignored by the
	// client, and older rows simply omit these fields.
	SortKey string `json:"sortKey,omitempty"` // name | modified | size
	SortDir string `json:"sortDir,omitempty"` // asc | desc
}

// PaneState is the full dual-pane UI persistence payload.
type PaneState struct {
	Left          PaneSnapshot `json:"left"`
	Right         PaneSnapshot `json:"right"`
	InspectorOpen *bool        `json:"inspectorOpen,omitempty"`
	ActivePane    string       `json:"activePane,omitempty"` // left | right
	UpdatedAt     time.Time    `json:"updatedAt,omitempty"`
}

// Store persists user preferences.
type Store struct {
	db *sql.DB
}

// New creates a preference store.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// ListFavorites returns favorites newest-first.
func (s *Store) ListFavorites() ([]Favorite, error) {
	rows, err := s.db.Query(`
		SELECT id, volume_id, path, label, created_at
		FROM favorites
		ORDER BY created_at DESC, id DESC
		LIMIT ?`, maxFavorites)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Favorite, 0)
	for rows.Next() {
		var f Favorite
		var created string
		if err := rows.Scan(&f.ID, &f.VolumeID, &f.Path, &f.Label, &created); err != nil {
			return nil, err
		}
		f.CreatedAt = parseTime(created)
		out = append(out, f)
	}
	return out, rows.Err()
}

// AddFavorite inserts a favorite. Empty path means volume root.
func (s *Store) AddFavorite(volumeID, relPath, label string) (*Favorite, error) {
	volumeID = strings.TrimSpace(volumeID)
	if volumeID == "" || strings.ContainsAny(volumeID, `/\`) {
		return nil, ErrInvalid
	}
	relPath, err := normalizePrefPath(relPath)
	if err != nil {
		return nil, err
	}
	label = strings.TrimSpace(label)
	if len(label) > 200 {
		label = label[:200]
	}
	if label == "" {
		if relPath == "" {
			label = volumeID
		} else {
			label = relPath
		}
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.Exec(`
		INSERT INTO favorites(volume_id, path, label, created_at)
		VALUES(?, ?, ?, ?)`, volumeID, relPath, label, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Favorite{
		ID:        id,
		VolumeID:  volumeID,
		Path:      relPath,
		Label:     label,
		CreatedAt: parseTime(now),
	}, nil
}

// RemoveFavorite deletes by id.
func (s *Store) RemoveFavorite(id int64) error {
	res, err := s.db.Exec(`DELETE FROM favorites WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RemoveFavoriteByLocation deletes by volume + path.
func (s *Store) RemoveFavoriteByLocation(volumeID, relPath string) error {
	relPath, err := normalizePrefPath(relPath)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`DELETE FROM favorites WHERE volume_id = ? AND path = ?`, volumeID, relPath)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListRecent returns recent locations newest-first.
func (s *Store) ListRecent() ([]RecentLocation, error) {
	rows, err := s.db.Query(`
		SELECT volume_id, path, visited_at
		FROM recent_locations
		ORDER BY visited_at DESC
		LIMIT ?`, maxRecent)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RecentLocation, 0)
	for rows.Next() {
		var r RecentLocation
		var visited string
		if err := rows.Scan(&r.VolumeID, &r.Path, &visited); err != nil {
			return nil, err
		}
		r.VisitedAt = parseTime(visited)
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecordRecent upserts a recent location and prunes old rows.
func (s *Store) RecordRecent(volumeID, relPath string) error {
	volumeID = strings.TrimSpace(volumeID)
	if volumeID == "" || strings.ContainsAny(volumeID, `/\`) {
		return ErrInvalid
	}
	relPath, err := normalizePrefPath(relPath)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		INSERT INTO recent_locations(volume_id, path, visited_at)
		VALUES(?, ?, ?)
		ON CONFLICT(volume_id, path) DO UPDATE SET visited_at = excluded.visited_at
	`, volumeID, relPath, now); err != nil {
		return err
	}
	// Prune beyond maxRecent.
	if _, err := tx.Exec(`
		DELETE FROM recent_locations
		WHERE id NOT IN (
			SELECT id FROM recent_locations
			ORDER BY visited_at DESC
			LIMIT ?
		)`, maxRecent); err != nil {
		return err
	}
	return tx.Commit()
}

// ClearRecent removes all recent locations.
func (s *Store) ClearRecent() error {
	_, err := s.db.Exec(`DELETE FROM recent_locations`)
	return err
}

// GetPaneState loads persisted pane state, or nil if unset.
func (s *Store) GetPaneState() (*PaneState, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM preferences WHERE key = ?`, paneStateKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st PaneState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		return nil, fmt.Errorf("%w: corrupt pane state", ErrInvalid)
	}
	return &st, nil
}

// SavePaneState validates and persists pane state.
func (s *Store) SavePaneState(st PaneState) (*PaneState, error) {
	if err := validatePaneSnapshot(st.Left); err != nil {
		return nil, err
	}
	if err := validatePaneSnapshot(st.Right); err != nil {
		return nil, err
	}
	if st.ActivePane != "" && st.ActivePane != "left" && st.ActivePane != "right" {
		return nil, ErrInvalid
	}
	st.UpdatedAt = time.Now().UTC()
	raw, err := json.Marshal(st)
	if err != nil {
		return nil, err
	}
	now := st.UpdatedAt.Format(time.RFC3339Nano)
	_, err = s.db.Exec(`
		INSERT INTO preferences(key, value, updated_at) VALUES(?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, paneStateKey, string(raw), now)
	if err != nil {
		return nil, err
	}
	return &st, nil
}

func validatePaneSnapshot(p PaneSnapshot) error {
	if p.VolumeID != "" && strings.ContainsAny(p.VolumeID, `/\`) {
		return ErrInvalid
	}
	if _, err := normalizePrefPath(p.Path); err != nil {
		return err
	}
	if p.View != "" && p.View != "list" && p.View != "grid" {
		return ErrInvalid
	}
	switch p.SortKey {
	case "", "name", "modified", "size":
	default:
		return ErrInvalid
	}
	switch p.SortDir {
	case "", "asc", "desc":
	default:
		return ErrInvalid
	}
	return nil
}

func normalizePrefPath(rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" || rel == "." || rel == "/" {
		return "", nil
	}
	if strings.ContainsRune(rel, 0) || strings.HasPrefix(rel, "/") || strings.Contains(rel, `\`) {
		return "", ErrInvalid
	}
	parts := strings.Split(rel, "/")
	clean := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" || p == "." {
			continue
		}
		if p == ".." {
			return "", ErrInvalid
		}
		clean = append(clean, p)
	}
	return strings.Join(clean, "/"), nil
}

func parseTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}
