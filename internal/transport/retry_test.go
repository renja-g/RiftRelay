package transport

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type testResponse struct {
	statusCode int
	retryAfter string
}

type sequenceRoundTripper struct {
	responses []testResponse
	calls     int
}

func (s *sequenceRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	idx := s.calls
	if idx >= len(s.responses) {
		idx = len(s.responses) - 1
	}
	s.calls++

	resp := s.responses[idx]
	header := make(http.Header)
	if resp.retryAfter != "" {
		header.Set("Retry-After", resp.retryAfter)
	}

	return &http.Response{
		StatusCode: resp.statusCode,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader("ok")),
		Request:    r,
	}, nil
}

func TestWithRetryAfter429(t *testing.T) {
	tests := []struct {
		name       string
		maxRetries int
		responses  []testResponse
		request    func(t *testing.T) *http.Request
		wantStatus int
		wantCalls  int
	}{
		{
			name:       "retries once when retry-after is present",
			maxRetries: 3,
			responses: []testResponse{
				{statusCode: http.StatusTooManyRequests, retryAfter: "0"},
				{statusCode: http.StatusOK},
			},
			request: func(t *testing.T) *http.Request {
				t.Helper()
				req, err := http.NewRequest(http.MethodGet, "https://example.com/test", nil)
				if err != nil {
					t.Fatalf("new request: %v", err)
				}
				return req
			},
			wantStatus: http.StatusOK,
			wantCalls:  2,
		},
		{
			name:       "does not retry when retry-after header is missing",
			maxRetries: 3,
			responses: []testResponse{
				{statusCode: http.StatusTooManyRequests},
				{statusCode: http.StatusOK},
			},
			request: func(t *testing.T) *http.Request {
				t.Helper()
				req, err := http.NewRequest(http.MethodGet, "https://example.com/test", nil)
				if err != nil {
					t.Fatalf("new request: %v", err)
				}
				return req
			},
			wantStatus: http.StatusTooManyRequests,
			wantCalls:  1,
		},
		{
			name:       "stops retrying after max retries",
			maxRetries: 3,
			responses: []testResponse{
				{statusCode: http.StatusTooManyRequests, retryAfter: "0"},
				{statusCode: http.StatusTooManyRequests, retryAfter: "0"},
				{statusCode: http.StatusTooManyRequests, retryAfter: "0"},
				{statusCode: http.StatusTooManyRequests, retryAfter: "0"},
				{statusCode: http.StatusOK},
			},
			request: func(t *testing.T) *http.Request {
				t.Helper()
				req, err := http.NewRequest(http.MethodGet, "https://example.com/test", nil)
				if err != nil {
					t.Fatalf("new request: %v", err)
				}
				return req
			},
			wantStatus: http.StatusTooManyRequests,
			wantCalls:  4,
		},
		{
			name:       "does not retry when body cannot be replayed",
			maxRetries: 3,
			responses: []testResponse{
				{statusCode: http.StatusTooManyRequests, retryAfter: "0"},
				{statusCode: http.StatusOK},
			},
			request: func(t *testing.T) *http.Request {
				t.Helper()
				req, err := http.NewRequest(http.MethodPost, "https://example.com/test", strings.NewReader("payload"))
				if err != nil {
					t.Fatalf("new request: %v", err)
				}
				req.GetBody = nil
				return req
			},
			wantStatus: http.StatusTooManyRequests,
			wantCalls:  1,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			rt := &sequenceRoundTripper{responses: tt.responses}
			retryingTransport := WithRetryAfter429(rt, tt.maxRetries)

			resp, err := retryingTransport.RoundTrip(tt.request(t))
			if err != nil {
				t.Fatalf("round trip: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, resp.StatusCode)
			}
			if rt.calls != tt.wantCalls {
				t.Fatalf("expected %d attempts, got %d", tt.wantCalls, rt.calls)
			}
		})
	}
}

func TestWithRetryAfter429HonorsContextCancellation(t *testing.T) {
	rt := &sequenceRoundTripper{
		responses: []testResponse{
			{statusCode: http.StatusTooManyRequests, retryAfter: "60"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com/test", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	retryingTransport := WithRetryAfter429(rt, 3)
	_, err = retryingTransport.RoundTrip(req)
	if err == nil {
		t.Fatalf("expected context cancellation error")
	}
}

func TestParseRetryAfterHeader(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()

	tests := []struct {
		name     string
		header   string
		wantWait time.Duration
		wantOK   bool
	}{
		{
			name:     "parses delta seconds",
			header:   "3",
			wantWait: 3 * time.Second,
			wantOK:   true,
		},
		{
			name:     "parses http date",
			header:   now.Add(5 * time.Second).Format(http.TimeFormat),
			wantWait: 5 * time.Second,
			wantOK:   true,
		},
		{
			name:     "past date results in immediate retry",
			header:   now.Add(-5 * time.Second).Format(http.TimeFormat),
			wantWait: 0,
			wantOK:   true,
		},
		{
			name:   "invalid header",
			header: "later",
			wantOK: false,
		},
		{
			name:   "empty header",
			header: "",
			wantOK: false,
		},
		{
			name:   "negative seconds are invalid",
			header: "-1",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			gotWait, gotOK := parseRetryAfterHeader(tt.header, now)
			if gotOK != tt.wantOK {
				t.Fatalf("expected ok=%v, got %v", tt.wantOK, gotOK)
			}
			if gotWait != tt.wantWait {
				t.Fatalf("expected wait=%s, got %s", tt.wantWait, gotWait)
			}
		})
	}
}
