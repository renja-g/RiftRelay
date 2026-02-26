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

type captureRoundTripper struct {
	lastRequest *http.Request
}

func (c *captureRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	c.lastRequest = r.Clone(r.Context())
	header := make(http.Header)
	header.Set("X-App-Rate-Limit", "20:1")
	header.Set("X-App-Rate-Limit-Count", "1:1")
	header.Set("X-Method-Rate-Limit", "20:1")
	header.Set("X-Method-Rate-Limit-Count", "1:1")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
		Request:    r,
	}, nil
}

func TestProxyInjectsTokenAndRoutes(t *testing.T) {
	l, err := limiter.New(limiter.Config{
		KeyCount:      1,
		QueueCapacity: 32,
	})
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	defer l.Close()

	collector := metrics.NewCollector()
	rt := &captureRoundTripper{}
	handler := New(
		config.Config{
			Tokens:           []string{"test-token"},
			AdmissionTimeout: 2 * time.Second,
		},
		func(o *options) {
			o.baseTransport = rt
		},
		WithLimiter(l),
		WithMetrics(collector),
	)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/na1/lol/status/v4/platform-data", nil)
	req.RemoteAddr = "203.0.113.9:4242"
	req.Header.Set("Connection", "X-Riot-Token")
	req.Header.Set("X-Forwarded-For", "198.51.100.7")
	req.Header.Set("X-Forwarded-Host", "spoofed.example")
	req.Header.Set("X-Forwarded-Proto", "https")
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if rt.lastRequest == nil {
		t.Fatalf("expected transport request to be captured")
	}
	if got := rt.lastRequest.Header.Get("X-Riot-Token"); got != "test-token" {
		t.Fatalf("expected X-Riot-Token=test-token, got %q", got)
	}
	if rt.lastRequest.URL.Host != "na1.api.riotgames.com" {
		t.Fatalf("unexpected host: %s", rt.lastRequest.URL.Host)
	}
	if rt.lastRequest.URL.Path != "/lol/status/v4/platform-data" {
		t.Fatalf("unexpected upstream path: %s", rt.lastRequest.URL.Path)
	}
	if got := rt.lastRequest.Header.Get("X-Forwarded-For"); got != "203.0.113.9" {
		t.Fatalf("expected X-Forwarded-For=203.0.113.9, got %q", got)
	}
	if got := rt.lastRequest.Header.Get("X-Forwarded-Host"); got != "example.com" {
		t.Fatalf("expected X-Forwarded-Host=example.com, got %q", got)
	}
	if got := rt.lastRequest.Header.Get("X-Forwarded-Proto"); got != "http" {
		t.Fatalf("expected X-Forwarded-Proto=http, got %q", got)
	}
}
