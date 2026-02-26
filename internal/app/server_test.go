package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/renja-g/RiftRelay/internal/config"
)

func TestServerFeatureFlagRoutes(t *testing.T) {
	tests := []struct {
		name                  string
		metricsEnabled        bool
		pprofEnabled          bool
		expectMetricsEndpoint bool
		expectPprofEndpoint   bool
	}{
		{
			name:                  "all optional endpoints disabled",
			metricsEnabled:        false,
			pprofEnabled:          false,
			expectMetricsEndpoint: false,
			expectPprofEndpoint:   false,
		},
		{
			name:                  "metrics endpoint enabled only",
			metricsEnabled:        true,
			pprofEnabled:          false,
			expectMetricsEndpoint: true,
			expectPprofEndpoint:   false,
		},
		{
			name:                  "pprof endpoint enabled only",
			metricsEnabled:        false,
			pprofEnabled:          true,
			expectMetricsEndpoint: false,
			expectPprofEndpoint:   true,
		},
		{
			name:                  "all optional endpoints enabled",
			metricsEnabled:        true,
			pprofEnabled:          true,
			expectMetricsEndpoint: true,
			expectPprofEndpoint:   true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			srv, err := New(config.Config{
				Tokens:         []string{"test-token"},
				QueueCapacity:  32,
				MetricsEnabled: tt.metricsEnabled,
				PprofEnabled:   tt.pprofEnabled,
			})
			if err != nil {
				t.Fatalf("new server: %v", err)
			}
			t.Cleanup(func() {
				_ = srv.limiter.Close()
			})

			handler := srv.server.Handler

			healthResp := httptest.NewRecorder()
			healthReq := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			handler.ServeHTTP(healthResp, healthReq)
			if healthResp.Code != http.StatusNoContent {
				t.Fatalf("expected /healthz status %d, got %d", http.StatusNoContent, healthResp.Code)
			}

			metricsResp := httptest.NewRecorder()
			metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			handler.ServeHTTP(metricsResp, metricsReq)

			if tt.expectMetricsEndpoint {
				if metricsResp.Code != http.StatusOK {
					t.Fatalf("expected /metrics status %d, got %d", http.StatusOK, metricsResp.Code)
				}
				if !strings.Contains(metricsResp.Body.String(), "riftrelay_http_requests_total") {
					t.Fatalf("expected /metrics response body to expose metrics")
				}
			} else {
				if metricsResp.Code == http.StatusOK {
					t.Fatalf("expected /metrics to be disabled, got status %d", metricsResp.Code)
				}
				if strings.Contains(metricsResp.Body.String(), "riftrelay_http_requests_total") {
					t.Fatalf("expected disabled /metrics response to not expose metrics payload")
				}
			}

			pprofResp := httptest.NewRecorder()
			pprofReq := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
			handler.ServeHTTP(pprofResp, pprofReq)

			if tt.expectPprofEndpoint {
				if pprofResp.Code != http.StatusOK {
					t.Fatalf("expected /debug/pprof/ status %d, got %d", http.StatusOK, pprofResp.Code)
				}
			} else if pprofResp.Code == http.StatusOK {
				t.Fatalf("expected /debug/pprof/ to be disabled, got status %d", pprofResp.Code)
			}
		})
	}
}
