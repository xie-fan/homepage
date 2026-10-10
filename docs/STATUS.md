# Status

QA round 1 fixes done and committed (responsive grid, live icon updates, inline/friendly errors with API codes,
duplicate-URL warn+confirm, dwell-based cross-page drag, app favicon, per-host icon reuse, fetch fallbacks).
`go test ./...`: 88 tests pass. Container rebuilt with `docker compose up -d --build`, data kept.
Known: stackoverflow.com / npmjs.com block Go's TLS client (Cloudflare 403); juejin.cn only references its icon from JS → letter avatar.
