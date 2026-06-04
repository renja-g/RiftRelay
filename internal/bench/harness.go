package bench

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/renja-g/RiftRelay/internal/app"
	"github.com/renja-g/RiftRelay/internal/config"
	"github.com/renja-g/RiftRelay/internal/proxy"
	"github.com/renja-g/RiftRelay/internal/testutil"
)

const (
	// AppRateLimitAssumption is the Riot app limit used for throughput benchmarks.
	AppRateLimitAssumption = appRateLimitHeader

	defaultBenchWarmup    = 2 * time.Second
	defaultBenchDuration  = 10 * time.Second
	defaultParallelism    = 128
	workersPerPlatform    = 8
)

// LoLPlatformRoutingHosts are platform routing values; each has its own app rate limit.
var LoLPlatformRoutingHosts = []string{
	"br1", "eun1", "euw1", "jp1", "kr", "la1", "la2", "na1", "oc1", "ru", "tr1",
	"ph2", "sg2", "th2", "tw2", "vn2",
}

const leagueV4ChallengerPath = "/lol/league/v4/challengerleagues/by-queue/RANKED_SOLO_5x5"

// LoadResult summarizes a sustained load run against RiftRelay.
type LoadResult struct {
	OK       int64
	Rejected int64
	Other    int64
	Duration time.Duration
}

func (r LoadResult) OKRPS() float64 {
	if r.Duration <= 0 {
		return 0
	}
	return float64(r.OK) / r.Duration.Seconds()
}

func (r LoadResult) TotalRPS() float64 {
	if r.Duration <= 0 {
		return 0
	}
	return float64(r.OK+r.Rejected+r.Other) / r.Duration.Seconds()
}

type loadConfig struct {
	path       func(workerID int) string
	warmup     time.Duration
	duration   time.Duration
	parallel   int
	inFlight   int
}

func benchConfig() config.Config {
	cfg := testutil.DummyConfig()
	cfg.QueueCapacity = 65536
	cfg.AdmissionTimeout = 0
	cfg.AdditionalWindow = 150 * time.Millisecond
	cfg.DefaultAppLimits = AppRateLimitAssumption
	cfg.MetricsEnabled = false
	cfg.SwaggerEnabled = false
	cfg.UpstreamTimeout = 0
	cfg.Tokens = []string{"bench-token"}
	return cfg
}

func newBenchServer(tb testing.TB, upstream http.RoundTripper) (*httptest.Server, func()) {
	tb.Helper()

	server, err := app.New(
		benchConfig(),
		app.WithSwaggerHandler(http.NotFoundHandler()),
		app.WithProxyOptions(proxy.WithBaseTransport(upstream)),
	)
	if err != nil {
		tb.Fatalf("app.New() error = %v", err)
	}

	ts := httptest.NewServer(server.Handler())
	cleanup := func() {
		ts.Close()
		_ = server.Shutdown(tb.Context())
	}
	return ts, cleanup
}

func runLoad(tb testing.TB, relay *httptest.Server, cfg loadConfig) LoadResult {
	tb.Helper()

	if cfg.parallel <= 0 {
		cfg.parallel = defaultParallelism
	}
	if cfg.warmup <= 0 {
		cfg.warmup = defaultBenchWarmup
	}
	if cfg.duration <= 0 {
		cfg.duration = defaultBenchDuration
	}
	if cfg.path == nil {
		tb.Fatal("loadConfig.path is nil")
	}

	if cfg.inFlight <= 0 {
		cfg.inFlight = cfg.parallel
	}

	client := benchHTTPClient()
	var okCount, rejectedCount, otherCount atomic.Int64
	stop := make(chan struct{})
	sem := make(chan struct{}, cfg.inFlight)
	var wg sync.WaitGroup

	for worker := 0; worker < cfg.parallel; worker++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			path := cfg.path(workerID)
			for {
				select {
				case <-stop:
					return
				default:
				}

				select {
				case sem <- struct{}{}:
				case <-stop:
					return
				}

				req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, relay.URL+path, nil)
				if err != nil {
					<-sem
					otherCount.Add(1)
					continue
				}
				resp, err := client.Do(req)
				<-sem
				if err != nil {
					otherCount.Add(1)
					continue
				}
				_ = resp.Body.Close()

				switch resp.StatusCode {
				case http.StatusNoContent, http.StatusOK:
					okCount.Add(1)
				case http.StatusTooManyRequests:
					rejectedCount.Add(1)
				default:
					otherCount.Add(1)
				}
			}
		}(worker)
	}

	time.Sleep(cfg.warmup)
	start := time.Now()
	time.Sleep(cfg.duration)
	close(stop)
	wg.Wait()

	return LoadResult{
		OK:       okCount.Load(),
		Rejected: rejectedCount.Load(),
		Other:    otherCount.Load(),
		Duration: time.Since(start),
	}
}

func logLoadResult(tb testing.TB, name string, result LoadResult, parallel int) {
	tb.Helper()
	tb.Logf(
		"%s: ok=%d (%.0f rps) rejected=%d other=%d total=%.0f rps over %s (workers=%d, limit=%s)",
		name,
		result.OK,
		result.OKRPS(),
		result.Rejected,
		result.Other,
		result.TotalRPS(),
		result.Duration.Round(time.Millisecond),
		parallel,
		AppRateLimitAssumption,
	)
}

func benchHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 0,
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			MaxIdleConns:          4096,
			MaxIdleConnsPerHost:   4096,
			MaxConnsPerHost:       4096,
			IdleConnTimeout:       90 * time.Second,
			DisableCompression:    true,
			ForceAttemptHTTP2:     false,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}
}

func defaultSingleRegionParallelism() int {
	p := defaultParallelism
	if cpus := runtime.GOMAXPROCS(0); cpus*16 > p {
		p = cpus * 16
	}
	return p
}

func defaultLeagueParallelism() int {
	return len(LoLPlatformRoutingHosts) * workersPerPlatform
}

func reportBenchMetrics(b *testing.B, result LoadResult) {
	b.ReportMetric(result.OKRPS(), "ok_rps")
	b.ReportMetric(result.TotalRPS(), "req_rps")
	if secs := result.Duration.Seconds(); secs > 0 {
		b.ReportMetric(float64(result.Rejected)/secs, "reject_rps")
	}
}

func singleRegionPath(_ int) string {
	return "/europe/riot/account/v1/accounts/me"
}

func leagueV4PlatformPath(workerID int) string {
	platform := LoLPlatformRoutingHosts[(workerID/workersPerPlatform)%len(LoLPlatformRoutingHosts)]
	return fmt.Sprintf("/%s%s", platform, leagueV4ChallengerPath)
}
