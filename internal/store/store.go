// Package store defines the persistence layer: domain types, the
// BookmarkStore/SettingsStore interfaces, and the SQLite implementation
// (sqlite.go) backed by internal/db.
package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Bookmark is one tile on the homepage grid: a titled link plus its
// cached favicon state.
type Bookmark struct {
	ID         int64     `json:"id"`
	Title      string    `json:"title"`
	URL        string    `json:"url"`
	Position   int       `json:"position"`
	IconPath   string    `json:"icon_path"`   // file name inside the icons dir; "" when unset
	IconStatus string    `json:"icon_status"` // one of the Icon* constants below
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Icon fetch states for Bookmark.IconStatus.
const (
	IconPending = "pending" // queued for (or mid) fetch
	IconOK      = "ok"      // icon cached at IconPath
	IconNone    = "none"    // site has no usable icon
	IconError   = "error"   // last fetch failed
)

// NewBookmark is the input for creating a bookmark; the store assigns
// the id, position and icon state.
type NewBookmark struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// ErrNotFound is returned (wrapped) when a bookmark id does not exist.
var ErrNotFound = errors.New("not found")

// ErrInvalidOrder is returned (wrapped) by Reorder when ids is not a
// permutation of all existing bookmark ids.
var ErrInvalidOrder = errors.New("invalid order")

// Grid is the page layout: Rows x Cols tiles per page.
type Grid struct {
	Rows int `json:"rows"`
	Cols int `json:"cols"`
}

// Supported grid bounds (docs/PLAN.md).
const (
	gridRowsMin, gridRowsMax = 3, 5
	gridColsMin, gridColsMax = 4, 6
)

// DefaultGrid is used when no grid setting is stored.
var DefaultGrid = Grid{Rows: 4, Cols: 6}

// Validate reports whether the grid is within the supported bounds.
func (g Grid) Validate() error {
	if g.Rows < gridRowsMin || g.Rows > gridRowsMax {
		return fmt.Errorf("grid rows %d: want %d..%d", g.Rows, gridRowsMin, gridRowsMax)
	}
	if g.Cols < gridColsMin || g.Cols > gridColsMax {
		return fmt.Errorf("grid cols %d: want %d..%d", g.Cols, gridColsMin, gridColsMax)
	}
	return nil
}

// Settings holds the user preferences persisted in the settings table.
type Settings struct {
	Grid Grid `json:"grid"`
}

// BookmarkStore is the persistence contract for bookmarks. Mutating
// methods keep positions compact (0..n-1) and run in one transaction.
type BookmarkStore interface {
	// List returns all bookmarks ordered by position, then id.
	List(ctx context.Context) ([]Bookmark, error)
	// Get returns one bookmark, or ErrNotFound.
	Get(ctx context.Context, id int64) (Bookmark, error)
	// Create appends a bookmark after the current last position with a
	// pending icon.
	Create(ctx context.Context, title, url string) (Bookmark, error)
	// Update edits title and URL. A changed URL resets the cached icon to
	// pending so it gets refetched.
	Update(ctx context.Context, id int64, title, url string) (Bookmark, error)
	// Delete removes a bookmark and compacts the remaining positions.
	Delete(ctx context.Context, id int64) error
	// Reorder assigns positions 0..n-1 following ids, which must be a
	// permutation of every existing id (else ErrInvalidOrder).
	Reorder(ctx context.Context, ids []int64) error
	// SetIcon records the icon file name and status for a bookmark.
	SetIcon(ctx context.Context, id int64, path, status string) error
	// ReplaceAll deletes all bookmarks then inserts items in order; the
	// import "replace" mode.
	ReplaceAll(ctx context.Context, items []NewBookmark) ([]Bookmark, error)
	// AppendMany inserts items after the current last position; the
	// import "append" mode.
	AppendMany(ctx context.Context, items []NewBookmark) ([]Bookmark, error)
}

// SettingsStore persists Settings as JSON values keyed by name.
type SettingsStore interface {
	// Get returns the stored settings merged over the defaults.
	Get(ctx context.Context) (Settings, error)
	// Put validates and stores s.
	Put(ctx context.Context, s Settings) error
}

// Store bundles the store interfaces over one database. Both declare a
// Get method with different signatures, so no single type can implement
// both; they are embedded instead — disambiguate the colliding Get via
// s.BookmarkStore.Get / s.SettingsStore.Get.
type Store struct {
	BookmarkStore
	SettingsStore
}
