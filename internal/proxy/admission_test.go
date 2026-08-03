package proxy

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/renja-g/RiftRelay/internal/limiter"
	"github.com/renja-g/RiftRelay/internal/router"
)

func TestAuthModeFromHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		header  http.Header
		want    authMode
		wantErr bool
	}{
		{
			name: "API key",
			want: authModeAPIKey,
		},
		{
			name:   "Bearer",
			header: http.Header{"Authorization": []string{"bEaReR access-token"}},
			want:   authModeBearer,
		},
		{
			name:   "Bearer with whitespace",
			header: http.Header{"Authorization": []string{"  Bearer   access-token  "}},
			want:   authModeBearer,
		},
		{
			name:    "unsupported scheme",
			header:  http.Header{"Authorization": []string{"Basic credentials"}},
			wantErr: true,
		},
		{
			name:    "missing Bearer token",
			header:  http.Header{"Authorization": []string{"Bearer"}},
			wantErr: true,
		},
		{
			name:    "Bearer token with whitespace",
			header:  http.Header{"Authorization": []string{"Bearer access token"}},
			wantErr: true,
		},
		{
			name:    "duplicate Authorization",
			header:  http.Header{"Authorization": []string{"Bearer first", "Bearer second"}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := authModeFromHeader(tt.header)
			if (err != nil) != tt.wantErr {
				t.Fatalf("authModeFromHeader() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("authModeFromHeader() = %d, want %d", got, tt.want)
			}
		})
	}
}

func BenchmarkAuthModeFromHeader(b *testing.B) {
	tests := []struct {
		name   string
		header http.Header
	}{
		{name: "APIKey", header: make(http.Header)},
		{name: "Bearer", header: http.Header{"Authorization": []string{"Bearer access-token"}}},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			b.ReportAllocs()
			var mode authMode
			var err error
			for b.Loop() {
				mode, err = authModeFromHeader(tt.header)
			}
			runtime.KeepAlive(mode)
			runtime.KeepAlive(err)
		})
	}
}

func TestAdmissionMiddleware(t *testing.T) {
	t.Parallel()

	newLimiter := func(t *testing.T) *limiter.Limiter {
		t.Helper()

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
		return l
	}

	t.Run("passes admission context to downstream handler", func(t *testing.T) {
		t.Parallel()

		l := newLimiter(t)
		pathInfo := router.PathInfo{
			Region:       "europe",
			UpstreamPath: "/riot/account/v1/accounts/me",
			Bucket:       "europe:riot/account/v1/accounts/me",
		}

		handler := admissionMiddleware(l, nil, time.Second, 1)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			info, ok := admissionFromContext(r.Context())
			if !ok {
				t.Fatal("admissionFromContext() ok = false, want true")
			}
			if info.Region != pathInfo.Region || info.Bucket != pathInfo.Bucket || info.Priority != "high" || info.BudgetID != "default" || info.KeyIndex != 0 || info.AuthMode != authModeAPIKey {
				t.Fatalf("admission info = %#v", info)
			}
			w.WriteHeader(http.StatusNoContent)
		}))

		req := httptest.NewRequest(http.MethodGet, "/europe/riot/account/v1/accounts/me", nil)
		req.Header.Set("X-Priority", "high")
		req = req.WithContext(router.WithPath(req.Context(), pathInfo))
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNoContent; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
	})

	t.Run("rejects malformed token index header", func(t *testing.T) {
		t.Parallel()

		l := newLimiter(t)
		handler := admissionMiddleware(l, nil, time.Second, 1)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("downstream handler should not be called")
		}))

		req := httptest.NewRequest(http.MethodGet, "/europe/riot/account/v1/accounts/me", nil)
		req.Header.Set("X-Riot-Token-Index", "abc")
		req = req.WithContext(router.WithPath(req.Context(), router.PathInfo{
			Region:       "europe",
			UpstreamPath: "/riot/account/v1/accounts/me",
			Bucket:       "europe:riot/account/v1/accounts/me",
		}))
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
	})

	t.Run("rejects out of range token index", func(t *testing.T) {
		t.Parallel()

		l := newLimiter(t)
		handler := admissionMiddleware(l, nil, time.Second, 1)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("downstream handler should not be called")
		}))

		req := httptest.NewRequest(http.MethodGet, "/europe/riot/account/v1/accounts/me", nil)
		req.Header.Set("X-Riot-Token-Index", "5")
		req = req.WithContext(router.WithPath(req.Context(), router.PathInfo{
			Region:       "europe",
			UpstreamPath: "/riot/account/v1/accounts/me",
			Bucket:       "europe:riot/account/v1/accounts/me",
		}))
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
	})

	t.Run("passes configured budget id", func(t *testing.T) {
		t.Parallel()

		l, err := limiter.New(limiter.Config{
			KeyCount:         1,
			QueueCapacity:    2,
			DefaultAppLimits: "20:1",
			RateBudgets: map[string]limiter.BudgetConfig{
				"worker": {Share: 0.8},
			},
		})
		if err != nil {
			t.Fatalf("limiter.New() error = %v", err)
		}
		t.Cleanup(func() {
			_ = l.Close()
		})

		handler := admissionMiddleware(l, nil, time.Second, 1)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			info, ok := admissionFromContext(r.Context())
			if !ok {
				t.Fatal("admissionFromContext() ok = false, want true")
			}
			if got, want := info.BudgetID, "worker"; got != want {
				t.Fatalf("BudgetID = %q, want %q", got, want)
			}
			w.WriteHeader(http.StatusNoContent)
		}))

		req := httptest.NewRequest(http.MethodGet, "/europe/riot/account/v1/accounts/me", nil)
		req.Header.Set("X-Rate-Budget", "worker")
		req = req.WithContext(router.WithPath(req.Context(), router.PathInfo{
			Region:       "europe",
			UpstreamPath: "/riot/account/v1/accounts/me",
			Bucket:       "europe:riot/account/v1/accounts/me",
		}))
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNoContent; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
	})

	t.Run("rejects unknown budget id", func(t *testing.T) {
		t.Parallel()

		l := newLimiter(t)
		handler := admissionMiddleware(l, nil, time.Second, 1)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("downstream handler should not be called")
		}))

		req := httptest.NewRequest(http.MethodGet, "/europe/riot/account/v1/accounts/me", nil)
		req.Header.Set("X-Rate-Budget", "missing")
		req = req.WithContext(router.WithPath(req.Context(), router.PathInfo{
			Region:       "europe",
			UpstreamPath: "/riot/account/v1/accounts/me",
			Bucket:       "europe:riot/account/v1/accounts/me",
		}))
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
	})

	t.Run("passes Bearer request with pinned token index", func(t *testing.T) {
		t.Parallel()

		l := newLimiter(t)
		handler := admissionMiddleware(l, nil, time.Second, 1)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			info, ok := admissionFromContext(r.Context())
			if !ok {
				t.Fatal("admissionFromContext() ok = false, want true")
			}
			if got, want := info.KeyIndex, 1; got != want {
				t.Errorf("admission KeyIndex = %d, want %d", got, want)
			}
			if got, want := info.AuthMode, authModeBearer; got != want {
				t.Errorf("admission AuthMode = %d, want %d", got, want)
			}
			w.WriteHeader(http.StatusNoContent)
		}))

		req := httptest.NewRequest(http.MethodGet, "/europe/riot/account/v1/accounts/me", nil)
		req.Header.Set("Authorization", "Bearer access-token")
		req.Header.Set("X-Riot-Token-Index", "1")
		req = req.WithContext(router.WithPath(req.Context(), router.PathInfo{
			Region:       "europe",
			UpstreamPath: "/riot/account/v1/accounts/me",
			Bucket:       "europe:riot/account/v1/accounts/me",
		}))
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNoContent; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
	})

	for _, tt := range []struct {
		name       string
		auth       []string
		tokenIndex string
	}{
		{name: "Bearer without token index", auth: []string{"Bearer access-token"}},
		{name: "Bearer with automatic token index", auth: []string{"Bearer access-token"}, tokenIndex: "0"},
		{name: "unsupported authorization", auth: []string{"Basic credentials"}, tokenIndex: "1"},
		{name: "duplicate authorization", auth: []string{"Bearer first", "Bearer second"}, tokenIndex: "1"},
	} {
		t.Run("rejects "+tt.name, func(t *testing.T) {
			t.Parallel()

			l := newLimiter(t)
			handler := admissionMiddleware(l, nil, time.Second, 1)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("downstream handler should not be called")
			}))

			req := httptest.NewRequest(http.MethodGet, "/europe/riot/account/v1/accounts/me", nil)
			for _, value := range tt.auth {
				req.Header.Add("Authorization", value)
			}
			if tt.tokenIndex != "" {
				req.Header.Set("X-Riot-Token-Index", tt.tokenIndex)
			}
			req = req.WithContext(router.WithPath(req.Context(), router.PathInfo{
				Region:       "europe",
				UpstreamPath: "/riot/account/v1/accounts/me",
				Bucket:       "europe:riot/account/v1/accounts/me",
			}))
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if got, want := rec.Code, http.StatusBadRequest; got != want {
				t.Fatalf("status = %d, want %d", got, want)
			}
		})
	}
}
