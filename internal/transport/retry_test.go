package transport

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type mockTransport struct {
	responses []*http.Response
	errors    []error
	callCount int
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if m.callCount < len(m.errors) && m.errors[m.callCount] != nil {
		err := m.errors[m.callCount]
		m.callCount++
		return nil, err
	}
	if m.callCount < len(m.responses) {
		resp := m.responses[m.callCount]
		m.callCount++
		return resp, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
		Header:     make(http.Header),
	}, nil
}

func TestNewRetryTransport(t *testing.T) {
	tests := []struct {
		name        string
		base        http.RoundTripper
		maxRetries  int
		wantWrapped bool
	}{
		{
			name:        "positive maxRetries wraps transport",
			base:        http.DefaultTransport,
			maxRetries:  2,
			wantWrapped: true,
		},
		{
			name:        "zero maxRetries returns base",
			base:        http.DefaultTransport,
			maxRetries:  0,
			wantWrapped: false,
		},
		{
			name:        "negative maxRetries returns base",
			base:        http.DefaultTransport,
			maxRetries:  -1,
			wantWrapped: false,
		},
		{
			name:        "nil base transport",
			base:        nil,
			maxRetries:  2,
			wantWrapped: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := NewRetryTransport(tt.base, tt.maxRetries)
			if tt.wantWrapped {
				_, ok := rt.(retryTransport)
				if !ok && tt.maxRetries > 0 {
					t.Errorf("NewRetryTransport() should return retryTransport when maxRetries > 0")
				}
			} else {
				if rt != tt.base {
					t.Errorf("NewRetryTransport() should return base when maxRetries <= 0")
				}
			}
		})
	}
}

func TestRetryTransport_SuccessfulRequest(t *testing.T) {
	mock := &mockTransport{
		responses: []*http.Response{
			{
				StatusCode: http.StatusOK,
				Body:       http.NoBody,
				Header:     make(http.Header),
			},
		},
	}

	rt := NewRetryTransport(mock, 3)
	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v, want nil", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("RoundTrip() status = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if mock.callCount != 1 {
		t.Errorf("RoundTrip() callCount = %v, want 1", mock.callCount)
	}
}

func TestRetryTransport_RetriesOn429(t *testing.T) {
	mock := &mockTransport{
		responses: []*http.Response{
			{
				StatusCode: http.StatusTooManyRequests,
				Body:       http.NoBody,
				Header:     http.Header{"Retry-After": []string{"1"}},
			},
			{
				StatusCode: http.StatusOK,
				Body:       http.NoBody,
				Header:     make(http.Header),
			},
		},
	}

	rt := NewRetryTransport(mock, 3)
	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	req.GetBody = func() (io.ReadCloser, error) {
		return http.NoBody, nil
	}

	start := time.Now()
	resp, err := rt.RoundTrip(req)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("RoundTrip() error = %v, want nil", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("RoundTrip() status = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if mock.callCount != 2 {
		t.Errorf("RoundTrip() callCount = %v, want 2", mock.callCount)
	}
	if duration < time.Second {
		t.Errorf("RoundTrip() should have waited at least 1 second, got %v", duration)
	}
}

func TestRetryTransport_RespectsMaxRetries(t *testing.T) {
	mock := &mockTransport{
		responses: []*http.Response{
			{
				StatusCode: http.StatusTooManyRequests,
				Body:       http.NoBody,
				Header:     http.Header{"Retry-After": []string{"0"}},
			},
			{
				StatusCode: http.StatusTooManyRequests,
				Body:       http.NoBody,
				Header:     http.Header{"Retry-After": []string{"0"}},
			},
			{
				StatusCode: http.StatusTooManyRequests,
				Body:       http.NoBody,
				Header:     http.Header{"Retry-After": []string{"0"}},
			},
			{
				StatusCode: http.StatusTooManyRequests,
				Body:       http.NoBody,
				Header:     http.Header{"Retry-After": []string{"0"}},
			},
		},
	}

	rt := NewRetryTransport(mock, 2) // max 2 retries = 3 total attempts
	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	req.GetBody = func() (io.ReadCloser, error) {
		return http.NoBody, nil
	}

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v, want nil", err)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("RoundTrip() status = %v, want %v", resp.StatusCode, http.StatusTooManyRequests)
	}
	if mock.callCount != 3 {
		t.Errorf("RoundTrip() callCount = %v, want 3", mock.callCount)
	}
}

func TestRetryTransport_NoRetryOnNon429(t *testing.T) {
	statusCodes := []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
	}

	for _, statusCode := range statusCodes {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			mock := &mockTransport{
				responses: []*http.Response{
					{
						StatusCode: statusCode,
						Body:       http.NoBody,
						Header:     make(http.Header),
					},
				},
			}

			rt := NewRetryTransport(mock, 3)
			req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)

			resp, err := rt.RoundTrip(req)
			if err != nil {
				t.Fatalf("RoundTrip() error = %v, want nil", err)
			}
			if resp.StatusCode != statusCode {
				t.Errorf("RoundTrip() status = %v, want %v", resp.StatusCode, statusCode)
			}
			if mock.callCount != 1 {
				t.Errorf("RoundTrip() callCount = %v, want 1", mock.callCount)
			}
		})
	}
}

func TestRetryTransport_NoRetryWhenBodyCannotBeReset(t *testing.T) {
	mock := &mockTransport{
		responses: []*http.Response{
			{
				StatusCode: http.StatusTooManyRequests,
				Body:       http.NoBody,
				Header:     make(http.Header),
			},
		},
	}

	rt := NewRetryTransport(mock, 3)
	body := bytes.NewReader([]byte("test"))
	req := httptest.NewRequest(http.MethodPost, "http://example.com", body)

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v, want nil", err)
	}
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("RoundTrip() status = %v, want %v", resp.StatusCode, http.StatusTooManyRequests)
	}
	if mock.callCount != 1 {
		t.Errorf("RoundTrip() callCount = %v, want 1", mock.callCount)
	}
}

func TestRetryTransport_RetryWithNilBody(t *testing.T) {
	mock := &mockTransport{
		responses: []*http.Response{
			{
				StatusCode: http.StatusTooManyRequests,
				Body:       http.NoBody,
				Header:     http.Header{"Retry-After": []string{"0"}},
			},
			{
				StatusCode: http.StatusOK,
				Body:       http.NoBody,
				Header:     make(http.Header),
			},
		},
	}

	rt := NewRetryTransport(mock, 3)
	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Body = nil
	req.GetBody = func() (io.ReadCloser, error) {
		return nil, nil
	}

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v, want nil", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("RoundTrip() status = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if mock.callCount != 2 {
		t.Errorf("RoundTrip() callCount = %v, want 2", mock.callCount)
	}
}

func TestRetryTransport_ContextCancellation(t *testing.T) {
	mock := &mockTransport{
		responses: []*http.Response{
			{
				StatusCode: http.StatusTooManyRequests,
				Body:       http.NoBody,
				Header:     http.Header{"Retry-After": []string{"10"}}, // Long delay
			},
		},
	}

	rt := NewRetryTransport(mock, 3)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil).WithContext(ctx)
	req.GetBody = func() (io.ReadCloser, error) {
		return http.NoBody, nil
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	resp, err := rt.RoundTrip(req)
	if err == nil {
		t.Error("RoundTrip() error = nil, want context cancellation error")
		if resp != nil {
			resp.Body.Close()
		}
	}
	if err != context.Canceled && err != context.DeadlineExceeded {
		if !strings.Contains(err.Error(), "context") {
			t.Errorf("RoundTrip() error = %v, want context cancellation error", err)
		}
	}
}

func TestRetryTransport_GetBodyError(t *testing.T) {
	mock := &mockTransport{
		responses: []*http.Response{
			{
				StatusCode: http.StatusTooManyRequests,
				Body:       http.NoBody,
				Header:     make(http.Header),
			},
		},
	}

	rt := NewRetryTransport(mock, 3)
	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	req.GetBody = func() (io.ReadCloser, error) {
		return nil, io.ErrUnexpectedEOF
	}

	resp, err := rt.RoundTrip(req)
	if err == nil {
		t.Error("RoundTrip() error = nil, want error from GetBody")
		if resp != nil {
			resp.Body.Close()
		}
	}
	if err != io.ErrUnexpectedEOF {
		t.Errorf("RoundTrip() error = %v, want %v", err, io.ErrUnexpectedEOF)
	}
}

func TestRetryTransport_HandlesErrors(t *testing.T) {
	testErr := &http.ProtocolError{ErrorString: "test error"}
	mock := &mockTransport{
		errors: []error{testErr},
	}

	rt := NewRetryTransport(mock, 3)
	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)

	resp, err := rt.RoundTrip(req)
	if err == nil {
		t.Error("RoundTrip() error = nil, want error")
		if resp != nil {
			resp.Body.Close()
		}
	}
	if err != testErr {
		t.Errorf("RoundTrip() error = %v, want %v", err, testErr)
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantMin  time.Duration
		wantMax  time.Duration
		checkNow bool
	}{
		{
			name:    "empty string",
			input:   "",
			wantMin: 0,
			wantMax: 0,
		},
		{
			name:    "seconds as number",
			input:   "5",
			wantMin: 5 * time.Second,
			wantMax: 5 * time.Second,
		},
		{
			name:    "seconds with unit",
			input:   "10s",
			wantMin: 10 * time.Second,
			wantMax: 10 * time.Second,
		},
		{
			name:    "minutes with unit",
			input:   "2m",
			wantMin: 2 * time.Minute,
			wantMax: 2 * time.Minute,
		},
		{
			name:    "invalid number",
			input:   "abc",
			wantMin: 0,
			wantMax: 0,
		},
		{
			name:     "HTTP date in future",
			input:    time.Now().UTC().Add(15 * time.Second).Format(http.TimeFormat),
			wantMin:  14 * time.Second,
			wantMax:  16 * time.Second,
			checkNow: true,
		},
		{
			name:    "HTTP date in past",
			input:   time.Now().UTC().Add(-10 * time.Second).Format(http.TimeFormat),
			wantMin: 0,
			wantMax: 0,
		},
		{
			name:    "invalid HTTP date",
			input:   "not a date",
			wantMin: 0,
			wantMax: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRetryAfter(tt.input)
			if tt.checkNow {
				if got < tt.wantMin || got > tt.wantMax {
					t.Errorf("parseRetryAfter(%q) = %v, want between %v and %v", tt.input, got, tt.wantMin, tt.wantMax)
				}
			} else {
				if got != tt.wantMin {
					t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.input, got, tt.wantMin)
				}
			}
		})
	}
}

func TestRetryTransport_MultipleRetriesWithDifferentDelays(t *testing.T) {
	mock := &mockTransport{
		responses: []*http.Response{
			{
				StatusCode: http.StatusTooManyRequests,
				Body:       http.NoBody,
				Header:     http.Header{"Retry-After": []string{"1"}},
			},
			{
				StatusCode: http.StatusTooManyRequests,
				Body:       http.NoBody,
				Header:     http.Header{"Retry-After": []string{"1"}},
			},
			{
				StatusCode: http.StatusOK,
				Body:       http.NoBody,
				Header:     make(http.Header),
			},
		},
	}

	rt := NewRetryTransport(mock, 3)
	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	req.GetBody = func() (io.ReadCloser, error) {
		return http.NoBody, nil
	}

	start := time.Now()
	resp, err := rt.RoundTrip(req)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("RoundTrip() error = %v, want nil", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("RoundTrip() status = %v, want %v", resp.StatusCode, http.StatusOK)
	}
	if mock.callCount != 3 {
		t.Errorf("RoundTrip() callCount = %v, want 3", mock.callCount)
	}
	if duration < 2*time.Second {
		t.Errorf("RoundTrip() should have waited at least 2 seconds, got %v", duration)
	}
}

func TestRetryTransport_ClosesResponseBody(t *testing.T) {
	body1 := &closeTracker{ReadCloser: http.NoBody}
	body2 := &closeTracker{ReadCloser: http.NoBody}

	mock := &mockTransport{
		responses: []*http.Response{
			{
				StatusCode: http.StatusTooManyRequests,
				Body:       body1,
				Header:     http.Header{"Retry-After": []string{"0"}},
			},
			{
				StatusCode: http.StatusOK,
				Body:       body2,
				Header:     make(http.Header),
			},
		},
	}

	rt := NewRetryTransport(mock, 3)
	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
	req.GetBody = func() (io.ReadCloser, error) {
		return http.NoBody, nil
	}

	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v, want nil", err)
	}

	if !body1.closed {
		t.Error("First response body should be closed before retry")
	}

	resp.Body.Close()
}

type closeTracker struct {
	io.ReadCloser
	closed bool
}

func (c *closeTracker) Close() error {
	c.closed = true
	return c.ReadCloser.Close()
}
