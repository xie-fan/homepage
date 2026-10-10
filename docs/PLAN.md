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
