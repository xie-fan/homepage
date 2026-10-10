# Homepage — Plan

Personal homepage, self-hosted with Docker. First feature: a paged bookmark grid
(like a phone launcher). Built to grow: more widgets/features will be added later.

## Architecture

```
Browser (static HTML/CSS/JS, no build step)
   │  fetch() JSON
   ▼
Go binary (net/http, stdlib router with Go 1.22+ patterns)
   ├── internal/api        HTTP handlers, JSON in/out, validation
   ├── internal/store      data-access layer (interfaces + SQLite impl)
   ├── internal/db         open SQLite (modernc.org/sqlite, pure Go), run migrations
   ├── internal/favicon    fetch + parse + cache site icons, SSRF guard
   ├── internal/config     env-based config
   └── web/                static frontend, embedded with go:embed
   ▼
/data  (Docker volume)
   ├── homepage.db         SQLite (WAL)
   └── icons/              cached icon files (<sha256(host)>.<ext>)
```

* Single binary, `CGO_ENABLED=0`, final image `gcr.io/distroless/static` (or alpine) — small.
* No frontend build chain: plain ES modules + CSS, embedded via `go:embed`.

## Directory layout

```
cmd/homepage/main.go
internal/config/config.go
internal/db/db.go
internal/db/migrations/0001_init.sql        (embedded, applied in order)
internal/store/store.go                      (types + interfaces)
internal/store/sqlite.go                     (+ sqlite_test.go)
internal/favicon/{fetch.go,parse.go,guard.go,cache.go} (+ tests)
internal/api/{server.go,bookmarks.go,settings.go,icons.go,importexport.go} (+ api_test.go)
web/{embed.go,index.html,app.js,style.css}
Dockerfile  docker-compose.yml  .dockerignore  README.md
```

## Configuration (env)

| var | default | meaning |
|---|---|---|
| `HOMEPAGE_ADDR` | `:8080` | listen address |
| `HOMEPAGE_DATA_DIR` | `./data` | DB + icon cache dir |
| `HOMEPAGE_FAVICON_ALLOW_PRIVATE` | `false` | allow fetching icons from private/loopback IPs |
| `HOMEPAGE_FAVICON_FALLBACK` | `` (off) | optional third-party fallback: `google` or `duckduckgo` |
| `HOMEPAGE_FAVICON_PROXY` | `` (direct) | optional proxy URL for icon fetches (`http`/`https`/`socks5`/`socks5h`); env proxies are never used — with a proxy the SSRF guard can only vet the proxy's IP, not the target's |
| `HOMEPAGE_FAVICON_TIMEOUT` | `8s` | per-request timeout |

## DB schema (migration 0001)

```sql
CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL);
CREATE TABLE bookmarks (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  title      TEXT NOT NULL,
  url        TEXT NOT NULL,
  position   INTEGER NOT NULL,          -- global order; pages derived from grid size
  icon_path  TEXT NOT NULL DEFAULT '',  -- file name inside icons/, '' = none / not fetched
  icon_status TEXT NOT NULL DEFAULT 'pending', -- pending | ok | none | error
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE INDEX idx_bookmarks_position ON bookmarks(position);
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL); -- JSON values
```

Settings keys: `grid` → `{"rows":4,"cols":6}`; rows ∈ [3,5], cols ∈ [4,6].

## REST API (JSON)

| method | path | body / notes |
|---|---|---|
| GET | `/api/health` | `{"ok":true}` |
| GET | `/api/bookmarks` | list ordered by position |
| POST | `/api/bookmarks` | `{title,url}` → 201 bookmark; appended at end; icon fetched async |
| PUT | `/api/bookmarks/{id}` | `{title,url}`; URL change → refetch icon |
| DELETE | `/api/bookmarks/{id}` | 204 |
| PUT | `/api/bookmarks/order` | `{"ids":[...]}` full order → positions 0..n-1 |
| POST | `/api/bookmarks/{id}/icon/refresh` | refetch icon |
| GET | `/api/icons/{id}` | icon bytes (cached file), 404 if none |
| GET | `/api/settings` | `{"grid":{"rows":4,"cols":6}}` |
| PUT | `/api/settings` | same shape, validated |
| GET | `/api/export` | `{"version":1,"settings":{...},"bookmarks":[{title,url}]}` (download) |
| POST | `/api/import?mode=append|replace` | same shape; validated; icons fetched async |

Validation: title 1..200 chars (trimmed; default to host if empty), URL must be absolute
`http`/`https` with a host, ≤ 2048 chars. Errors: `{"error":"..."}` with 400/404/500.

## Favicon fetching

1. GET the page (timeout, max 1 MiB body, ≤ 5 redirects), parse `<link rel>` with
   `golang.org/x/net/html`: prefer `apple-touch-icon` / largest `sizes` icon, then `icon`,
   `shortcut icon`; resolve relative hrefs; support `data:` URIs.
2. Fall back to `<scheme>://<host>/favicon.ico`.
3. Optional third-party fallback (config).
4. Validate response: 2xx, image content-type (or sniffed), ≤ 512 KiB.
5. Save to `icons/<sha256(url host)>.<ext>`; update bookmark `icon_status/icon_path`.

SSRF guard: custom `net.Dialer.Control` rejects loopback/private/link-local/unspecified/
multicast/CGNAT IPs (after DNS resolution, so redirects + rebinding are covered), unless
`ALLOW_PRIVATE`. Only http/https. Concurrency limited (worker semaphore).

## Frontend

* `index.html`: header (title, + Add, ⚙ Settings, Import/Export), a horizontal
  track of pages, left/right arrow buttons, page dots.
* `app.js` (ES module): state `{bookmarks, grid}`; render pages = chunk(bookmarks, rows*cols);
  CSS grid per page; translateX track; navigation: arrows, dots, keyboard ←/→,
  wheel (debounced, deltaX or deltaY), touch/pointer swipe.
* Tiles: icon `<img src=/api/icons/{id}>` or letter avatar (first letter, color from hash of host);
  click opens URL; right-click / hover ✎ to edit; modal for add/edit/delete.
* Drag & drop (HTML5 DnD / pointer): reorder within page; hovering near page edge while
  dragging flips page → cross-page move; on drop send `PUT /api/bookmarks/order`.
* Settings modal: rows (3–5) & cols (4–6) selects. Import (file input, append/replace) / Export.
* Polls `/api/bookmarks` a few times after add to pick up icons fetched async.

## Task list (each executed by Devin CLI, reviewed, built/tested, committed)

1. **Scaffold**: `go.mod` (module `homepage`, go 1.24), `cmd/homepage/main.go` with config,
   `/api/health`, graceful shutdown, `.gitignore` (data/, binaries). Verify: `go build ./...`, curl health.
2. **DB + migrations**: `internal/db` opens SQLite (modernc, WAL, foreign keys, busy_timeout),
   embedded versioned migrations runner, `0001_init.sql`. Test: migrations idempotent.
3. **Store layer**: types + `BookmarkStore`/`SettingsStore` interfaces, SQLite impl
   (CRUD, append position, reorder in tx, replace-all for import, settings get/put w/ defaults). Tests.
4. **REST API**: handlers for bookmarks/settings/order/import/export with validation, using
   httptest-based tests (favicon fetcher behind an interface, stubbed in tests).
5. **Favicon**: guard (SSRF dialer), HTML parse, fetch with limits, cache files, async worker,
   `/api/icons/{id}` + refresh endpoint. Unit tests for parsing + guard + fetch via httptest
   (with allow-private on for test servers).
6. **Frontend**: embedded static UI — paging (arrows/dots/wheel/keys/swipe), grid settings,
   tiles with icon/letter avatar, add/edit/delete modals, import/export.
7. **Drag & drop**: reorder incl. cross-page via edge hover; persist order.
8. **Docker**: multi-stage Dockerfile (CGO off, non-root, distroless/alpine), `.dockerignore`,
   `docker-compose.yml` (port `${HOMEPAGE_PORT:-8080}`, `./data:/data`), README.
9. **Final verification**: `go test ./...`, manual curl run, screenshots, zip.

---

## Round 8 — independent pages (replaces "one flat list split into pages")

### Model
Each bookmark has `page` (0-based) and `position` (0-based within the page). Capacity `cap = rows*cols`.
A page holds ≤ cap bookmarks, may have free slots at its end, never has holes in the middle
(positions are compacted 0..n-1 within the page). Pages are numbered 0..P-1 with no empty page,
except a single empty page 0 when there are no bookmarks.

### Algorithm (package `internal/layout`, mirrored 1:1 in `web/static/layout.js`)
Layout = `[][]int64` (ids per page, in order). All ops return a new layout.
- `FromFlat(ids, cap)` — fill pages sequentially (migration, import without page info).
- `Insert(L, id, page, index, cap)` — remove `id` from its current page (that page closes up,
  nothing is pulled from the next page); insert into target `page` (page == len(L) means a new
  page at the end) at `min(index, len(page))`; then `cascade` from the target page; then drop
  empty pages. Thumbnail drop = Insert with index = len(page).
- `cascade(L, from, cap)` — for p = from…: while len(L[p]) > cap, pop its last item and push it to
  the FRONT of L[p+1] (creating a new last page if needed).
- `Add(L, id, fromPage, cap)` — first page p ≥ fromPage with a free slot gets `id` at its end;
  if none, a new page is appended. Never pushes others.
- `Remove(L, id)` — remove, close up within page, drop the page if it became empty (unless only page).
- `Reflow(L, cap)` — grid change: cascade from page 0 (shrink pushes overflow forward; grow = no-op).
- `DropEmpty(L)` — remove empty pages, keep at least one.

### API changes
- Bookmark JSON gains `"page"` and `"position"` (within page). `GET /api/bookmarks` ordered by page, position.
- `POST /api/bookmarks` accepts optional `"page"` (the page the user is viewing; default 0) → `Add`.
- `POST /api/bookmarks/move` `{"id":N,"page":P,"index":I}` → applies `Insert` atomically in one tx,
  returns `200` with the full bookmark list (same shape as GET /api/bookmarks). `page` 0..len(pages)
  (len = new page), `index` ≥ 0 (clamped). Errors: `not_found`, `invalid_move` (400).
- `PUT /api/bookmarks/order` removed (frontend no longer uses it).
- `DELETE /api/bookmarks/{id}` → `Remove`.
- `PUT /api/settings` with a changed grid → `Reflow` in the same transaction.
- Export: bookmarks carry `page`, `position`. Import replace: if every item has `page`, group by
  (page, position) in file order of pages (renumbered 0..), then `Reflow` with the (new) cap; else
  `FromFlat`. Import append: `Add(…, fromPage=0)` per new (non-duplicate) bookmark.
- Migration `0003_pages.sql`: add `page`, convert flat order: rank = row_number over (position, id) − 1,
  page = rank / cap, position = rank % cap, cap from settings.grid (default 4×6).

### Frontend
- Pages are derived by grouping `state.bookmarks` by `page`; page count = max(page)+1 (≥1).
- Drag preview = `layout.insert(originalLayout, id, hoverPage, hoverIndex, cap)` recomputed from the
  drag-start snapshot on every hover change (never cumulative); pointer outside grid/thumbnails →
  preview shows the original layout (matches cancel on release). Drop → `POST /api/bookmarks/move`,
  adopt the returned list (server is the source of truth). Hovering an empty slot → index = len(page).
- Add sends `page: currentPage`; afterwards go to the new bookmark's page and highlight it.
- Search view unchanged (flat filtered list, drag disabled).

### Tickets
- P1 (backend): internal/layout + tests; migration 0003; store (page/position, Move/Add/Remove/Reflow
  in tx, import/export); API (move endpoint, add page hint, settings reflow, remove /order); tests.
- P2 (frontend, parallel, against the contract above): web/static/layout.js; app.js page grouping,
  add-with-page; dnd.js snapshot preview + move endpoint + outside-grid revert; thumbs.js drop → move.
- P3 (reviewer): ui_check.py scenarios, migration check against pre-pages backup, container rebuild.
