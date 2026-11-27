package settings

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBookmarkStoreOrderAndConflicts(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)
	config := filepath.Join(home, "kubeconfig")
	items, err := store.ReadBookmarks()
	if err != nil || len(items) != 0 {
		t.Fatalf("empty store: %+v %v", items, err)
	}
	for _, svc := range []string{"one", "two", "three"} {
		added, changed, err := store.AddBookmark(t.Context(), Bookmark{KubeconfigPath: config,
			BookmarkTarget: BookmarkTarget{Context: "prod", Namespace: "default", Service: svc}}, nil)
		if err != nil || !changed || added[len(added)-1].Service != svc {
			t.Fatalf("append: %+v %v", added, err)
		}
		items = added
	}
	before := []string{items[0].ID, items[1].ID, items[2].ID}
	order := []string{before[2], before[0], before[1]}
	reordered, changed, err := store.ReorderBookmarks(t.Context(), before, order)
	if err != nil || !changed || reordered[0].ID != before[2] {
		t.Fatalf("reorder: %+v %v", reordered, err)
	}
	current, changed, err := NewStore(home).ReorderBookmarks(t.Context(), before, order)
	if err == nil || changed || !slices.Equal(current, reordered) {
		t.Fatalf("stale reorder: %+v %v", current, err)
	}
	removed, changed, err := store.RemoveBookmark(t.Context(), before[0])
	if err != nil || !changed || !slices.Equal(removed, []Bookmark{items[2], items[1]}) {
		t.Fatalf("remove: %+v %v", removed, err)
	}
	if _, _, err := store.Reset(t.Context()); err != nil {
		t.Fatal(err)
	}
	item := Bookmark{KubeconfigPath: config, BookmarkTarget: BookmarkTarget{Context: "prod", Namespace: "default", Service: "failed"}}
	if _, changed, err := store.AddBookmark(t.Context(), item, func() error { return errors.New("settings changed") }); changed || err == nil {
		t.Fatalf("accepted rejected write: %v", err)
	}
	item.Name = strings.Repeat("x", maxSettingsSize)
	if _, changed, err := store.AddBookmark(t.Context(), item, nil); changed || err == nil {
		t.Fatalf("accepted oversized write: %v", err)
	}
	if current, err := store.ReadBookmarks(); err != nil || !slices.Equal(current, removed) {
		t.Fatalf("failed updates or reset changed store: %+v %v", current, err)
	}
	const invalid = "this is not a bookmark list\n"
	if err := os.WriteFile(store.bookmarksPath(), []byte(invalid), 0600); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := store.RemoveBookmark(t.Context(), before[1]); changed || err == nil {
		t.Fatalf("modified a broken file: %v", err)
	}
	if data, err := os.ReadFile(store.bookmarksPath()); err != nil || string(data) != invalid {
		t.Fatalf("broken file was replaced: %q %v", data, err)
	}
}

func TestExportRefusesBookmarksPath(t *testing.T) {
	home := t.TempDir()
	store := NewStore(home)
	item := Bookmark{KubeconfigPath: filepath.Join(home, "config"),
		BookmarkTarget: BookmarkTarget{Context: "prod", Namespace: "default", Service: "api"}}
	if _, _, err := store.AddBookmark(t.Context(), item, nil); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(home, "linked.yaml")
	if err := os.Symlink(store.bookmarksPath(), alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{store.bookmarksPath(), alias} {
		if err := store.ExportTransfer(path, []TransferField{TransferDomain}); err == nil {
			t.Fatalf("export accepted %s", path)
		}
	}
	if items, err := store.ReadBookmarks(); err != nil || len(items) != 1 || items[0].BookmarkTarget != item.BookmarkTarget {
		t.Fatalf("bookmarks overwritten: %+v %v", items, err)
	}
}
