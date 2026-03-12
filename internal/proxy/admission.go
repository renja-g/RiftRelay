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
	Priority  string
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

			tokenIndex := -1 // -1 means "any token"
			if val := r.Header.Get("X-Riot-Token-Index"); val != "" {
				if parsed, err := strconv.Atoi(val); err == nil {
					tokenIndex = parsed
				}
			}

			admitCtx := r.Context()
			cancel := func() {}
			if timeout > 0 {
				admitCtx, cancel = context.WithTimeout(admitCtx, timeout)
			}
			defer cancel()

			start := time.Now()
			ticket, err := l.Admit(admitCtx, limiter.Admission{
				Region:     info.Region,
				Bucket:     info.Bucket,
				Priority:   priority,
				TokenIndex: tokenIndex,
			})
			waitDuration := time.Since(start)

			if err != nil {
				if m != nil {
					reason := "rejected"
					if rejected, ok := err.(*limiter.RejectedError); ok {
						if rejected.Reason == "shutting_down" {
							reason = "shutting_down"
						} else {
							reason = "rejected_" + rejected.Reason
						}
					} else if err == context.DeadlineExceeded || err == context.Canceled {
						reason = "rejected_timeout"
					}
					m.ObserveAdmissionResult(reason, info.Region, info.Bucket, priority.String())
					m.ObserveQueueWait(info.Bucket, priority, waitDuration)
				}
				log.Printf("admission_reject region=%s bucket=%s priority=%s err=%v", info.Region, info.Bucket, priority.String(), err)

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
				m.ObserveAdmissionResult("allowed", info.Region, info.Bucket, priority.String())
			}

			ctx := withKeyIndex(r.Context(), ticket.KeyIndex)
			ctx = withAdmission(ctx, admissionContext{
				Region:    info.Region,
				Bucket:    info.Bucket,
				KeyIndex:  ticket.KeyIndex,
				Priority:  priority.String(),
				StartedAt: time.Now(), // Captured after admission so upstream_duration excludes queue wait
			})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
