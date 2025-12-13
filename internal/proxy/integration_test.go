package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/renja-g/rp/internal/config"
)

func TestProxyIntegration_SuccessfulRequest(t *testing.T) {
	tests := []struct {
		name            string
		path            string
		method          string
		requestBody     string
		responseStatus  int
		responseBody    string
		responseHeaders map[string]string
		wantStatus      int
		wantBody        string
		wantHeaders     map[string]string
	}{
		{
			name:           "GET request to status endpoint",
			path:           "/na1/lol/status/v4/platform-data",
			method:         http.MethodGet,
			responseStatus: http.StatusOK,
			responseBody:   `{"status":"operational"}`,
			responseHeaders: map[string]string{
				"Content-Type": "application/json",
			},
			wantStatus: http.StatusOK,
			wantBody:   `{"status":"operational"}`,
			wantHeaders: map[string]string{
				"Content-Type": "application/json",
			},
		},
		{
			name:           "POST request with body",
			path:           "/euw1/riot/account/v1/accounts/me",
			method:         http.MethodPost,
			requestBody:    `{"test":"data"}`,
			responseStatus: http.StatusCreated,
			responseBody:   `{"id":"123"}`,
			wantStatus:     http.StatusCreated,
			wantBody:       `{"id":"123"}`,
		},
		{
			name:           "parameterized path",
			path:           "/kr/lol/summoner/v4/summoners/by-puuid/abc123def456",
			method:         http.MethodGet,
			responseStatus: http.StatusOK,
			responseBody:   `{"puuid":"abc123def456"}`,
			wantStatus:     http.StatusOK,
			wantBody:       `{"puuid":"abc123def456"}`,
		},
		{
			name:           "multi-parameter path",
			path:           "/na1/riot/account/v1/accounts/by-riot-id/PlayerName/TAG123",
			method:         http.MethodGet,
			responseStatus: http.StatusOK,
			responseBody:   `{"gameName":"PlayerName"}`,
			wantStatus:     http.StatusOK,
			wantBody:       `{"gameName":"PlayerName"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.requestBody != "" {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Fatalf("Failed to read request body: %v", err)
					}
					if string(body) != tt.requestBody {
						t.Errorf("Backend received body = %v, want %v", string(body), tt.requestBody)
					}
				}

				for k, v := range tt.responseHeaders {
					w.Header().Set(k, v)
				}

				w.WriteHeader(tt.responseStatus)
				w.Write([]byte(tt.responseBody))
			}))
			defer backend.Close()

			transport := &testTransport{
				baseURL: backend.URL,
			}

			cfg := config.Config{
				Token:      "test-token",
				MaxRetries: 2,
			}

			handler := New(cfg, WithBaseTransport(transport))

			var body io.Reader
			if tt.requestBody != "" {
				body = bytes.NewReader([]byte(tt.requestBody))
			}
			req := httptest.NewRequest(tt.method, tt.path, body)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("Response status = %v, want %v", rec.Code, tt.wantStatus)
			}

			if rec.Body.String() != tt.wantBody {
				t.Errorf("Response body = %v, want %v", rec.Body.String(), tt.wantBody)
			}

			for k, v := range tt.wantHeaders {
				if got := rec.Header().Get(k); got != v {
					t.Errorf("Response header %s = %v, want %v", k, got, v)
				}
			}
		})
	}
}

func TestProxyIntegration_ErrorHandling(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		backendHandler http.HandlerFunc
		wantStatus     int
		wantBody       string
	}{
		{
			name: "upstream connection error",
			path: "/na1/lol/status/v4/platform-data",
			backendHandler: func(w http.ResponseWriter, r *http.Request) {
				hj, ok := w.(http.Hijacker)
				if ok {
					conn, _, _ := hj.Hijack()
					conn.Close()
				}
			},
			wantStatus: http.StatusBadGateway,
			wantBody:   "upstream unavailable\n",
		},
		{
			name: "backend returns 500",
			path: "/euw1/lol/status/v4/platform-data",
			backendHandler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte("internal server error"))
			},
			wantStatus: http.StatusInternalServerError,
			wantBody:   "internal server error",
		},
		{
			name: "backend returns 404",
			path: "/kr/lol/summoner/v4/summoners/by-puuid/invalid",
			backendHandler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte("not found"))
			},
			wantStatus: http.StatusNotFound,
			wantBody:   "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := httptest.NewServer(tt.backendHandler)
			defer backend.Close()

			transport := &testTransport{
				baseURL: backend.URL,
			}

			cfg := config.Config{
				Token:      "test-token",
				MaxRetries: 2,
			}

			handler := New(cfg, WithBaseTransport(transport))

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("Response status = %v, want %v", rec.Code, tt.wantStatus)
			}

			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("Response body = %v, want to contain %v", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestProxyIntegration_InvalidPaths(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{
			name:       "root path",
			path:       "/",
			wantStatus: http.StatusBadRequest,
			wantBody:   "expected path /{region}/riot/...\n",
		},
		{
			name:       "only region",
			path:       "/na1",
			wantStatus: http.StatusBadRequest,
			wantBody:   "expected path /{region}/riot/...\n",
		},
		{
			name:       "region with trailing slash",
			path:       "/na1/",
			wantStatus: http.StatusBadRequest,
			wantBody:   "expected path /{region}/riot/...\n",
		},
		{
			name:       "invalid path format",
			path:       "/invalid",
			wantStatus: http.StatusBadRequest,
			wantBody:   "expected path /{region}/riot/...\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				Token:      "test-token",
				MaxRetries: 2,
			}

			handler := New(cfg)

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("Response status = %v, want %v", rec.Code, tt.wantStatus)
			}

			if rec.Body.String() != tt.wantBody {
				t.Errorf("Response body = %v, want %v", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestProxyIntegration_MiddlewareChain(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))
	defer backend.Close()

	transport := &testTransport{
		baseURL: backend.URL,
	}

	cfg := config.Config{
		Token:      "test-token",
		MaxRetries: 2,
	}

	middlewareOrder := []string{}
	middleware1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			middlewareOrder = append(middlewareOrder, "1")
			w.Header().Set("X-Middleware-1", "applied")
			next.ServeHTTP(w, r)
		})
	}

	middleware2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			middlewareOrder = append(middlewareOrder, "2")
			w.Header().Set("X-Middleware-2", "applied")
			next.ServeHTTP(w, r)
		})
	}

	middleware3 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			middlewareOrder = append(middlewareOrder, "3")
			w.Header().Set("X-Middleware-3", "applied")
			next.ServeHTTP(w, r)
		})
	}

	handler := New(cfg, WithBaseTransport(transport), WithMiddleware(middleware1, middleware2, middleware3))

	req := httptest.NewRequest(http.MethodGet, "/na1/lol/status/v4/platform-data", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	wantOrder := []string{"1", "2", "3"}
	if len(middlewareOrder) != len(wantOrder) {
		t.Errorf("Middleware order length = %v, want %v", len(middlewareOrder), len(wantOrder))
	}
	for i, v := range middlewareOrder {
		if i < len(wantOrder) && v != wantOrder[i] {
			t.Errorf("Middleware order[%d] = %v, want %v", i, v, wantOrder[i])
		}
	}

	if rec.Header().Get("X-Middleware-1") != "applied" {
		t.Error("X-Middleware-1 header not found")
	}
	if rec.Header().Get("X-Middleware-2") != "applied" {
		t.Error("X-Middleware-2 header not found")
	}
	if rec.Header().Get("X-Middleware-3") != "applied" {
		t.Error("X-Middleware-3 header not found")
	}

	if rec.Code != http.StatusOK {
		t.Errorf("Response status = %v, want %v", rec.Code, http.StatusOK)
	}
}

func TestProxyIntegration_RetryBehavior(t *testing.T) {
	attemptCount := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		if attemptCount < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("rate limited"))
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("success"))
		}
	}))
	defer backend.Close()

	transport := &testTransport{
		baseURL: backend.URL,
	}

	cfg := config.Config{
		Token:      "test-token",
		MaxRetries: 5,
	}

	handler := New(cfg, WithBaseTransport(transport))

	req := httptest.NewRequest(http.MethodGet, "/na1/lol/status/v4/platform-data", nil)
	req.GetBody = func() (io.ReadCloser, error) {
		return http.NoBody, nil
	}
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if attemptCount != 3 {
		t.Errorf("Backend was called %d times, want 3", attemptCount)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("Response status = %v, want %v", rec.Code, http.StatusOK)
	}

	if rec.Body.String() != "success" {
		t.Errorf("Response body = %v, want success", rec.Body.String())
	}
}

func TestProxyIntegration_RetryRespectsMaxRetries(t *testing.T) {
	attemptCount := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte("rate limited"))
	}))
	defer backend.Close()

	transport := &testTransport{
		baseURL: backend.URL,
	}

	cfg := config.Config{
		Token:      "test-token",
		MaxRetries: 2,
	}

	handler := New(cfg, WithBaseTransport(transport))

	req := httptest.NewRequest(http.MethodGet, "/na1/lol/status/v4/platform-data", nil)
	req.GetBody = func() (io.ReadCloser, error) {
		return http.NoBody, nil
	}
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if attemptCount != 3 {
		t.Errorf("Backend was called %d times, want 3", attemptCount)
	}

	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("Response status = %v, want %v", rec.Code, http.StatusTooManyRequests)
	}
}

func TestProxyIntegration_RequestHeadersForwarded(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Custom-Header") != "custom-value" {
			t.Errorf("Backend received X-Custom-Header = %v, want custom-value", r.Header.Get("X-Custom-Header"))
		}
		if r.Header.Get("User-Agent") != "test-agent" {
			t.Errorf("Backend received User-Agent = %v, want test-agent", r.Header.Get("User-Agent"))
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer backend.Close()

	transport := &testTransport{
		baseURL: backend.URL,
	}

	cfg := config.Config{
		Token:      "test-token",
		MaxRetries: 2,
	}

	handler := New(cfg, WithBaseTransport(transport))

	req := httptest.NewRequest(http.MethodGet, "/na1/lol/status/v4/platform-data", nil)
	req.Header.Set("X-Custom-Header", "custom-value")
	req.Header.Set("User-Agent", "test-agent")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Response status = %v, want %v", rec.Code, http.StatusOK)
	}
}

func TestProxyIntegration_DifferentRegions(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{
			name: "NA region",
			path: "/na1/lol/status/v4/platform-data",
		},
		{
			name: "EUW region",
			path: "/euw1/lol/status/v4/platform-data",
		},
		{
			name: "KR region",
			path: "/kr/lol/status/v4/platform-data",
		},
		{
			name: "BR region",
			path: "/br1/lol/status/v4/platform-data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("ok"))
			}))
			defer backend.Close()

			transport := &testTransport{
				baseURL: backend.URL,
			}

			cfg := config.Config{
				Token:      "test-token",
				MaxRetries: 2,
			}

			handler := New(cfg, WithBaseTransport(transport))

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("Response status = %v, want %v", rec.Code, http.StatusOK)
			}
		})
	}
}

func TestProxyIntegration_LargeResponseBody(t *testing.T) {
	largeBody := strings.Repeat("a", 100*1024)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(largeBody))
	}))
	defer backend.Close()

	transport := &testTransport{
		baseURL: backend.URL,
	}

	cfg := config.Config{
		Token:      "test-token",
		MaxRetries: 2,
	}

	handler := New(cfg, WithBaseTransport(transport))

	req := httptest.NewRequest(http.MethodGet, "/na1/lol/status/v4/platform-data", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Response status = %v, want %v", rec.Code, http.StatusOK)
	}

	if rec.Body.Len() != len(largeBody) {
		t.Errorf("Response body length = %v, want %v", rec.Body.Len(), len(largeBody))
	}
}

type testTransport struct {
	baseURL string
}

func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	testURL := t.baseURL + req.URL.Path
	if req.URL.RawQuery != "" {
		testURL += "?" + req.URL.RawQuery
	}

	var body io.ReadCloser
	if req.GetBody != nil {
		var err error
		body, err = req.GetBody()
		if err != nil {
			return nil, err
		}
	} else if req.Body != nil {
		bodyBytes, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	testReq, err := http.NewRequestWithContext(req.Context(), req.Method, testURL, body)
	if err != nil {
		if body != nil {
			body.Close()
		}
		return nil, err
	}

	for k, v := range req.Header {
		testReq.Header[k] = v
	}

	client := &http.Client{
		Timeout: 5 * time.Second,
	}
	return client.Do(testReq)
}
