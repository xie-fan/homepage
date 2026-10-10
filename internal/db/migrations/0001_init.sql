CREATE TABLE bookmarks (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  title       TEXT NOT NULL,
  url         TEXT NOT NULL,
  position    INTEGER NOT NULL,           -- global order; pages derived from grid size
  icon_path   TEXT NOT NULL DEFAULT '',   -- file name inside icons/, '' = none / not fetched
  icon_status TEXT NOT NULL DEFAULT 'pending', -- pending | ok | none | error
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);
CREATE INDEX idx_bookmarks_position ON bookmarks(position);
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL); -- JSON values
