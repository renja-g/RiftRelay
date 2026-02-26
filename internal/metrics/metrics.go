package metrics

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/renja-g/RiftRelay/internal/limiter"
)

type Collector struct {
	inflight        atomic.Int64
	totalRequests   atomic.Uint64
	admissionWaitNS atomic.Uint64
	admissionCount  atomic.Uint64

	mu               sync.RWMutex
	queueDepth       map[string]int
	admissionResults map[string]uint64
	upstreamStatuses map[int]uint64
}

const queueKeySeparator = "\x1f"

func NewCollector() *Collector {
	return &Collector{
		queueDepth:       make(map[string]int),
		admissionResults: make(map[string]uint64),
		upstreamStatuses: make(map[int]uint64),
	}
}

func (c *Collector) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.inflight.Add(1)
		c.totalRequests.Add(1)
		defer c.inflight.Add(-1)
		next.ServeHTTP(w, r)
	})
}

func (c *Collector) ObserveQueueDepth(bucket string, priority limiter.Priority, depth int) {
	c.mu.Lock()
	c.queueDepth[bucket+queueKeySeparator+priorityLabel(priority)] = depth
	c.mu.Unlock()
}

func (c *Collector) ObserveAdmission(wait time.Duration, outcome string) {
	c.admissionWaitNS.Add(uint64(wait))
	c.admissionCount.Add(1)

	c.mu.Lock()
	c.admissionResults[outcome]++
	c.mu.Unlock()
}

func (c *Collector) ObserveAdmissionWait(wait time.Duration) {
	c.admissionWaitNS.Add(uint64(wait))
	c.admissionCount.Add(1)
}

func (c *Collector) ObserveAdmissionResult(outcome string) {
	c.mu.Lock()
	c.admissionResults[outcome]++
	c.mu.Unlock()
}

func (c *Collector) ObserveUpstream(statusCode int, _ time.Duration) {
	c.mu.Lock()
	c.upstreamStatuses[statusCode]++
	c.mu.Unlock()
}

func (c *Collector) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	total := c.totalRequests.Load()
	inflight := c.inflight.Load()
	admitCount := c.admissionCount.Load()
	admitAvg := float64(0)
	if admitCount > 0 {
		admitAvg = float64(c.admissionWaitNS.Load()) / float64(admitCount) / 1_000_000
	}

	_, _ = fmt.Fprintf(w, "riftrelay_http_requests_total %d\n", total)
	_, _ = fmt.Fprintf(w, "riftrelay_http_inflight %d\n", inflight)
	_, _ = fmt.Fprintf(w, "riftrelay_admission_wait_avg_ms %.3f\n", admitAvg)

	c.mu.RLock()
	defer c.mu.RUnlock()

	admissionKeys := sortedStringKeys(c.admissionResults)
	for _, outcome := range admissionKeys {
		value := c.admissionResults[outcome]
		_, _ = fmt.Fprintf(
			w,
			"riftrelay_admission_total{outcome=%q} %d\n",
			escapeLabel(outcome),
			value,
		)
	}

	queueKeys := sortedStringKeysInt(c.queueDepth)
	for _, key := range queueKeys {
		parts := strings.SplitN(key, queueKeySeparator, 2)
		if len(parts) != 2 {
			continue
		}
		bucket := parts[0]
		priority := parts[1]
		_, _ = fmt.Fprintf(
			w,
			"riftrelay_queue_depth{bucket=%q,priority=%q} %d\n",
			escapeLabel(bucket),
			escapeLabel(priority),
			c.queueDepth[key],
		)
	}

	statusCodes := make([]int, 0, len(c.upstreamStatuses))
	for code := range c.upstreamStatuses {
		statusCodes = append(statusCodes, code)
	}
	sort.Ints(statusCodes)
	for _, code := range statusCodes {
		_, _ = fmt.Fprintf(
			w,
			"riftrelay_upstream_responses_total{code=%q} %d\n",
			strconv.Itoa(code),
			c.upstreamStatuses[code],
		)
	}
}

func priorityLabel(priority limiter.Priority) string {
	if priority == limiter.PriorityHigh {
		return "high"
	}
	return "normal"
}

func sortedStringKeys(m map[string]uint64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringKeysInt(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func escapeLabel(v string) string {
	value := strings.ReplaceAll(v, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}
