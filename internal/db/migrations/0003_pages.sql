-- Round 8 "independent pages": each bookmark carries its page plus the
-- position within that page (docs/PLAN.md). Existing rows keep their
-- flat order, chunked by the configured grid capacity (default 4x6).

ALTER TABLE bookmarks ADD COLUMN page INTEGER NOT NULL DEFAULT 0;

-- rank = row_number() over (order by position, id) - 1; the new
-- page = rank / cap and position = rank % cap, where cap is
-- grid.rows * grid.cols from the settings table (JSON), defaulting to
-- 24 when the grid is missing, unreadable or degenerate.
WITH cap AS (
  SELECT CASE WHEN c > 0 THEN c ELSE 24 END AS n
  FROM (
    SELECT COALESCE(
        (SELECT json_extract(value, '$.rows') FROM settings WHERE key = 'grid')
      * (SELECT json_extract(value, '$.cols') FROM settings WHERE key = 'grid'),
      24) AS c
  )
),
ranked AS (
  SELECT id, ROW_NUMBER() OVER (ORDER BY position, id) - 1 AS rank
  FROM bookmarks
)
UPDATE bookmarks
SET page     = ranked.rank / cap.n,
    position = ranked.rank % cap.n
FROM ranked, cap
WHERE bookmarks.id = ranked.id;

CREATE INDEX idx_bookmarks_page_position ON bookmarks(page, position);
