package transport

import (
	"net/http"
	"time"
)

type retryTransport struct {
	base       http.RoundTripper
	maxRetries int
}

// NewRetryTransport wraps the given transport with retry-on-429 behavior.
func NewRetryTransport(base http.RoundTripper, maxRetries int) http.RoundTripper {
	if maxRetries <= 0 {
		return base
	}
	return retryTransport{
		base:       base,
		maxRetries: maxRetries,
	}
}

func (t retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	transport := t.base
	if transport == nil {
		transport = http.DefaultTransport
	}

	attempt := 0
	for {
		resp, err := transport.RoundTrip(req)
		if err != nil {
			return resp, err
		}

		if resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}

		if attempt >= t.maxRetries {
			return resp, nil
		}

		if req.Body != nil && req.GetBody == nil {
			return resp, nil
		}

		delay := parseRetryAfter(resp.Header.Get("Retry-After"))
		resp.Body.Close()

		if req.GetBody != nil {
			newBody, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req.Body = newBody
		}

		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
		}

		attempt++
	}
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}

	if d, err := time.ParseDuration(v); err == nil {
		return d
	}

	if secs, err := time.ParseDuration(v + "s"); err == nil {
		return secs
	}

	if ts, err := http.ParseTime(v); err == nil {
		now := time.Now()
		if ts.After(now) {
			return ts.Sub(now)
		}
	}

	return 0
}
