# AGENTS.md

## Cursor Cloud specific instructions

### Project overview

RiftRelay is a rate-limiting reverse proxy for the Riot Games API, built in Go.
It has two services:

| Service | Port | Purpose |
|---|---|---|
| RiftRelay (Go proxy) | 8985 | Main application — rate-limiting reverse proxy |
| Docs site (TypeScript/React) | 3000 | Documentation site in `docs/` using Waku + Fumadocs |

### Running the Go proxy

```bash
RIOT_TOKEN=dev-test-token go run .
```

A valid `RIOT_TOKEN` env var is required to start (any non-empty value works for local dev; real Riot API keys needed for actual proxying). The server exposes `/healthz`, `/metrics`, `/swagger/`, and proxies requests at `/{region}/{riot-api-path}`.

### Running the docs site

```bash
cd docs && pnpm dev
```

### Commands reference

See `README.md` for full configuration and endpoint details. Key commands:

- **Tests:** `go test ./...` (with race: `go test -race ./internal/config ./internal/router ./internal/limiter ./internal/proxy`)
- **Lint (Go):** `go tool golangci-lint run` (golangci-lint is a Go tool dependency, not a standalone binary)
- **Lint (Docs):** `cd docs && pnpm lint` (uses Biome)
- **Build:** `go build -o riftrelay .`

### Non-obvious notes

- golangci-lint is declared as a `tool` directive in `go.mod` and invoked via `go tool golangci-lint run`, not as a standalone binary.
- No databases or external services are required — all rate-limit state is in-memory.
- The proxy requires `RIOT_TOKEN` to be set (even a dummy value) or it will fail to start.
- Prometheus/Grafana are optional Docker Compose services (only needed with `ENABLE_METRICS=true` profile) — not required for development.
