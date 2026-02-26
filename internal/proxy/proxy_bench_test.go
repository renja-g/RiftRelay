package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/renja-g/RiftRelay/internal/config"
	"github.com/renja-g/RiftRelay/internal/limiter"
	"github.com/renja-g/RiftRelay/internal/metrics"
)

type staticRoundTripper struct{}

func (staticRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("ok")),
		Request:    r,
	}, nil
}

func BenchmarkProxyEndToEnd(b *testing.B) {
	l, err := limiter.New(limiter.Config{
		KeyCount:      1,
		QueueCapacity: 4096,
	})
	if err != nil {
		b.Fatalf("new limiter: %v", err)
	}
	defer l.Close()

	handler := New(
		config.Config{
			Tokens:           []string{"bench-token"},
			AdmissionTimeout: 2 * time.Second,
		},
		WithLimiter(l),
		WithMetrics(metrics.NewCollector()),
	)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/na1/lol/status/v4/platform-data", nil)
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			b.Fatalf("expected 200, got %d", rr.Code)
		}
	}
}
