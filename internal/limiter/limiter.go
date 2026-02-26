package limiter

import (
	"container/heap"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const idleTimerWindow = 24 * time.Hour

type Limiter struct {
	cfg       Config
	admitCh   chan *admitRequest
	observeCh chan Observation
	closeCh   chan chan struct{}
}

func New(cfg Config) (*Limiter, error) {
	if cfg.KeyCount <= 0 {
		return nil, fmt.Errorf("KeyCount must be > 0")
	}
	if cfg.QueueCapacity <= 0 {
		return nil, fmt.Errorf("QueueCapacity must be > 0")
	}
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	if cfg.Metrics == nil {
		cfg.Metrics = noopMetrics{}
	}

	l := &Limiter{
		cfg:       cfg,
		admitCh:   make(chan *admitRequest),
		observeCh: make(chan Observation, 256),
		closeCh:   make(chan chan struct{}),
	}
	go l.loop()

	return l, nil
}

func (l *Limiter) Admit(ctx context.Context, admission Admission) (Ticket, error) {
	if admission.Region == "" || admission.Bucket == "" {
		return Ticket{}, &RejectedError{Reason: "invalid_route"}
	}

	req := &admitRequest{
		ctx:       ctx,
		admission: admission,
		received:  l.cfg.Clock.Now(),
		resp:      make(chan admitResponse, 1),
	}

	select {
	case l.admitCh <- req:
	case <-ctx.Done():
		return Ticket{}, ctx.Err()
	}

	select {
	case out := <-req.resp:
		return out.ticket, out.err
	case <-ctx.Done():
		return Ticket{}, ctx.Err()
	}
}

func (l *Limiter) Observe(observation Observation) {
	select {
	case l.observeCh <- observation:
	default:
		// Drop burst observations when downstream is saturated. Admission logic still converges with future updates.
	}
}

func (l *Limiter) Close() error {
	done := make(chan struct{})
	l.closeCh <- done
	<-done
	return nil
}

func (l *Limiter) loop() {
	keys := make([]keyState, l.cfg.KeyCount)
	for i := range keys {
		keys[i] = newKeyState()
	}

	buckets := make(map[string]*bucketQueue)
	wakeups := make(wakeHeap, 0)
	heap.Init(&wakeups)

	timer := time.NewTimer(idleTimerWindow)
	defer timer.Stop()

	for {
		nextWake := idleTimerWindow
		if len(wakeups) > 0 {
			now := l.cfg.Clock.Now()
			nextWake = wakeups[0].wakeAt.Sub(now)
			if nextWake < 0 {
				nextWake = 0
			}
		}
		resetTimer(timer, nextWake)

		select {
		case req := <-l.admitCh:
			l.handleAdmit(req, keys, buckets, &wakeups)
		case obs := <-l.observeCh:
			l.handleObservation(obs, keys, buckets, &wakeups)
		case <-timer.C:
			now := l.cfg.Clock.Now()
			for len(wakeups) > 0 {
				next := wakeups[0]
				if next.wakeAt.After(now) {
					break
				}
				heap.Pop(&wakeups)
				l.dispatch(next, keys, &wakeups)
			}
		case done := <-l.closeCh:
			for _, bucket := range buckets {
				for req := bucket.dequeueValid(); req != nil; req = bucket.dequeueValid() {
					select {
					case req.resp <- admitResponse{err: &RejectedError{Reason: "shutting_down"}}:
					default:
					}
				}
			}
			close(done)
			return
		}
	}
}

func (l *Limiter) handleAdmit(
	req *admitRequest,
	keys []keyState,
	buckets map[string]*bucketQueue,
	wakeups *wakeHeap,
) {
	if req == nil {
		return
	}
	if req.ctx.Err() != nil {
		req.resp <- admitResponse{err: req.ctx.Err()}
		return
	}

	bucket := buckets[req.admission.Bucket]
	if bucket == nil {
		bucket = &bucketQueue{
			region:    req.admission.Region,
			bucket:    req.admission.Bucket,
			heapIndex: -1,
		}
		buckets[req.admission.Bucket] = bucket
	}

	if bucket.depth() >= l.cfg.QueueCapacity {
		now := l.cfg.Clock.Now()
		_, earliest := l.pickKey(now, keys, bucket.region, bucket.bucket, req.admission.Priority)
		req.resp <- admitResponse{
			err: &RejectedError{
				Reason:     "queue_full",
				RetryAfter: maxDuration(earliest.Sub(now), time.Second),
			},
		}
		l.cfg.Metrics.ObserveAdmission(0, "rejected_queue_full")
		return
	}

	bucket.enqueue(req)
	l.cfg.Metrics.ObserveQueueDepth(bucket.bucket, req.admission.Priority, bucket.depth())
	l.dispatch(bucket, keys, wakeups)
}

func (l *Limiter) handleObservation(
	obs Observation,
	keys []keyState,
	buckets map[string]*bucketQueue,
	wakeups *wakeHeap,
) {
	if obs.KeyIndex < 0 || obs.KeyIndex >= len(keys) {
		return
	}
	if obs.Region == "" || obs.Bucket == "" {
		return
	}

	now := l.cfg.Clock.Now()
	retryAfter := parseRetryAfter(obs.Header.Get("Retry-After"), now)

	limitType := strings.ToLower(strings.TrimSpace(obs.Header.Get("X-Rate-Limit-Type")))
	applyMethodRetry := obs.StatusCode == http.StatusTooManyRequests && limitType == "method"
	applyAppRetry := obs.StatusCode == http.StatusTooManyRequests && !applyMethodRetry

	key := &keys[obs.KeyIndex]

	appLimits := parseRateHeader(obs.Header.Get("X-App-Rate-Limit"), obs.Header.Get("X-App-Rate-Limit-Count"))
	methodLimits := parseRateHeader(obs.Header.Get("X-Method-Rate-Limit"), obs.Header.Get("X-Method-Rate-Limit-Count"))

	key.app(obs.Region).apply(appLimits, retryAfter, applyAppRetry, now, l.cfg.AdditionalWindow)
	key.method(obs.Bucket).apply(methodLimits, retryAfter, applyMethodRetry, now, l.cfg.AdditionalWindow)

	// An app-limit update can unblock or block multiple buckets in the same region.
	for _, bucket := range buckets {
		if bucket.region == obs.Region {
			l.dispatch(bucket, keys, wakeups)
		}
	}
}

func (l *Limiter) dispatch(bucket *bucketQueue, keys []keyState, wakeups *wakeHeap) {
	if bucket == nil {
		return
	}

	for {
		req := bucket.dequeueValid()
		if req == nil {
			removeWake(wakeups, bucket)
			return
		}

		now := l.cfg.Clock.Now()
		keyIndex, earliest := l.pickKey(now, keys, bucket.region, bucket.bucket, req.admission.Priority)
		if keyIndex < 0 {
			req.resp <- admitResponse{err: &RejectedError{Reason: "no_available_key", RetryAfter: time.Second}}
			l.cfg.Metrics.ObserveAdmission(0, "rejected_no_key")
			continue
		}

		if earliest.After(now) {
			// Put request back at head of corresponding queue.
			if req.admission.Priority == PriorityHigh {
				bucket.high = append([]*admitRequest{req}, bucket.high...)
			} else {
				bucket.normal = append([]*admitRequest{req}, bucket.normal...)
			}
			upsertWake(wakeups, bucket, earliest)
			return
		}

		key := &keys[keyIndex]
		if !key.app(bucket.region).consume(now) || !key.method(bucket.bucket).consume(now) {
			upsertWake(wakeups, bucket, now.Add(5*time.Millisecond))
			// Put request back and retry at next wake-up.
			if req.admission.Priority == PriorityHigh {
				bucket.high = append([]*admitRequest{req}, bucket.high...)
			} else {
				bucket.normal = append([]*admitRequest{req}, bucket.normal...)
			}
			return
		}

		req.resp <- admitResponse{ticket: Ticket{KeyIndex: keyIndex}}
		l.cfg.Metrics.ObserveAdmission(now.Sub(req.received), "allowed")
		l.cfg.Metrics.ObserveQueueDepth(bucket.bucket, req.admission.Priority, bucket.depth())
	}
}

func (l *Limiter) pickKey(now time.Time, keys []keyState, region, bucket string, priority Priority) (int, time.Time) {
	bestIndex := -1
	bestAt := time.Time{}
	bypassPacing := priority == PriorityHigh

	for i := range keys {
		key := &keys[i]
		appAt := key.app(region).nextAllowed(now, bypassPacing)
		methodAt := key.method(bucket).nextAllowed(now, bypassPacing)
		readyAt := appAt
		if methodAt.After(readyAt) {
			readyAt = methodAt
		}

		if bestIndex < 0 || readyAt.Before(bestAt) {
			bestIndex = i
			bestAt = readyAt
		}
	}

	if bestIndex < 0 {
		return -1, now.Add(time.Second)
	}
	return bestIndex, bestAt
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	if duration < 0 {
		duration = 0
	}
	timer.Reset(duration)
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
