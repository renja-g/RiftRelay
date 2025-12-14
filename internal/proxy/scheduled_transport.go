package proxy

import (
	"net/http"
	"strings"

	"github.com/renja-g/rp/internal/router"
)

type scheduledTransport struct {
	scheduler *RateScheduler
	base      http.RoundTripper
}

func (t scheduledTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	key := buildKey(req)
	priority := strings.EqualFold(req.Header.Get("X-Priority"), "high")

	if err := t.scheduler.acquire(req.Context(), key, priority); err != nil {
		return nil, err
	}

	transport := t.base
	if transport == nil {
		transport = http.DefaultTransport
	}

	resp, err := transport.RoundTrip(req)
	if err == nil && resp != nil {
		t.scheduler.updateFromHeaders(key, resp.Header)
	}
	return resp, err
}

func buildKey(req *http.Request) string {
	if info, ok := router.PathFromContext(req.Context()); ok {
		pattern := info.PathPattern
		if pattern == "" {
			pattern = info.Path
		}
		return info.Region + "|" + pattern
	}
	return req.Host + "|" + req.URL.Path
}

