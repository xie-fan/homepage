-- Seed the tile-spacing preference ("compact" | "normal" | "relaxed");
-- values are stored as JSON like every settings row.
INSERT OR IGNORE INTO settings(key, value) VALUES ('spacing', '"normal"');
