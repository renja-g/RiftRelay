package proxy

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/renja-g/RiftRelay/internal/testutil"
)

func TestProxyNewRewritesRequestAndInjectsToken(t *testing.T) {
	t.Parallel()

	cfg := testutil.DummyConfig()
	cfg.UpstreamTimeout = 0

	var gotHost string
	var gotPath string
	var gotToken string

	handler := New(cfg, WithBaseTransport(testutil.RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		gotHost = r.URL.Host
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-Riot-Token")
		return testutil.HTTPResponse(http.StatusNoContent, "", nil), nil
	})))

	req := httptest.NewRequest(http.MethodGet, "/europe/riot/account/v1/accounts/me", nil)
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
	if got, want := gotToken, cfg.Tokens[0]; got != want {
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
