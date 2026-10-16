package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"testing/synctest"
	"time"

	"github.com/renja-g/RiftRelay/internal/limiter"
	"github.com/renja-g/RiftRelay/internal/testutil"
)

func TestProxyNewRewritesRequestAndInjectsToken(t *testing.T) {
	t.Parallel()

	cfg := testutil.DummyConfig()
	cfg.UpstreamTimeout = 0

	var gotHost string
	var gotPath string
	var gotTokens []string

	handler := New(cfg, WithBaseTransport(testutil.RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotHost = r.URL.Host
		gotPath = r.URL.Path
		gotTokens = r.Header.Values("X-Riot-Token")
		return testutil.HTTPResponse(http.StatusNoContent, "", nil), nil
	})))

	req := httptest.NewRequest(http.MethodGet, "/europe/riot/account/v1/accounts/me", nil)
	req.Header.Set("X-Riot-Token", "client-supplied-token")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusNoContent; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := gotHost, "europe.api.riotgames.com"; got != want {
		t.Fatalf("host = %q, want %q", got, want)
	}
	if got, want := gotPath, "/riot/account/v1/accounts/me"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if got, want := gotTokens, []string{cfg.Tokens[0]}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("X-Riot-Token = %q, want %q", got, want)
	}
}

func TestProxyNewForwardsBearerAndStripsClientRiotToken(t *testing.T) {
	t.Parallel()

	l, err := limiter.New(limiter.Config{
		KeyCount:          2,
		AutomaticKeyCount: 1,
		QueueCapacity:     2,
		DefaultAppLimits:  "20:1",
	})
	if err != nil {
		t.Fatalf("limiter.New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = l.Close()
	})

	cfg := testutil.DummyConfig()
	cfg.Tokens = []string{"automatic-token", "rso-rate-limit-slot"}
	cfg.AutomaticTokenCount = 1
	cfg.UpstreamTimeout = 0

	var gotAuthorization string
	var gotRiotTokens []string
	handler := New(cfg, WithLimiter(l), WithBaseTransport(testutil.RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotAuthorization = r.Header.Get("Authorization")
		gotRiotTokens = r.Header.Values("X-Riot-Token")
		return testutil.HTTPResponse(http.StatusNoContent, "", nil), nil
	})))

	req := httptest.NewRequest(http.MethodGet, "/europe/riot/account/v1/accounts/me", nil)
	req.Header.Set("Authorization", "Bearer access-token")
	req.Header.Set("X-Riot-Token-Index", "1")
	req.Header.Add("X-Riot-Token", "client-supplied-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusNoContent; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := gotAuthorization, "Bearer access-token"; got != want {
		t.Errorf("Authorization = %q, want %q", got, want)
	}
	if len(gotRiotTokens) != 0 {
		t.Errorf("X-Riot-Token = %q, want absent", gotRiotTokens)
	}
}

func TestProxyNewRejectsBearerWithoutPinnedTokenIndex(t *testing.T) {
	t.Parallel()

	l, err := limiter.New(limiter.Config{
		KeyCount:          2,
		AutomaticKeyCount: 1,
		QueueCapacity:     2,
		DefaultAppLimits:  "20:1",
	})
	if err != nil {
		t.Fatalf("limiter.New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = l.Close()
	})

	cfg := testutil.DummyConfig()
	cfg.Tokens = []string{"automatic-token", "rso-rate-limit-slot"}
	cfg.AutomaticTokenCount = 1
	handler := New(cfg, WithLimiter(l), WithBaseTransport(testutil.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("upstream transport should not be called")
		return nil, nil
	})))

	for _, tt := range []struct {
		name       string
		tokenIndex string
	}{
		{name: "missing index"},
		{name: "automatic index", tokenIndex: "0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/europe/riot/account/v1/accounts/me", nil)
			req.Header.Set("Authorization", "Bearer access-token")
			if tt.tokenIndex != "" {
				req.Header.Set("X-Riot-Token-Index", tt.tokenIndex)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if got, want := rec.Code, http.StatusBadRequest; got != want {
				t.Fatalf("status = %d, want %d", got, want)
			}
		})
	}
}

func TestProxyNewInjectsExplicitlySelectedPinnedToken(t *testing.T) {
	t.Parallel()

	l, err := limiter.New(limiter.Config{
		KeyCount:          2,
		AutomaticKeyCount: 1,
		QueueCapacity:     2,
		DefaultAppLimits:  "20:1",
	})
	if err != nil {
		t.Fatalf("limiter.New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = l.Close()
	})

	cfg := testutil.DummyConfig()
	cfg.Tokens = []string{"automatic-token", "pinned-token"}
	cfg.AutomaticTokenCount = 1
	var gotToken string
	handler := New(cfg, WithLimiter(l), WithBaseTransport(testutil.RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotToken = r.Header.Get("X-Riot-Token")
		return testutil.HTTPResponse(http.StatusNoContent, "", nil), nil
	})))

	req := httptest.NewRequest(http.MethodGet, "/europe/riot/account/v1/accounts/me", nil)
	req.Header.Set("X-Riot-Token-Index", "1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusNoContent; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := gotToken, "pinned-token"; got != want {
		t.Fatalf("X-Riot-Token = %q, want %q", got, want)
	}
}

func TestProxyNewMapsTransportErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantRetry  string
	}{
		{
			name:       "deadline exceeded",
			err:        context.DeadlineExceeded,
			wantStatus: http.StatusRequestTimeout,
			wantRetry:  "1",
		},
		{
			name:       "context canceled",
			err:        context.Canceled,
			wantStatus: 499,
		},
		{
			name:       "generic upstream failure",
			err:        errors.New("boom"),
			wantStatus: http.StatusBadGateway,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := testutil.DummyConfig()
			cfg.UpstreamTimeout = 0
			handler := New(cfg, WithBaseTransport(testutil.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
				return nil, tt.err
			})))

			req := httptest.NewRequest(http.MethodGet, "/europe/riot/account/v1/accounts/me", nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if got, want := rec.Code, tt.wantStatus; got != want {
				t.Fatalf("status = %d, want %d", got, want)
			}
			if got := rec.Header().Get("Retry-After"); got != tt.wantRetry {
				t.Fatalf("Retry-After = %q, want %q", got, tt.wantRetry)
			}
		})
	}
}

// Riot can reset an HTTP/2 stream after sending the response headers. The
// client must get a retryable status instead of a truncated success.
func TestProxyNewMapsFailedUpstreamBodyToBadGateway(t *testing.T) {
	t.Parallel()

	cfg := testutil.DummyConfig()
	cfg.UpstreamTimeout = 0
	handler := New(cfg, WithBaseTransport(testutil.RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(io.MultiReader(strings.NewReader(`[{"puuid":`), iotest.ErrReader(errors.New("stream error: INTERNAL_ERROR")))),
			Request:    r,
		}, nil
	})))

	req := httptest.NewRequest(http.MethodGet, "/europe/riot/account/v1/accounts/me", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if got, want := rec.Code, http.StatusBadGateway; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if strings.Contains(rec.Body.String(), "puuid") {
		t.Fatalf("body = %q, want no partial upstream body", rec.Body.String())
	}
}

func TestProxyRetryUpdatesLimiterBeforeWaiting(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l, err := limiter.New(limiter.Config{
			KeyCount:         1,
			QueueCapacity:    2,
			DefaultAppLimits: "20:1",
		})
		if err != nil {
			t.Fatalf("limiter.New() error = %v", err)
		}
		defer func() { _ = l.Close() }()

		var attempts atomic.Int32
		cfg := testutil.DummyConfig()
		cfg.UpstreamTimeout = 0
		handler := New(cfg, WithLimiter(l), WithBaseTransport(testutil.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
			if attempts.Add(1) == 1 {
				return testutil.HTTPResponse(http.StatusTooManyRequests, "", http.Header{
					"Retry-After":       []string{"1"},
					"X-Rate-Limit-Type": []string{"application"},
				}), nil
			}
			return testutil.HTTPResponse(http.StatusNoContent, "", nil), nil
		})))

		firstDone := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/europe/riot/account/v1/accounts/me", nil))
			firstDone <- recorder
		}()
		synctest.Wait()

		secondDone := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/europe/riot/account/v1/accounts/me", nil))
			secondDone <- recorder
		}()
		synctest.Wait()

		if got, want := attempts.Load(), int32(1); got != want {
			t.Fatalf("upstream attempts during Retry-After = %d, want %d", got, want)
		}
		select {
		case recorder := <-secondDone:
			t.Fatalf("second request completed before Retry-After with status %d", recorder.Code)
		default:
		}

		time.Sleep(time.Second)
		synctest.Wait()
		for i, done := range []<-chan *httptest.ResponseRecorder{firstDone, secondDone} {
			recorder := <-done
			if got, want := recorder.Code, http.StatusNoContent; got != want {
				t.Errorf("request %d status = %d, want %d", i+1, got, want)
			}
		}
	})
}
