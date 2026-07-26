package prefs_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/robikorb/dirdeck/backend/internal/db"
	"github.com/robikorb/dirdeck/backend/internal/prefs"
)

func TestFavoritesRecentAndPanes(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store := prefs.New(database)

	fav, err := store.AddFavorite("fixture-ro", "photos", "Photos")
	if err != nil {
		t.Fatal(err)
	}
	if fav.ID == 0 || fav.Label != "Photos" {
		t.Fatalf("%+v", fav)
	}
	_, err = store.AddFavorite("fixture-ro", "photos", "dup")
	if err != prefs.ErrDuplicate {
		t.Fatalf("dup: %v", err)
	}
	list, err := store.ListFavorites()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %#v", err, list)
	}

	if err := store.RecordRecent("fixture-rw", "projects"); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRecent("fixture-rw", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordRecent("fixture-rw", "projects"); err != nil {
		t.Fatal(err)
	}
	recent, err := store.ListRecent()
	if err != nil || len(recent) != 2 {
		t.Fatalf("recent: %v %#v", err, recent)
	}
	if recent[0].Path != "projects" && recent[0].Path != "" {
		t.Fatalf("unexpected order %#v", recent)
	}

	open := true
	saved, err := store.SavePaneState(prefs.PaneState{
		Left:          prefs.PaneSnapshot{VolumeID: "fixture-ro", Path: "photos", View: "grid"},
		Right:         prefs.PaneSnapshot{VolumeID: "fixture-rw", Path: "", View: "list"},
		InspectorOpen: &open,
		ActivePane:    "left",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.GetPaneState()
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if got.Left.Path != "photos" || got.Right.View != "list" || saved.ActivePane != "left" {
		t.Fatalf("%+v", got)
	}

	if err := store.RemoveFavorite(fav.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.ClearRecent(); err != nil {
		t.Fatal(err)
	}
	recent, _ = store.ListRecent()
	if len(recent) != 0 {
		t.Fatalf("expected empty recent")
	}
}

func TestRejectTraversalInPrefs(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store := prefs.New(database)
	if _, err := store.AddFavorite("v", "../x", ""); err != prefs.ErrInvalid {
		t.Fatalf("got %v", err)
	}
	if err := store.RecordRecent("v", "/abs"); err != prefs.ErrInvalid {
		t.Fatalf("got %v", err)
	}
	_, err = store.SavePaneState(prefs.PaneState{
		Left:  prefs.PaneSnapshot{VolumeID: "v", Path: "..", View: "list"},
		Right: prefs.PaneSnapshot{VolumeID: "v", Path: "", View: "list"},
	})
	if err != prefs.ErrInvalid {
		t.Fatalf("got %v", err)
	}
}

func TestPaneStateAcceptsSortAndRejectsUnknownValues(t *testing.T) {
	database, err := db.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store := prefs.New(database)

	saved, err := store.SavePaneState(prefs.PaneState{
		Left:  prefs.PaneSnapshot{VolumeID: "a", Path: "", View: "list", SortKey: "size", SortDir: "desc"},
		Right: prefs.PaneSnapshot{VolumeID: "b", Path: "sub", View: "grid"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Left.SortKey != "size" || saved.Left.SortDir != "desc" {
		t.Fatalf("sort not persisted: %+v", saved.Left)
	}
	// Omitted sort stays empty rather than being invented.
	if saved.Right.SortKey != "" || saved.Right.SortDir != "" {
		t.Fatalf("unexpected sort defaults: %+v", saved.Right)
	}

	loaded, err := store.GetPaneState()
	if err != nil || loaded == nil {
		t.Fatalf("reload: err=%v state=%v", err, loaded)
	}
	if loaded.Left.SortKey != "size" || loaded.Left.SortDir != "desc" {
		t.Fatalf("sort lost on reload: %+v", loaded.Left)
	}

	for _, bad := range []prefs.PaneSnapshot{
		{VolumeID: "a", SortKey: "colour"},
		{VolumeID: "a", SortDir: "sideways"},
	} {
		if _, err := store.SavePaneState(prefs.PaneState{Left: bad}); !errors.Is(err, prefs.ErrInvalid) {
			t.Fatalf("expected ErrInvalid for %+v, got %v", bad, err)
		}
	}
}
