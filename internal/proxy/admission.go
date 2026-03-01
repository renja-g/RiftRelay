package proxy

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/renja-g/RiftRelay/internal/limiter"
	"github.com/renja-g/RiftRelay/internal/metrics"
	"github.com/renja-g/RiftRelay/internal/router"
)

type admissionContext struct {
	Region    string
	Bucket    string
	KeyIndex  int
	Priority  limiter.Priority
	StartedAt time.Time
}

type admissionContextKey struct{}

func withAdmission(ctx context.Context, info admissionContext) context.Context {
	return context.WithValue(ctx, admissionContextKey{}, info)
}

func admissionFromContext(ctx context.Context) (admissionContext, bool) {
	info, ok := ctx.Value(admissionContextKey{}).(admissionContext)
	return info, ok
}

func admissionMiddleware(
	l *limiter.Limiter,
	m *metrics.Collector,
	timeout time.Duration,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			info, ok := router.PathFromContext(r.Context())
			if !ok {
				http.Error(w, "invalid route context", http.StatusBadRequest)
				return
			}

			priority := limiter.PriorityNormal
			if strings.EqualFold(r.Header.Get("X-Priority"), "high") {
				priority = limiter.PriorityHigh
			}

			admitCtx := r.Context()
			cancel := func() {}
			if timeout > 0 {
				admitCtx, cancel = context.WithTimeout(admitCtx, timeout)
			}
			defer cancel()

			start := time.Now()
			ticket, err := l.Admit(admitCtx, limiter.Admission{
				Region:   info.Region,
				Bucket:   info.Bucket,
				Priority: priority,
			})
			waitDuration := time.Since(start)

			if err != nil {
				if m != nil {
					m.ObserveAdmissionResult("rejected")
					m.ObserveQueueWait(info.Bucket, priority, waitDuration)
				}
				log.Printf("admission_reject region=%s bucket=%s priority=%s err=%v", info.Region, info.Bucket, priorityString(priority), err)

				retryAfter := time.Second
				if rejected, ok := err.(*limiter.RejectedError); ok && rejected.RetryAfter > 0 {
					retryAfter = rejected.RetryAfter
				}

				w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Round(time.Second).Seconds())))
				http.Error(w, "request rejected by admission control", http.StatusTooManyRequests)
				return
			}

			if m != nil {
				m.ObserveQueueWait(info.Bucket, priority, waitDuration)
				m.ObserveAdmissionResult("allowed")
			}

			ctx := withKeyIndex(r.Context(), ticket.KeyIndex)
			ctx = withAdmission(ctx, admissionContext{
				Region:    info.Region,
				Bucket:    info.Bucket,
				KeyIndex:  ticket.KeyIndex,
				Priority:  priority,
				StartedAt: start,
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func priorityString(priority limiter.Priority) string {
	if priority == limiter.PriorityHigh {
		return "high"
	}
	return "normal"
}
