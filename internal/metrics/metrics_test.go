package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/renja-g/RiftRelay/internal/limiter"
)

func TestMetricsOutput(t *testing.T) {
	c := NewCollector()

	// Simulate some metrics
	c.ObserveQueueDepth("europe:test:bucket", limiter.PriorityHigh, 5)
	c.ObserveQueueDepth("europe:test:bucket", limiter.PriorityNormal, 3)
	c.ObserveAdmission(time.Millisecond*50, "allowed")
	c.ObserveAdmissionResult("rejected_queue_full")
	c.ObserveUpstream(200, time.Millisecond*100)
	c.ObserveUpstreamDuration("europe", "test:bucket", time.Millisecond*100)
	c.ObserveQueueWait("europe:test:bucket", limiter.PriorityHigh, time.Millisecond*25)

	// Test the HTTP handler
	req := httptest.NewRequest("GET", "/metrics", nil)
	rr := httptest.NewRecorder()
	c.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	body := rr.Body.String()

	// Check for expected metrics
	expectedMetrics := []string{
		"riftrelay_http_requests_total",
		"riftrelay_http_inflight",
		"riftrelay_admission_total",
		"riftrelay_queue_depth",
		"riftrelay_upstream_responses_total",
		"riftrelay_queue_wait_seconds",    // Histogram with observations
		"riftrelay_upstream_duration_seconds", // Histogram with observations
		"go_goroutines",  // Go runtime metrics
		"process_resident_memory_bytes", // Process metrics
	}

	for _, metric := range expectedMetrics {
		if !strings.Contains(body, metric) {
			t.Errorf("expected metric %q not found in output", metric)
		}
	}

	// Check histogram bucket labels are present
	if !strings.Contains(body, `riftrelay_queue_wait_seconds_bucket{bucket="europe:test:bucket",priority="high"`) {
		t.Error("expected queue_wait histogram bucket with labels not found")
	}

	// Check upstream_duration has bucket and region labels (order may vary)
	if !strings.Contains(body, `bucket="test:bucket"`) || !strings.Contains(body, `region="europe"`) {
		t.Error("expected upstream_duration histogram to have bucket and region labels")
	}
}

func TestMiddlewareRecordsMetrics(t *testing.T) {
	c := NewCollector()

	handler := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	}))

	req := httptest.NewRequest("GET", "/europe/riot/account/v1/accounts", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	// Now check metrics output includes request_duration histogram
	metricsReq := httptest.NewRequest("GET", "/metrics", nil)
	metricsRr := httptest.NewRecorder()
	c.ServeHTTP(metricsRr, metricsReq)

	body := metricsRr.Body.String()

	// After middleware processes a request, request_duration histogram should appear
	if !strings.Contains(body, "riftrelay_request_duration_seconds") {
		t.Error("expected riftrelay_request_duration_seconds histogram after middleware request")
	}

	// Check request was counted
	if !strings.Contains(body, "riftrelay_http_requests_total 1") {
		t.Error("expected riftrelay_http_requests_total to be 1")
	}
}

func TestMiddlewareWithPriorityHeader(t *testing.T) {
	c := NewCollector()

	handler := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot) // 418
		w.Write([]byte("I'm a teapot"))
	}))

	req := httptest.NewRequest("GET", "/europe/riot/account/v1/accounts", nil)
	req.Header.Set("X-Priority", "high")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusTeapot {
		t.Fatalf("expected status 418, got %d", rr.Code)
	}

	// Check metrics
	metricsReq := httptest.NewRequest("GET", "/metrics", nil)
	metricsRr := httptest.NewRecorder()
	c.ServeHTTP(metricsRr, metricsReq)

	body := metricsRr.Body.String()

	// Should have priority="high" and status_code="418" labels
	if !strings.Contains(body, `priority="high"`) {
		t.Error("expected priority=high label in metrics")
	}
	if !strings.Contains(body, `status_code="418"`) {
		t.Error("expected status_code=418 label in metrics")
	}
}
