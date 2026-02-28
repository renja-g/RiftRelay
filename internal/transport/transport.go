package transport

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/renja-g/RiftRelay/internal/config"
)

func New(cfg config.UpstreamTransportConfig) *http.Transport {
	dialer := &net.Dialer{
		Timeout:   cfg.DialTimeout,
		KeepAlive: cfg.DialKeepAlive,
	}

	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     cfg.ForceAttemptHTTP2,
		MaxIdleConns:          cfg.MaxIdleConns,
		MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
		MaxConnsPerHost:       cfg.MaxConnsPerHost,
		IdleConnTimeout:       cfg.IdleConnTimeout,
		TLSHandshakeTimeout:   cfg.TLSHandshakeTimeout,
		ExpectContinueTimeout: cfg.ExpectContinueTimeout,
		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
	}
}

func WithRequestTimeout(base http.RoundTripper, timeout time.Duration) http.RoundTripper {
	if timeout <= 0 {
		return base
	}
	return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		return base.RoundTrip(r.Clone(ctx))
	})
}

func WithRetryAfter429(base http.RoundTripper, maxRetries int) http.RoundTripper {
	if maxRetries <= 0 {
		return base
	}

	return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		canRetryBody := canReplayRequestBody(r)

		for attempt := 0; ; attempt++ {
			req := r
			if attempt > 0 {
				clonedReq, err := cloneRequestForRetry(r)
				if err != nil {
					return nil, err
				}
				req = clonedReq
			}

			resp, err := base.RoundTrip(req)
			if err != nil {
				return nil, err
			}
			if resp.StatusCode != http.StatusTooManyRequests || attempt >= maxRetries || !canRetryBody {
				return resp, nil
			}

			waitFor, ok := parseRetryAfterHeader(resp.Header.Get("Retry-After"), time.Now())
			if !ok {
				return resp, nil
			}

			if resp.Body != nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}

			if waitFor <= 0 {
				continue
			}

			timer := time.NewTimer(waitFor)
			select {
			case <-timer.C:
			case <-r.Context().Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return nil, r.Context().Err()
			}
		}
	})
}

func canReplayRequestBody(r *http.Request) bool {
	return r.Body == nil || r.Body == http.NoBody || r.GetBody != nil
}

func cloneRequestForRetry(r *http.Request) (*http.Request, error) {
	cloned := r.Clone(r.Context())
	if r.GetBody == nil {
		return cloned, nil
	}

	body, err := r.GetBody()
	if err != nil {
		return nil, fmt.Errorf("clone request body: %w", err)
	}
	cloned.Body = body
	return cloned, nil
}

func parseRetryAfterHeader(value string, now time.Time) (time.Duration, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, false
	}

	seconds, err := strconv.Atoi(trimmed)
	if err == nil {
		if seconds < 0 {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}

	retryAt, err := http.ParseTime(trimmed)
	if err != nil {
		return 0, false
	}

	waitFor := retryAt.Sub(now)
	if waitFor < 0 {
		return 0, true
	}

	return waitFor, true
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}
