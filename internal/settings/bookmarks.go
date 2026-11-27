package settings

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"go.yaml.in/yaml/v3"
)

type BookmarkTarget struct {
	Context   string `json:"context" yaml:"context"`
	Namespace string `json:"namespace" yaml:"namespace"`
	Service   string `json:"service" yaml:"service"`
	PortName  string `json:"port_name,omitempty" yaml:"port_name,omitempty"`
}

func (t BookmarkTarget) Valid() bool {
	return t.Context != "" && t.Namespace != "" && t.Service != ""
}

func (t BookmarkTarget) In(items []Bookmark) bool {
	return slices.ContainsFunc(items, func(item Bookmark) bool { return item.BookmarkTarget == t })
}

type Bookmark struct {
	ID             string `json:"id" yaml:"id"`
	Name           string `json:"name,omitempty" yaml:"name,omitempty"`
	KubeconfigPath string `json:"kubeconfig_path" yaml:"kubeconfig_path"`
	BookmarkTarget `yaml:",inline"`
}

func (b Bookmark) valid() bool {
	return b.ID != "" && filepath.IsAbs(b.KubeconfigPath) &&
		filepath.Clean(b.KubeconfigPath) == b.KubeconfigPath && b.BookmarkTarget.Valid()
}

func (s Store) bookmarksPath() string { return filepath.Join(s.Directory(), "bookmarks.yaml") }

func (s Store) ReadBookmarks() ([]Bookmark, error) {
	file, err := openSettingsFile(s.bookmarksPath())
	if errors.Is(err, os.ErrNotExist) {
		return []Bookmark{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open bookmarks: %w", err)
	}
	defer file.Close()
	decoder := yaml.NewDecoder(io.LimitReader(file, maxSettingsSize+1))
	decoder.KnownFields(true)
	items := []Bookmark{}
	if err := decoder.Decode(&items); err != nil {
		return nil, fmt.Errorf("read bookmarks: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("The bookmarks file must contain one YAML document.")
	}
	ids := make(map[string]bool, len(items))
	targets := make(map[BookmarkTarget]bool, len(items))
	for _, item := range items {
		if !item.valid() || ids[item.ID] || targets[item.BookmarkTarget] {
			return nil, errors.New("The bookmarks file contains an invalid or duplicate item.")
		}
		ids[item.ID], targets[item.BookmarkTarget] = true, true
	}
	return items, nil
}

// AddBookmark checks uniqueness under the same lock as the write. verify may
// recheck local settings under the lock.
func (s Store) AddBookmark(ctx context.Context, item Bookmark, verify func() error) ([]Bookmark, bool, error) {
	return s.updateBookmarks(ctx, func(items []Bookmark) ([]Bookmark, error) {
		if item.BookmarkTarget.In(items) {
			return items, nil
		}
		item.ID = rand.Text()
		if !item.valid() {
			return items, errors.New("Choose a context, namespace, and service.")
		}
		if verify != nil {
			if err := verify(); err != nil {
				return items, err
			}
		}
		return append(items, item), nil
	})
}

func (s Store) RemoveBookmark(ctx context.Context, id string) ([]Bookmark, bool, error) {
	return s.updateBookmarks(ctx, func(items []Bookmark) ([]Bookmark, error) {
		index := slices.IndexFunc(items, func(item Bookmark) bool { return item.ID == id })
		if index < 0 {
			return items, nil
		}
		return slices.Delete(items, index, index+1), nil
	})
}

func (s Store) ReorderBookmarks(ctx context.Context, before, order []string) ([]Bookmark, bool, error) {
	return s.updateBookmarks(ctx, func(items []Bookmark) ([]Bookmark, error) {
		if len(items) != len(before) {
			return items, errors.New("The bookmark list changed. Review the current order and try again.")
		}
		byID := make(map[string]Bookmark, len(items))
		for index, item := range items {
			if item.ID != before[index] {
				return items, errors.New("The bookmark list changed. Review the current order and try again.")
			}
			byID[item.ID] = item
		}
		if len(order) != len(items) {
			return items, errors.New("The new order must include every bookmark.")
		}
		reordered := make([]Bookmark, 0, len(items))
		for _, id := range order {
			item, ok := byID[id]
			if !ok {
				return items, errors.New("The new order must include every bookmark once.")
			}
			reordered = append(reordered, item)
			delete(byID, id)
		}
		return reordered, nil
	})
}

func (s Store) updateBookmarks(ctx context.Context, change func([]Bookmark) ([]Bookmark, error)) ([]Bookmark, bool, error) {
	lock, err := s.lock(ctx)
	if err != nil {
		return nil, false, err
	}
	defer lock.Close()
	items, err := s.ReadBookmarks()
	if err != nil {
		return nil, false, err
	}
	updated, err := change(slices.Clone(items))
	if err != nil || slices.Equal(items, updated) {
		return items, false, err
	}
	data, err := yaml.Marshal(updated)
	if err != nil {
		return items, false, err
	}
	if len(data) > maxSettingsSize {
		return items, false, errors.New("The bookmarks file exceeds 1 MiB.")
	}
	if err := ctx.Err(); err != nil {
		return items, false, err
	}
	if err := s.replaceFile(s.bookmarksPath(), data); err != nil {
		return items, false, fmt.Errorf("save bookmarks: %w", err)
	}
	return updated, true, nil
}
