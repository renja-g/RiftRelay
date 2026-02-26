# RiftRelay

A high-throughput relay for the Riot API with centralized admission control. Built in Go with with integrated Swagger UI.

## What it does

RiftRelay sits between your application and Riot's API, managing rate limits intelligently so you don't have to. By default it will spread requests evenly across the remaining rate limit window. If you need to send requests with higher priority, you can add the `X-Priority: high` header to bypass the pacing delay but still respect the rate limit.

## Quick start

### Option 1: Docker (recommended)

```bash
curl -fsSL "https://raw.githubusercontent.com/renja-g/RiftRelay/main/scripts/setup-docker-stack.sh" | bash
```

The script downloads everything you need, prompts for your Riot API token and start RiftRelay at the end. For non-interactive setup:

```bash
curl -fsSL "https://raw.githubusercontent.com/renja-g/RiftRelay/main/scripts/setup-docker-stack.sh" | \
  bash -s -- --token "your-riot-token"
```

### Option 2: Run from source

```bash
export RIOT_TOKEN="your-riot-token"
go run .
```

The server starts on `http://localhost:8985` by default.

## Usage

Send requests through RiftRelay using the format `/{region}/{riot-api-path}`:

```bash
curl "http://localhost:8985/europe/riot/account/v1/accounts/by-riot-id/Someone/EUW1"
```

For high-priority requests, add the `X-Priority: high` header to bypass pacing delays (rate limits still apply):

```bash
curl -H "X-Priority: high" "http://localhost:8985/europe/riot/account/v1/accounts/by-riot-id/Someone/EUW1"
```

You can also explore and send API requests through the Swagger UI at `http://localhost:8985/swagger/` (enabled by default).

## Configuration

Set `RIOT_TOKEN` (required). Multiple tokens are supported as comma separated values:

```bash
RIOT_TOKEN=token1,token2,token3
```

Common settings:

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8985` | Server port |
| `QUEUE_CAPACITY` | `2048` | Max queued requests |
| `ADMISSION_TIMEOUT` | `5m` | Max wait time for admission (how long a request can wait in the queue) |
| `SHUTDOWN_TIMEOUT` | `20s` | Graceful shutdown timeout |
| `ENABLE_METRICS` | `false` | Enable `/metrics` endpoint |
| `ENABLE_PPROF` | `false` | Enable pprof endpoints |
| `ENABLE_SWAGGER` | `true` | Enable Swagger UI |

See `.env.example` for all available options.

## Endpoints

- **Health check**: `GET /healthz`
- **Metrics**: `GET /metrics` (when enabled)
- **Swagger UI**: `GET /swagger/` (when enabled)
- **pprof**: `/debug/pprof/*` (when enabled)

## How it works

When requests come in, RiftRelay figures out which rate limit bucket they belong to and adds them to a queue. A scheduler picks requests from the queue and sends them when there's room in the rate limit window. Instead of sending all requests at once when the limit resets, RiftRelay spreads them out evenly over time to avoid sudden bursts.

If there's no room in the rate limit window, RiftRelay returns `429 Too Many Requests` with a `Retry-After` header telling you when to try again. Requests that do get through are sent to Riot's API, and RiftRelay keeps track of the rate limits based on the response headers it gets back.

## Development

Run tests:

```bash
go test ./...
```

With race detection:

```bash
go test -race ./internal/config ./internal/router ./internal/limiter ./internal/proxy
```

Benchmarks:

```bash
go test -run '^$' -bench . -benchmem ./internal/limiter ./internal/proxy
```

## Project structure

- `main.go` - Entry point and signal handling
- `internal/app` - Server lifecycle and routing
- `internal/config` - Environment configuration
- `internal/router` - Path parsing and bucket keys
- `internal/limiter` - Admission control and scheduling
- `internal/proxy` - Reverse proxy adapter
- `internal/transport` - HTTP transport configuration
- `internal/metrics` - Metrics collection

## Notes

- On shutdown, the server drains pending requests within `SHUTDOWN_TIMEOUT`
- Invalid routes return `400 Bad Request`
