package limiter

import (
	"math"
	"time"
)

const defaultBudgetID = "default"

type limitWindow struct {
	limit        int
	used         int
	window       time.Duration
	resetAt      time.Time
	locallyReset bool
}

func (w *limitWindow) rollover(now time.Time) {
	w.used = 0
	w.resetAt = now.Add(w.window)
	w.locallyReset = true
}

type rateState struct {
	windows       []limitWindow
	blockedUntil  time.Time
	defaultPacing pacingState
	pacing        map[string]*pacingState
}

type pacingState struct {
	lastGranted time.Time
	windows     map[time.Duration]*pacingWindow
}

type pacingWindow struct {
	used    int
	resetAt time.Time
}

func (s *rateState) nextAllowed(now time.Time, budgetID string, share float64, bypassPacing bool) time.Time {
	next := now
	budgetID = normalizeBudgetID(budgetID)
	var pacing *pacingState
	if !bypassPacing {
		pacing = s.pacingFor(budgetID)
	}

	if s.blockedUntil.After(next) {
		next = s.blockedUntil
	}

	for i := range s.windows {
		w := &s.windows[i]
		if !w.resetAt.After(now) {
			w.rollover(now)
		}
		if w.used >= w.limit && w.resetAt.After(next) {
			next = w.resetAt
			continue
		}
		if bypassPacing {
			continue
		}

		pacingWindow := pacing.windowFor(*w, now)
		requestsLeft := effectiveLimit(w.limit, share) - pacingWindow.used
		if requestsLeft <= 0 {
			if w.resetAt.After(next) {
				next = w.resetAt
			}
			continue
		}

		pacedAt := now
		if !pacing.lastGranted.IsZero() {
			nextSlot := pacing.lastGranted.Add(w.resetAt.Sub(now) / time.Duration(requestsLeft+1))
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

func (s *rateState) consume(now time.Time, budgetID string) bool {
	if s.blockedUntil.After(now) {
		return false
	}
	budgetID = normalizeBudgetID(budgetID)

	for i := range s.windows {
		w := &s.windows[i]
		if !w.resetAt.After(now) {
			w.rollover(now)
		}
		if w.used >= w.limit {
			return false
		}
	}

	pacing := s.pacingFor(budgetID)
	for i := range s.windows {
		w := &s.windows[i]
		w.used++
		pacing.windowFor(*w, now).used++
	}
	pacing.lastGranted = now
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

			if old, ok := existing[next.window]; ok {
				if !old.resetAt.After(now) {
					next.rollover(now)
				} else {
					// After a local rollover, upstream counts belong to a differently aligned window.
					if old.locallyReset || old.used > next.used {
						next.used = old.used
					}
					next.resetAt = old.resetAt
					next.locallyReset = old.locallyReset
				}
			}

			updated = append(updated, next)
		}
		s.windows = updated
		s.prunePacingWindows()
	}
	if seenCount {
		// We do not know exact prior request timestamps, but anchoring at "now" avoids instant bursts.
		if s.defaultPacing.lastGranted.IsZero() {
			s.defaultPacing.lastGranted = now
		}
	}

	if applyRetry && retryAfter != nil && retryAfter.After(s.blockedUntil) {
		s.blockedUntil = *retryAfter
	}
}

func (s *rateState) pacingFor(budgetID string) *pacingState {
	budgetID = normalizeBudgetID(budgetID)
	if budgetID == defaultBudgetID {
		return &s.defaultPacing
	}
	if s.pacing == nil {
		s.pacing = make(map[string]*pacingState)
	}
	pacing := s.pacing[budgetID]
	if pacing == nil {
		pacing = &pacingState{}
		s.pacing[budgetID] = pacing
	}
	return pacing
}

func (p *pacingState) windowFor(w limitWindow, now time.Time) *pacingWindow {
	if p.windows == nil {
		p.windows = make(map[time.Duration]*pacingWindow)
	}
	current := p.windows[w.window]
	if current == nil || !current.resetAt.Equal(w.resetAt) || !current.resetAt.After(now) {
		current = &pacingWindow{resetAt: w.resetAt}
		p.windows[w.window] = current
	}
	return current
}

func (s *rateState) prunePacingWindows() {
	if s.defaultPacing.windows == nil && len(s.pacing) == 0 {
		return
	}

	active := make(map[time.Duration]struct{}, len(s.windows))
	for _, w := range s.windows {
		active[w.window] = struct{}{}
	}
	prunePacingStateWindows(&s.defaultPacing, active)
	for _, pacing := range s.pacing {
		prunePacingStateWindows(pacing, active)
	}
}

func prunePacingStateWindows(pacing *pacingState, active map[time.Duration]struct{}) {
	for window := range pacing.windows {
		if _, ok := active[window]; !ok {
			delete(pacing.windows, window)
		}
	}
}

func effectiveLimit(limit int, share float64) int {
	if share <= 0 || share > 1 || math.IsNaN(share) || math.IsInf(share, 0) {
		share = 1
	}
	effective := int(math.Ceil(float64(limit) * share))
	if effective < 1 {
		return 1
	}
	return effective
}

type keyState struct {
	appByRegion      map[string]*rateState
	methodByBucket   map[string]*rateState
	defaultAppLimits []parsedWindow
}

func newKeyState(defaultAppLimits []parsedWindow) keyState {
	return keyState{
		appByRegion:      make(map[string]*rateState),
		methodByBucket:   make(map[string]*rateState),
		defaultAppLimits: defaultAppLimits,
	}
}

func (k *keyState) app(region string, now time.Time, additionalWindow time.Duration) *rateState {
	state, ok := k.appByRegion[region]
	if ok {
		return state
	}
	state = &rateState{}
	state.apply(k.defaultAppLimits, nil, false, now, additionalWindow)
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
