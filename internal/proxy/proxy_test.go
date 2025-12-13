package proxy

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/renja-g/rp/internal/config"
	"github.com/renja-g/rp/internal/router"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.Config
		opts        []Option
		wantHandler bool
	}{
		{
			name: "basic proxy without options",
			cfg: config.Config{
				Token:      "test-token",
				MaxRetries: 2,
			},
			opts:        nil,
			wantHandler: true,
		},
		{
			name: "proxy with custom transport",
			cfg: config.Config{
				Token:      "test-token",
				MaxRetries: 3,
			},
			opts: []Option{
				WithBaseTransport(http.DefaultTransport),
			},
			wantHandler: true,
		},
		{
			name: "proxy with middleware",
			cfg: config.Config{
				Token:      "test-token",
				MaxRetries: 2,
			},
			opts: []Option{
				WithMiddleware(func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("X-Test", "test")
						next.ServeHTTP(w, r)
					})
				}),
			},
			wantHandler: true,
		},
		{
			name: "proxy with multiple middlewares",
			cfg: config.Config{
				Token:      "test-token",
				MaxRetries: 2,
			},
			opts: []Option{
				WithMiddleware(
					func(next http.Handler) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							w.Header().Set("X-First", "first")
							next.ServeHTTP(w, r)
						})
					},
					func(next http.Handler) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							w.Header().Set("X-Second", "second")
							next.ServeHTTP(w, r)
						})
					},
				),
			},
			wantHandler: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := New(tt.cfg, tt.opts...)
			if (handler != nil) != tt.wantHandler {
				t.Errorf("New() handler = %v, want handler = %v", handler != nil, tt.wantHandler)
			}
		})
	}
}

func TestDirectorDirect(t *testing.T) {
	tests := []struct {
		name          string
		cfg           config.Config
		req           *http.Request
		wantScheme    string
		wantHost      string
		wantPath      string
		wantHeader    string
		wantHeaderVal string
	}{
		{
			name: "director modifies request from context",
			cfg: config.Config{
				Token:      "test-token",
				MaxRetries: 2,
			},
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/original/path", nil)
				info := router.PathInfo{
					Region: "na1",
					Path:   "/lol/summoner/v4/summoners/me",
				}
				return r.WithContext(router.WithPath(r.Context(), info))
			}(),
			wantScheme:    "https",
			wantHost:      "na1.api.riotgames.com",
			wantPath:      "/lol/summoner/v4/summoners/me",
			wantHeader:    "X-Riot-Token",
			wantHeaderVal: "test-token",
		},
		{
			name: "director modifies request from URL path",
			cfg: config.Config{
				Token:      "token-456",
				MaxRetries: 2,
			},
			req:           httptest.NewRequest(http.MethodGet, "/euw1/riot/account/v1/accounts/me", nil),
			wantScheme:    "https",
			wantHost:      "euw1.api.riotgames.com",
			wantPath:      "/riot/account/v1/accounts/me",
			wantHeader:    "X-Riot-Token",
			wantHeaderVal: "token-456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := options{
				baseTransport: http.DefaultTransport,
			}
			rp := newReverseProxy(tt.cfg, o)

			// Call the director function
			rp.Director(tt.req)

			if tt.req.URL.Scheme != tt.wantScheme {
				t.Errorf("Director() Scheme = %v, want %v", tt.req.URL.Scheme, tt.wantScheme)
			}
			if tt.req.URL.Host != tt.wantHost {
				t.Errorf("Director() Host = %v, want %v", tt.req.URL.Host, tt.wantHost)
			}
			if tt.req.Host != tt.wantHost {
				t.Errorf("Director() req.Host = %v, want %v", tt.req.Host, tt.wantHost)
			}
			if tt.req.URL.Path != tt.wantPath {
				t.Errorf("Director() Path = %v, want %v", tt.req.URL.Path, tt.wantPath)
			}
			if got := tt.req.Header.Get(tt.wantHeader); got != tt.wantHeaderVal {
				t.Errorf("Director() Header[%s] = %v, want %v", tt.wantHeader, got, tt.wantHeaderVal)
			}
		})
	}
}

func TestDirectorInvalidPath(t *testing.T) {
	cfg := config.Config{
		Token:      "test-token",
		MaxRetries: 2,
	}

	tests := []struct {
		name           string
		req            func() *http.Request
		shouldModify   bool
		expectedPath   string
		expectedHost   string
		expectedScheme string
	}{
		{
			name: "root path - should not modify",
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/", nil)
			},
			shouldModify:   false,
			expectedPath:   "/",
			expectedHost:   "",
			expectedScheme: "",
		},
		{
			name: "empty path after creation - should not modify",
			req: func() *http.Request {
				r := httptest.NewRequest(http.MethodGet, "/test", nil)
				r.URL.Path = ""
				return r
			},
			shouldModify:   false,
			expectedPath:   "",
			expectedHost:   "",
			expectedScheme: "",
		},
		{
			name: "path with only region - will modify (path becomes /)",
			req: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "/na1", nil)
			},
			shouldModify:   true,
			expectedPath:   "/",
			expectedHost:   "na1.api.riotgames.com",
			expectedScheme: "https",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := options{
				baseTransport: http.DefaultTransport,
			}
			rp := newReverseProxy(cfg, o)

			req := tt.req()
			rp.Director(req)

			if req.URL.Path != tt.expectedPath {
				t.Errorf("Director() Path = %v, want %v", req.URL.Path, tt.expectedPath)
			}
			if tt.shouldModify {
				if req.URL.Host != tt.expectedHost {
					t.Errorf("Director() Host = %v, want %v", req.URL.Host, tt.expectedHost)
				}
				if req.URL.Scheme != tt.expectedScheme {
					t.Errorf("Director() Scheme = %v, want %v", req.URL.Scheme, tt.expectedScheme)
				}
			} else {
				if req.URL.Host != "" {
					t.Errorf("Director() should not modify Host for invalid path, got %v", req.URL.Host)
				}
				if req.URL.Scheme != "" {
					t.Errorf("Director() should not modify Scheme for invalid path, got %v", req.URL.Scheme)
				}
			}
		})
	}
}

func TestApplyMiddleware(t *testing.T) {
	tests := []struct {
		name        string
		middlewares []Middleware
		wantOrder   []string
	}{
		{
			name: "single middleware",
			middlewares: []Middleware{
				func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("X-Order", "1")
						next.ServeHTTP(w, r)
					})
				},
			},
			wantOrder: []string{"1"},
		},
		{
			name: "multiple middlewares applied in reverse order",
			middlewares: []Middleware{
				func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Add("X-Order", "1")
						next.ServeHTTP(w, r)
					})
				},
				func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Add("X-Order", "2")
						next.ServeHTTP(w, r)
					})
				},
				func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Add("X-Order", "3")
						next.ServeHTTP(w, r)
					})
				},
			},
			wantOrder: []string{"1", "2", "3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextCalled := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			})

			handler := applyMiddleware(next, tt.middlewares...)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if !nextCalled {
				t.Error("applyMiddleware() next handler was not called")
			}

			order := rec.Header().Values("X-Order")
			if len(order) != len(tt.wantOrder) {
				t.Errorf("applyMiddleware() order length = %v, want %v", len(order), len(tt.wantOrder))
			}
			for i, v := range order {
				if i < len(tt.wantOrder) && v != tt.wantOrder[i] {
					t.Errorf("applyMiddleware() order[%d] = %v, want %v", i, v, tt.wantOrder[i])
				}
			}
		})
	}
}

func TestBufferPool(t *testing.T) {
	pool := &sync.Pool{
		New: func() any {
			buf := make([]byte, 32*1024)
			return &buf
		},
	}
	bp := bufferPool{pool: pool}

	t.Run("get and put buffer", func(t *testing.T) {
		buf1 := bp.Get()
		if len(buf1) != 32*1024 {
			t.Errorf("bufferPool.Get() length = %v, want %v", len(buf1), 32*1024)
		}

		bp.Put(buf1)

		buf2 := bp.Get()
		if len(buf2) != 32*1024 {
			t.Errorf("bufferPool.Get() length = %v, want %v", len(buf2), 32*1024)
		}
	})

	t.Run("multiple gets return buffers", func(t *testing.T) {
		bufs := make([][]byte, 10)
		for i := range bufs {
			bufs[i] = bp.Get()
			if len(bufs[i]) != 32*1024 {
				t.Errorf("bufferPool.Get() [%d] length = %v, want %v", i, len(bufs[i]), 32*1024)
			}
		}

		for _, buf := range bufs {
			bp.Put(buf)
		}
	})
}

func TestErrorHandler(t *testing.T) {
	cfg := config.Config{
		Token:      "test-token",
		MaxRetries: 2,
	}

	o := options{
		baseTransport: http.DefaultTransport,
	}
	rp := newReverseProxy(cfg, o)

	req := httptest.NewRequest(http.MethodGet, "/na1/lol/summoner/v4/summoners/me", nil)
	rec := httptest.NewRecorder()

	testErr := httputil.ErrLineTooLong
	rp.ErrorHandler(rec, req, testErr)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("ErrorHandler() status code = %v, want %v", rec.Code, http.StatusBadGateway)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "upstream unavailable") {
		t.Errorf("ErrorHandler() body = %v, want to contain 'upstream unavailable'", body)
	}
}

func TestWithBaseTransport(t *testing.T) {
	customTransport := &http.Transport{
		MaxIdleConns: 100,
	}

	opt := WithBaseTransport(customTransport)
	o := options{
		baseTransport: http.DefaultTransport,
	}

	opt(&o)

	if o.baseTransport != customTransport {
		t.Errorf("WithBaseTransport() baseTransport = %v, want %v", o.baseTransport, customTransport)
	}
}

func TestWithMiddleware(t *testing.T) {
	mw1Called := false
	mw2Called := false
	mw1 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mw1Called = true
			next.ServeHTTP(w, r)
		})
	}
	mw2 := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mw2Called = true
			next.ServeHTTP(w, r)
		})
	}

	opt := WithMiddleware(mw1, mw2)
	o := options{
		middlewares: []Middleware{},
	}

	opt(&o)

	if len(o.middlewares) != 2 {
		t.Errorf("WithMiddleware() middlewares length = %v, want %v", len(o.middlewares), 2)
	}

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	})

	handler := o.middlewares[0](o.middlewares[1](next))
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !mw1Called {
		t.Error("WithMiddleware() first middleware was not called")
	}
	if !mw2Called {
		t.Error("WithMiddleware() second middleware was not called")
	}
	if !nextCalled {
		t.Error("WithMiddleware() next handler was not called")
	}
}

func TestProxyIntegration(t *testing.T) {
	cfg := config.Config{
		Token:      "integration-token",
		MaxRetries: 2,
	}

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Riot-Token") != cfg.Token {
			t.Errorf("Backend received token = %v, want %v", r.Header.Get("X-Riot-Token"), cfg.Token)
		}
		if r.URL.Scheme != "https" {
			t.Errorf("Backend received scheme = %v, want https", r.URL.Scheme)
		}
		if !strings.Contains(r.URL.Host, ".api.riotgames.com") {
			t.Errorf("Backend received host = %v, want to contain .api.riotgames.com", r.URL.Host)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	}))
	defer backend.Close()

	middlewareCalled := false
	handler := New(cfg, WithMiddleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			middlewareCalled = true
			next.ServeHTTP(w, r)
		})
	}))

	req := httptest.NewRequest(http.MethodGet, "/na1/lol/summoner/v4/summoners/me", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !middlewareCalled {
		t.Error("Middleware was not called in integration test")
	}
}

func TestDefaultTransport(t *testing.T) {
	transport := defaultTransport()

	if transport == nil {
		t.Error("defaultTransport() returned nil")
	}

	if !transport.ForceAttemptHTTP2 {
		t.Error("defaultTransport() ForceAttemptHTTP2 = false, want true")
	}

	if transport.MaxIdleConns != 512 {
		t.Errorf("defaultTransport() MaxIdleConns = %v, want %v", transport.MaxIdleConns, 512)
	}

	if transport.IdleConnTimeout != 90*time.Second {
		t.Errorf("defaultTransport() IdleConnTimeout = %v, want %v", transport.IdleConnTimeout, 90*time.Second)
	}

	if transport.TLSHandshakeTimeout != 10*time.Second {
		t.Errorf("defaultTransport() TLSHandshakeTimeout = %v, want %v", transport.TLSHandshakeTimeout, 10*time.Second)
	}

	if transport.ExpectContinueTimeout != 1*time.Second {
		t.Errorf("defaultTransport() ExpectContinueTimeout = %v, want %v", transport.ExpectContinueTimeout, 1*time.Second)
	}
}

func TestMiddlewareFromGate(t *testing.T) {
	tests := []struct {
		name        string
		gate        RequestGate
		wantCalled  bool
		wantHeaders map[string]string
	}{
		{
			name: "gate wraps handler correctly",
			gate: &mockRequestGate{
				wrapFunc: func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("X-Gate", "applied")
						next.ServeHTTP(w, r)
					})
				},
			},
			wantCalled: true,
			wantHeaders: map[string]string{
				"X-Gate": "applied",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			middleware := MiddlewareFromGate(tt.gate)

			nextCalled := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			})

			handler := middleware(next)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if nextCalled != tt.wantCalled {
				t.Errorf("MiddlewareFromGate() next called = %v, want %v", nextCalled, tt.wantCalled)
			}

			for key, val := range tt.wantHeaders {
				if got := rec.Header().Get(key); got != val {
					t.Errorf("MiddlewareFromGate() header %s = %v, want %v", key, got, val)
				}
			}
		})
	}
}

func TestMiddlewareFromScheduler(t *testing.T) {
	tests := []struct {
		name        string
		scheduler   Scheduler
		wantCalled  bool
		wantHeaders map[string]string
	}{
		{
			name: "scheduler wraps handler correctly",
			scheduler: &mockScheduler{
				wrapFunc: func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.Header().Set("X-Scheduler", "applied")
						next.ServeHTTP(w, r)
					})
				},
			},
			wantCalled: true,
			wantHeaders: map[string]string{
				"X-Scheduler": "applied",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			middleware := MiddlewareFromScheduler(tt.scheduler)

			nextCalled := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			})

			handler := middleware(next)

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if nextCalled != tt.wantCalled {
				t.Errorf("MiddlewareFromScheduler() next called = %v, want %v", nextCalled, tt.wantCalled)
			}

			for key, val := range tt.wantHeaders {
				if got := rec.Header().Get(key); got != val {
					t.Errorf("MiddlewareFromScheduler() header %s = %v, want %v", key, got, val)
				}
			}
		})
	}
}

// mockRequestGate implements RequestGate for testing
type mockRequestGate struct {
	wrapFunc func(next http.Handler) http.Handler
}

func (m *mockRequestGate) Wrap(next http.Handler) http.Handler {
	if m.wrapFunc != nil {
		return m.wrapFunc(next)
	}
	return next
}

// mockScheduler implements Scheduler for testing
type mockScheduler struct {
	wrapFunc func(next http.Handler) http.Handler
}

func (m *mockScheduler) Wrap(next http.Handler) http.Handler {
	if m.wrapFunc != nil {
		return m.wrapFunc(next)
	}
	return next
}
