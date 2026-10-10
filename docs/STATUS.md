# Status

All planned tasks (1–9) done and committed. `go test ./...` green (69 tests).
Docker image builds (~13 MB distroless); `docker compose up -d` serves on :8080 with a named volume.
Seed sample data: `scripts/seed.sh http://localhost:8080`.
Known gaps: see README / final report (no auth, no healthcheck, some sites block icon fetching → letter avatar).
