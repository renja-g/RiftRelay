package bench

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/renja-g/RiftRelay/internal/testutil"
)

const (
	appRateLimitHeader   = "1000000:10"
	methodRateLimitHeader = "1000000:10"
	rateWindowSeconds    = 10
)

// MockUpstream simulates Riot rate-limit headers for throughput benchmarks.
type MockUpstream struct {
	appCounts    sync.Map // region -> *atomic.Int64
	methodCounts sync.Map // bucket key -> *atomic.Int64
}

func NewMockUpstream() *MockUpstream {
	return &MockUpstream{}
}

func (m *MockUpstream) RoundTrip(r *http.Request) (*http.Response, error) {
	region := regionFromHost(r.URL.Host)
	if region == "" {
		region = "unknown"
	}

	appCount := m.increment(&m.appCounts, region)
	bucket := methodBucketKey(region, r.URL.Path)
	methodCount := m.increment(&m.methodCounts, bucket)

	headers := http.Header{}
	headers.Set("X-App-Rate-Limit", appRateLimitHeader)
	headers.Set("X-App-Rate-Limit-Count", formatRateCount(appCount))
	headers.Set("X-Method-Rate-Limit", methodRateLimitHeader)
	headers.Set("X-Method-Rate-Limit-Count", formatRateCount(methodCount))

	resp := testutil.HTTPResponse(http.StatusNoContent, "", headers)
	resp.Request = r
	return resp, nil
}

func (m *MockUpstream) increment(store *sync.Map, key string) int64 {
	value, _ := store.LoadOrStore(key, &atomic.Int64{})
	counter := value.(*atomic.Int64)
	return counter.Add(1)
}

func regionFromHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return ""
	}
	if idx := strings.IndexByte(host, '.'); idx > 0 {
		return host[:idx]
	}
	return host
}

func methodBucketKey(region, upstreamPath string) string {
	upstreamPath = strings.TrimSpace(upstreamPath)
	if upstreamPath == "" {
		upstreamPath = "/"
	}
	if !strings.HasPrefix(upstreamPath, "/") {
		upstreamPath = "/" + upstreamPath
	}
	return region + ":" + strings.TrimPrefix(upstreamPath, "/")
}

func formatRateCount(count int64) string {
	return strconv.FormatInt(count, 10) + ":" + strconv.Itoa(rateWindowSeconds)
}
