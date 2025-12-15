package transport

import (
	"net/http"
	"strings"

	"github.com/renja-g/RiftRelay/internal/router"
	"github.com/renja-g/RiftRelay/internal/scheduler"
)

type scheduledTransport struct {
	scheduler *scheduler.RateScheduler
	base      http.RoundTripper
}

// NewScheduledTransport wraps the given transport with rate limit scheduling.
func NewScheduledTransport(base http.RoundTripper, sched *scheduler.RateScheduler) http.RoundTripper {
	return scheduledTransport{
		scheduler: sched,
		base:      base,
	}
}

func (t scheduledTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	key := buildKey(req)
	priority := strings.EqualFold(req.Header.Get("X-Priority"), "high")

	if err := t.scheduler.Acquire(req.Context(), key, priority); err != nil {
		return nil, err
	}

	transport := t.base
	if transport == nil {
		transport = http.DefaultTransport
	}

	resp, err := transport.RoundTrip(req)
	if err == nil && resp != nil {
		t.scheduler.UpdateFromHeaders(key, resp.Header)
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
