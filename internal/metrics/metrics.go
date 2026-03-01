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
	totalRequests   prometheus.Counter
	inflight        prometheus.Gauge
	admissionTotal  *prometheus.CounterVec
	queueDepth      *prometheus.GaugeVec
	upstreamTotal   *prometheus.CounterVec

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
		totalRequests: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "riftrelay_http_requests_total",
			Help: "Total number of HTTP requests received",
		}),
		inflight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "riftrelay_http_inflight",
			Help: "Number of requests currently being processed",
		}),
		admissionTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "riftrelay_admission_total",
			Help: "Total number of admission control decisions",
		}, []string{"outcome"}),
		queueDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "riftrelay_queue_depth",
			Help: "Current queue depth per bucket and priority",
		}, []string{"bucket", "priority"}),
		upstreamTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "riftrelay_upstream_responses_total",
			Help: "Total number of upstream responses by status code",
		}, []string{"code"}),
		// New histogram metrics with buckets optimized for proxy latencies
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "riftrelay_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets, // [.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10]
		}, []string{"region", "priority", "status_code"}),
		queueWaitSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "riftrelay_queue_wait_seconds",
			Help:    "Time spent waiting in admission queue",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		}, []string{"bucket", "priority"}),
		upstreamDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "riftrelay_upstream_duration_seconds",
			Help:    "Upstream request duration in seconds",
			Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30},
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
		c.totalRequests.Inc()
		c.inflight.Inc()

		// Capture priority from header for metrics labeling
		priority := "normal"
		if r.Header.Get("X-Priority") == "high" {
			priority = "high"
		}

		// Extract region from URL path if available
		region := "unknown"
		if len(r.URL.Path) > 1 {
			// Path is like "/europe/riot/account/v1/accounts"
			parts := splitPath(r.URL.Path)
			if len(parts) > 0 {
				region = parts[0]
			}
		}

		recorder := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}

		start := time.Now()
		next.ServeHTTP(recorder, r)
		duration := time.Since(start)

		c.inflight.Dec()
		c.requestDuration.WithLabelValues(region, priority, strconv.Itoa(recorder.statusCode)).Observe(duration.Seconds())
	})
}

// ObserveQueueDepth records the current queue depth for a bucket and priority.
func (c *Collector) ObserveQueueDepth(bucket string, priority limiter.Priority, depth int) {
	c.queueDepth.WithLabelValues(bucket, priorityLabel(priority)).Set(float64(depth))
}

// ObserveAdmission records both the wait time and outcome of admission.
// This method is kept for backward compatibility with the limiter.MetricsSink interface.
func (c *Collector) ObserveAdmission(wait time.Duration, outcome string) {
	c.ObserveAdmissionResult(outcome)
	// Note: We don't have bucket/priority context here, so we use "unknown" labels
	c.queueWaitSeconds.WithLabelValues("unknown", "unknown").Observe(wait.Seconds())
}

// ObserveQueueWait records the time spent waiting for admission with bucket and priority labels.
func (c *Collector) ObserveQueueWait(bucket string, priority limiter.Priority, wait time.Duration) {
	c.queueWaitSeconds.WithLabelValues(bucket, priorityLabel(priority)).Observe(wait.Seconds())
}

// ObserveAdmissionWait records the time spent waiting for admission (legacy method).
// Deprecated: Use ObserveQueueWait for richer metrics.
func (c *Collector) ObserveAdmissionWait(wait time.Duration) {
	c.queueWaitSeconds.WithLabelValues("unknown", "unknown").Observe(wait.Seconds())
}

// ObserveAdmissionResult records the outcome of an admission decision.
func (c *Collector) ObserveAdmissionResult(outcome string) {
	c.admissionTotal.WithLabelValues(outcome).Inc()
}

// ObserveUpstream records upstream response metrics.
func (c *Collector) ObserveUpstream(statusCode int, duration time.Duration) {
	c.upstreamTotal.WithLabelValues(strconv.Itoa(statusCode)).Inc()
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

// splitPath splits a URL path into components.
func splitPath(path string) []string {
	if len(path) == 0 || path[0] != '/' {
		return nil
	}
	// Simple path splitting
	var parts []string
	start := 1
	for i := 1; i <= len(path); i++ {
		if i == len(path) || path[i] == '/' {
			if i > start {
				parts = append(parts, path[start:i])
			}
			start = i + 1
		}
	}
	return parts
}
