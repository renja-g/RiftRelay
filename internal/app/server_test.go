package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/renja-g/RiftRelay/internal/proxy"
	"github.com/renja-g/RiftRelay/internal/testutil"
)

func TestServerHandlerOfflineEndpoints(t *testing.T) {
	t.Parallel()

	cfg := testutil.DummyConfig()
	server, err := New(
		cfg,
		WithSwaggerHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("swagger-stub"))
		})),
		WithProxyOptions(proxy.WithBaseTransport(testutil.RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			resp := testutil.HTTPResponse(http.StatusNoContent, "", nil)
			resp.Request = r
			return resp, nil
		}))),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		_ = server.Shutdown(t.Context())
	})

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "healthz", path: "/healthz", wantStatus: http.StatusNoContent},
		{name: "metrics", path: "/metrics", wantStatus: http.StatusOK, wantBody: "go_goroutines"},
		{name: "swagger", path: "/swagger/", wantStatus: http.StatusCreated, wantBody: "swagger-stub"},
		{name: "invalid proxy path", path: "/invalid", wantStatus: http.StatusBadRequest},
		{name: "valid proxy path", path: "/europe/riot/account/v1/accounts/me", wantStatus: http.StatusNoContent},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			server.Handler().ServeHTTP(rec, req)

			if got, want := rec.Code, tt.wantStatus; got != want {
				t.Fatalf("status = %d, want %d", got, want)
			}
			if tt.wantBody != "" && !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Fatalf("body = %q, want substring %q", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestServerShutdownWithoutStart(t *testing.T) {
	t.Parallel()

	cfg := testutil.DummyConfig()
	server, err := New(cfg, WithSwaggerHandler(http.NotFoundHandler()))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := server.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}
