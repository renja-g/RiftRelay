package limiter

import "time"

type limitWindow struct {
	limit   int
	used    int
	window  time.Duration
	resetAt time.Time
}

type rateState struct {
	windows      []limitWindow
	blockedUntil time.Time
	lastGranted  time.Time
}

func (s *rateState) nextAllowed(now time.Time, bypassPacing bool) time.Time {
	next := now

	if s.blockedUntil.After(next) {
		next = s.blockedUntil
	}

	for i := range s.windows {
		w := &s.windows[i]
		if !w.resetAt.After(now) {
			w.used = 0
			w.resetAt = now.Add(w.window)
		}
		if w.used >= w.limit && w.resetAt.After(next) {
			next = w.resetAt
			continue
		}
		if bypassPacing {
			continue
		}

		requestsLeft := w.limit - w.used
		if requestsLeft <= 0 {
			continue
		}

		timeLeft := w.resetAt.Sub(now)
		if timeLeft <= 0 {
			continue
		}

		interval := timeLeft / time.Duration(requestsLeft)
		if interval <= 0 {
			continue
		}

		pacedAt := now
		if !s.lastGranted.IsZero() {
			nextSlot := s.lastGranted.Add(interval)
			if nextSlot.After(pacedAt) {
				pacedAt = nextSlot
			}
		}
		if pacedAt.After(next) {
			next = pacedAt
		}
	}

	return next
}

func (s *rateState) consume(now time.Time) bool {
	if s.blockedUntil.After(now) {
		return false
	}

	for i := range s.windows {
		w := &s.windows[i]
		if !w.resetAt.After(now) {
			w.used = 0
			w.resetAt = now.Add(w.window)
		}
		if w.used >= w.limit {
			return false
		}
	}

	for i := range s.windows {
		s.windows[i].used++
	}
	s.lastGranted = now
	return true
}

func (s *rateState) apply(
	windows []parsedWindow,
	retryAfter *time.Time,
	applyRetry bool,
	now time.Time,
	additionalWindow time.Duration,
) {
	seenCount := false
	if len(windows) > 0 {
		existing := make(map[time.Duration]limitWindow, len(s.windows))
		for _, w := range s.windows {
			existing[w.window] = w
		}

		updated := make([]limitWindow, 0, len(windows))
		for _, parsed := range windows {
			if parsed.limit <= 0 || parsed.window <= 0 {
				continue
			}

			next := limitWindow{
				limit:   parsed.limit,
				used:    parsed.count,
				window:  parsed.window + additionalWindow,
				resetAt: now.Add(parsed.window + additionalWindow),
			}

			if next.used > next.limit {
				next.used = next.limit
			}
			if next.used > 0 {
				seenCount = true
			}

			if old, ok := existing[next.window]; ok && old.resetAt.After(now) {
				if old.used > next.used {
					next.used = old.used
				}
				next.resetAt = old.resetAt
			}

			updated = append(updated, next)
		}
		s.windows = updated
	}
	if s.lastGranted.IsZero() && seenCount {
		// We do not know exact prior request timestamps, but anchoring at "now" avoids instant bursts.
		s.lastGranted = now
	}

	if applyRetry && retryAfter != nil && retryAfter.After(s.blockedUntil) {
		s.blockedUntil = *retryAfter
	}
}

type keyState struct {
	appByRegion    map[string]*rateState
	methodByBucket map[string]*rateState
}

func newKeyState() keyState {
	return keyState{
		appByRegion:    make(map[string]*rateState),
		methodByBucket: make(map[string]*rateState),
	}
}

func (k *keyState) app(region string) *rateState {
	state, ok := k.appByRegion[region]
	if ok {
		return state
	}
	state = &rateState{}
	k.appByRegion[region] = state
	return state
}

func (k *keyState) method(bucket string) *rateState {
	state, ok := k.methodByBucket[bucket]
	if ok {
		return state
	}
	state = &rateState{}
	k.methodByBucket[bucket] = state
	return state
}
