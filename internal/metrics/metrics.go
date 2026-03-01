package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/renja-g/RiftRelay/internal/limiter"
)

// Collector holds all Prometheus metrics for RiftRelay.
type Collector struct {
	registry *prometheus.Registry

	// Existing metrics (preserved from original implementation)
	totalRequests  *prometheus.CounterVec
	inflight       *prometheus.GaugeVec
	admissionTotal *prometheus.CounterVec
	queueDepth     *prometheus.GaugeVec
	upstreamTotal  *prometheus.CounterVec

	// New histogram metrics
	requestDuration  *prometheus.HistogramVec
	queueWaitSeconds *prometheus.HistogramVec
	upstreamDuration *prometheus.HistogramVec

	handler http.Handler
}

// responseRecorder wraps http.ResponseWriter to capture status code.
type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (rr *responseRecorder) WriteHeader(code int) {
	rr.statusCode = code
	rr.ResponseWriter.WriteHeader(code)
}

// NewCollector creates a new metrics collector with all Prometheus metrics registered.
func NewCollector() *Collector {
	registry := prometheus.NewRegistry()

	// Register Go runtime metrics
	registry.MustRegister(prometheus.NewGoCollector())
	registry.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))

	c := &Collector{
		registry: registry,
		totalRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "riftrelay_http_requests_total",
			Help: "Total number of HTTP requests received",
		}, []string{"priority"}),
		inflight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "riftrelay_http_inflight",
			Help: "Number of requests currently being processed",
		}, []string{"priority"}),
		admissionTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "riftrelay_admission_total",
			Help: "Total number of admission control decisions",
		}, []string{"outcome", "priority"}),
		queueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "riftrelay_queue_depth",
			Help: "Current queue depth per bucket and priority",
		}, []string{"bucket", "priority"}),
		upstreamTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "riftrelay_upstream_responses_total",
			Help: "Total number of upstream responses by status code",
		}, []string{"code", "priority"}),
		// New histogram metrics with buckets optimized for proxy latencies
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "riftrelay_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 300},
		}, []string{"region", "priority", "status_code"}),
		queueWaitSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "riftrelay_queue_wait_seconds",
			Help:    "Time spent waiting in admission queue",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 300},
		}, []string{"bucket", "priority"}),
		upstreamDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "riftrelay_upstream_duration_seconds",
			Help:    "Upstream request duration in seconds",
			Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 300},
		}, []string{"region", "bucket"}),
	}

	// Register all metrics
	registry.MustRegister(
		c.totalRequests,
		c.inflight,
		c.admissionTotal,
		c.queueDepth,
		c.upstreamTotal,
		c.requestDuration,
		c.queueWaitSeconds,
		c.upstreamDuration,
	)

	c.handler = promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		Registry: registry,
	})

	return c
}

// Middleware returns an HTTP middleware that tracks request metrics.
// It records total requests, inflight requests, and request duration with labels.
func (c *Collector) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture priority from header for metrics labeling
		priority := "normal"
		if r.Header.Get("X-Priority") == "high" {
			priority = "high"
		}

		c.totalRequests.WithLabelValues(priority).Inc()
		c.inflight.WithLabelValues(priority).Inc()

		// Extract region from URL path if available
		region := extractRegion(r.URL.Path)

		recorder := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}

		start := time.Now()
		next.ServeHTTP(recorder, r)
		duration := time.Since(start)

		c.inflight.WithLabelValues(priority).Dec()
		c.requestDuration.WithLabelValues(region, priority, statusCodeStr(recorder.statusCode)).Observe(duration.Seconds())
	})
}

// ObserveQueueDepth records the current queue depth for a bucket and priority.
func (c *Collector) ObserveQueueDepth(bucket string, priority limiter.Priority, depth int) {
	c.queueDepth.WithLabelValues(bucket, priorityLabel(priority)).Set(float64(depth))
}

// ObserveQueueWait records the time spent waiting for admission with bucket and priority labels.
func (c *Collector) ObserveQueueWait(bucket string, priority limiter.Priority, wait time.Duration) {
	c.queueWaitSeconds.WithLabelValues(bucket, priorityLabel(priority)).Observe(wait.Seconds())
}

// ObserveAdmissionResult records the outcome of an admission decision.
func (c *Collector) ObserveAdmissionResult(outcome, priority string) {
	c.admissionTotal.WithLabelValues(outcome, priority).Inc()
}

// ObserveUpstream records upstream response metrics.
func (c *Collector) ObserveUpstream(statusCode int, priority string) {
	c.upstreamTotal.WithLabelValues(statusCodeStr(statusCode), priority).Inc()
}

// ObserveUpstreamDuration records upstream request duration with region and bucket labels.
func (c *Collector) ObserveUpstreamDuration(region, bucket string, duration time.Duration) {
	c.upstreamDuration.WithLabelValues(region, bucket).Observe(duration.Seconds())
}

// ServeHTTP implements http.Handler to expose metrics in Prometheus format.
func (c *Collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.handler.ServeHTTP(w, r)
}

// priorityLabel converts a Priority to its string representation.
func priorityLabel(p limiter.Priority) string {
	if p == limiter.PriorityHigh {
		return "high"
	}
	return "normal"
}

// splitPath extracts the region (first path segment) allocation-free.
func splitPath(path string) []string {
	// Let's implement this allocation-free by returning a slice of strings statically? No.
	// Actually we should just extract the region directly instead of returning a slice of strings.
	// Wait, since splitPath is currently used to get the parts[0], we can just make it return the region directly.
	return nil
}

// extractRegion extracts the first path segment (region) without allocations.
func extractRegion(path string) string {
	if len(path) == 0 || path[0] != '/' {
		return "unknown"
	}
	start := 1
	for i := 1; i < len(path); i++ {
		if path[i] == '/' {
			if i > start {
				return path[start:i]
			}
			return "unknown"
		}
	}
	if len(path) > start {
		return path[start:]
	}
	return "unknown"
}

// statusCodeStr returns a pre-allocated string for common HTTP status codes.
func statusCodeStr(code int) string {
	switch code {
	case 200:
		return "200"
	case 201:
		return "201"
	case 204:
		return "204"
	case 400:
		return "400"
	case 401:
		return "401"
	case 403:
		return "403"
	case 404:
		return "404"
	case 408:
		return "408"
	case 429:
		return "429"
	case 500:
		return "500"
	case 502:
		return "502"
	case 503:
		return "503"
	case 504:
		return "504"
	default:
		return strconv.Itoa(code)
	}
}
