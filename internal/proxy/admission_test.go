package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/renja-g/RiftRelay/internal/limiter"
	"github.com/renja-g/RiftRelay/internal/router"
)

func TestAdmissionMiddleware(t *testing.T) {
	t.Parallel()

	newLimiter := func(t *testing.T) *limiter.Limiter {
		t.Helper()

		l, err := limiter.New(limiter.Config{
			KeyCount:         1,
			QueueCapacity:    2,
			DefaultAppLimits: "20:1",
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

		handler := admissionMiddleware(l, nil, time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got, ok := keyIndexFromContext(r.Context()); !ok || got != 0 {
				t.Fatalf("keyIndexFromContext() = (%d, %v), want (0, true)", got, ok)
			}
			info, ok := admissionFromContext(r.Context())
			if !ok {
				t.Fatal("admissionFromContext() ok = false, want true")
			}
			if info.Region != pathInfo.Region || info.Bucket != pathInfo.Bucket || info.Priority != "high" {
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
		handler := admissionMiddleware(l, nil, time.Second)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
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
		handler := admissionMiddleware(l, nil, time.Second)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
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
}
