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
// cached favicon state. Page (0-based) and Position (0-based within the
// page) give its slot on the board.
type Bookmark struct {
	ID         int64     `json:"id"`
	Title      string    `json:"title"`
	URL        string    `json:"url"`
	Page       int       `json:"page"`
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

// NewBookmark is the input for creating or importing a bookmark; the
// store assigns the id and icon state. Page/Position carry the layout
// info an export file may hold: a non-nil Page places the bookmark on
// its recorded page on a replace import; nil means "no page info".
type NewBookmark struct {
	Title    string `json:"title"`
	URL      string `json:"url"`
	Page     *int   `json:"page,omitempty"`
	Position int    `json:"position"`
}

// ErrNotFound is returned (wrapped) when a bookmark id does not exist.
var ErrNotFound = errors.New("not found")

// ErrInvalidMove is returned (wrapped) by Move when the target slot is
// not reachable: a negative page or index, or a page beyond
// 0..len(pages) (len itself means a new last page).
var ErrInvalidMove = errors.New("invalid move")

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

// Spacing values for Settings.Spacing: the tile density presets.
const (
	SpacingCompact = "compact"
	SpacingNormal  = "normal"
	SpacingRelaxed = "relaxed"
)

// DefaultSpacing is used when no spacing setting is stored or the
// stored value is invalid.
const DefaultSpacing = SpacingNormal

// ValidateSpacing reports whether s is a supported spacing value.
func ValidateSpacing(s string) error {
	switch s {
	case SpacingCompact, SpacingNormal, SpacingRelaxed:
		return nil
	}
	return fmt.Errorf("spacing %q: want %q, %q or %q",
		s, SpacingCompact, SpacingNormal, SpacingRelaxed)
}

// Settings holds the user preferences persisted in the settings table.
type Settings struct {
	Grid    Grid   `json:"grid"`
	Spacing string `json:"spacing"`
}

// Validate checks every settings field.
func (s Settings) Validate() error {
	if err := s.Grid.Validate(); err != nil {
		return err
	}
	return ValidateSpacing(s.Spacing)
}

// BookmarkStore is the persistence contract for bookmarks. Mutating
// methods apply the internal/layout rules — independent pages of at
// most grid.rows*cols slots, compacted per page — inside one
// transaction.
type BookmarkStore interface {
	// List returns all bookmarks ordered by page, then position.
	List(ctx context.Context) ([]Bookmark, error)
	// Get returns one bookmark, or ErrNotFound.
	Get(ctx context.Context, id int64) (Bookmark, error)
	// Create adds a bookmark with a pending icon via layout.Add: the
	// first page at or after fromPage with a free slot gets it, or a
	// new last page when all pages from fromPage on are full.
	Create(ctx context.Context, title, url string, fromPage int) (Bookmark, error)
	// Update edits title and URL. A changed URL resets the cached icon to
	// pending so it gets refetched.
	Update(ctx context.Context, id int64, title, url string) (Bookmark, error)
	// Delete removes a bookmark via layout.Remove: its page closes up
	// and drops out when left empty.
	Delete(ctx context.Context, id int64) error
	// Move relocates a bookmark via layout.Insert and returns the full
	// list in display order. page may be len(pages) — a new last page —
	// and index clamps to the target page's end; anything else out of
	// range is ErrInvalidMove, a missing id ErrNotFound.
	Move(ctx context.Context, id int64, page, index int) ([]Bookmark, error)
	// SetIcon records the icon file name and status for a bookmark.
	SetIcon(ctx context.Context, id int64, path, status string) error
	// ReplaceAll deletes all bookmarks then inserts items; the import
	// "replace" mode. When every item carries a Page, items are grouped
	// by (page, position) — pages renumbered 0.. — and reflowed under
	// the current grid cap; otherwise items fill pages in file order.
	ReplaceAll(ctx context.Context, items []NewBookmark) ([]Bookmark, error)
	// AppendMany inserts items via layout.Add from page 0 each; the
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
