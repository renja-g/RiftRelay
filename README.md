# RiftRelay

A rate-limiting proxy for the Riot API. Built in Go with integrated Swagger UI.

## Shoutout

Big shoutout to [DarkIntaqt](https://github.com/DarkIntaqt) and [cosmic-radiance](https://github.com/DarkIntaqt/cosmic-radiance): we originally planned to build it together, but after we both lost interest for a while and didn't make any progress, he built it solo. His project inspired me to come back to this idea and build RiftRelay.

## What it does

RiftRelay sits between your application and Riot's API, managing rate limits intelligently so you don't have to. By default it will spread requests evenly across the remaining rate limit window. You can use `X-Priority: high` to bypass pacing delay, or `X-Rate-Budget` to pace a labeled client at a configured share of the full limit.

## Quick start

### Option 1: Docker (recommended)

```bash
curl -fsSL "https://raw.githubusercontent.com/renja-g/RiftRelay/main/scripts/setup-docker-stack.sh" | bash
```

The script downloads everything you need, prompts for your Riot API token, and configures the stack. Start it afterward with `cd riftrelay-stack && docker compose up -d`, or pass `--up` to the setup script.

Pre build Docker images are available on the [Docker Hub](https://hub.docker.com/r/renjag/riftrelay).

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

For a configured rate budget, add `X-Rate-Budget: <id>`. Unknown IDs return `400 Bad Request`.

```bash
export RATE_BUDGET_worker=0.8
curl -H "X-Rate-Budget: worker" "http://localhost:8985/europe/riot/account/v1/accounts/by-riot-id/Someone/EUW1"
```

To force a request to use a specific API token, use the `X-Riot-Token-Index` header (0-indexed). Tokens in `RIOT_TOKEN` come first, followed by tokens in `RIOT_PINNED_TOKEN`:

```bash
curl -H "X-Riot-Token-Index: 0" "http://localhost:8985/europe/riot/account/v1/accounts/by-riot-id/Someone/EUW1"
```

Bearer-authenticated RSO requests must select a pinned index so their rate-limit state stays separate from normal API keys. RiftRelay forwards the `Authorization` header and does not add `X-Riot-Token`:

```bash
RIOT_TOKEN=api-key
RIOT_PINNED_TOKEN=rso-rate-limit-slot

curl \
  -H "Authorization: Bearer $RSO_ACCESS_TOKEN" \
  -H "X-Riot-Token-Index: 1" \
  "http://localhost:8985/europe/riot/account/v1/accounts/me"
```

You can also explore and send API requests through the Swagger UI at `http://localhost:8985/swagger/` (enabled by default).

<img width="1440" height="783" alt="image" src="https://github.com/user-attachments/assets/4dc41b61-1cc7-419e-80c6-0c93cf213f0d" />



## Configuration

Set `RIOT_TOKEN` (required). Multiple tokens are supported as comma separated values:

```bash
RIOT_TOKEN=token1,token2,token3
```

Use `RIOT_PINNED_TOKEN` for tokens that must never be selected automatically. These tokens are only used by requests with an explicit `X-Riot-Token-Index`. For example, the pinned token below has index `2`:

```bash
RIOT_TOKEN=token1,token2
RIOT_PINNED_TOKEN=rso-rate-limit-slot
```

Supported environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `RIOT_TOKEN` | required | Automatically selected Riot API tokens (comma-separated) |
| `RIOT_PINNED_TOKEN` | unset | Explicit-only Riot API tokens, appended after `RIOT_TOKEN` for 0-based indexing |
| `PORT` | `8985` | Server port |
| `QUEUE_CAPACITY` | `2048` | Max queued requests |
| `ADMISSION_TIMEOUT` | `5m` | Max wait time for admission (how long a request can wait in the queue) |
| `ADDITIONAL_WINDOW_SIZE` | `150ms` | Extra buffer added to rate limit windows |
| `SHUTDOWN_TIMEOUT` | `20s` | Graceful shutdown timeout |
| `UPSTREAM_TIMEOUT` | `20s` | Deadline for each upstream attempt, including reading the response body (0 = no deadline) |
| `UPSTREAM_RETRIES` | `2` | Retries for a failed `GET`/`HEAD` upstream response (0 = no retries) |
| `ENABLE_METRICS` | `true` | Enable `/metrics` endpoint |
| `ENABLE_PPROF` | `false` | Enable pprof endpoints |
| `ENABLE_SWAGGER` | `true` | Enable Swagger UI |
| `DEFAULT_APP_RATE_LIMIT` | `20:1,100:120` | Default app rate limits before first upstream response |
| `RATE_BUDGET_<id>` | unset | Pace `X-Rate-Budget: <id>` at this share of the full limit (`0 < share <= 1`) |
| `RATE_BUDGET_<id>_OVERRIDES` | unset | Optional `bucket=share,bucket=share` overrides for a budget |

## Endpoints

- **Health check**: `GET /healthz`
- **Metrics**: `GET /metrics` (when enabled)
- **Swagger UI**: `GET /swagger/` (when enabled)
- **pprof**: `/debug/pprof/*` (when enabled)

## How it works

When requests come in, RiftRelay figures out which rate limit bucket they belong to and adds them to a queue. A scheduler picks requests from the queue and sends them when there's room in the rate limit window. Instead of sending all requests at once when the limit resets, RiftRelay spreads them out evenly over time to avoid sudden bursts.

If there's no room in the rate limit window, RiftRelay returns `429 Too Many Requests` with a `Retry-After` header telling you when to try again. Requests that do get through are sent to Riot's API, and RiftRelay keeps track of the rate limits based on the response headers it gets back.

For `GET` requests, RiftRelay reads the whole response before forwarding it. If Riot resets the stream, stalls, sends a truncated or invalid JSON body, or returns `502`/`503`/`504`, RiftRelay retries through the same rate limiting. When retries do not recover the request, the client gets an error status with `Retry-After`, never a `200` with a broken body.

Rate budgets are one-sided pacing hints. A budget such as `RATE_BUDGET_worker=0.8` makes `X-Rate-Budget: worker` space requests as if the limit were 80% of the full Riot limit, but it does not reserve the remaining 20% for other traffic. Other traffic still uses normal full-limit pacing and all traffic shares the same hard Riot counters.

## Development

Run tests:

```bash
go test ./...
```

With race detection:

```bash
go test -race ./...
```

Benchmarks:

```bash
go test -run '^$' -bench . -benchmem ./internal/limiter ./internal/proxy
```

End-to-end throughput benchmarks (mock upstream with `1000000:10` app/method limits, default pacing/spreading):

```bash
# Sustained load with summary logs (~12s per test)
go test ./internal/bench -run 'TestThroughput' -count=1 -v

# Benchmark metrics (ok_rps, req_rps, reject_rps); default benchtime runs once per case
go test ./internal/bench -run '^$' -bench 'Throughput' -benchtime=1x -count=3 -v
```
