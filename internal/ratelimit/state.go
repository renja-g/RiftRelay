package ratelimit

import (
	"net/http"
	"sync"
	"time"
)

type bucketRef struct {
	index int
	id    uint64
}

// State tracks rate limit buckets and spacing for normal traffic.
type State struct {
	mu         sync.Mutex
	buckets    []Bucket
	lastNormal time.Time
}

func NewState(defaultBuckets []Bucket) *State {
	if len(defaultBuckets) == 0 {
		defaultBuckets = []Bucket{
			{Limit: 20, Window: time.Second},
			{Limit: 100, Window: 120 * time.Second},
		}
	}
	return &State{buckets: defaultBuckets}
}

// Reserve computes the earliest allowed time for a request.
// It blocks capacity immediately by inserting placeholders so concurrent
// requests observe the reservation. The caller must call cancel only when
// abandoning the reservation (e.g., context cancellation before send).
func (s *State) Reserve(now time.Time, priority bool) (time.Time, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.prune(now)

	next := now
	maxSpacing := time.Duration(0)
	usedAny := false
	for _, b := range s.buckets {
		if len(b.entries) > 0 {
			usedAny = true
		}
		if b.Limit > 0 {
			remaining, untilReset := b.remaining(now)
			baseSpacing := b.Window / time.Duration(b.Limit)
			spacing := baseSpacing
			if remaining > 0 && untilReset > 0 {
				spacing = untilReset / time.Duration(remaining)
				if spacing < baseSpacing {
					spacing = baseSpacing
				}
			}
			if spacing > maxSpacing {
				maxSpacing = spacing
			}
		}
		if cand := b.nextAvailable(now); cand.After(next) {
			next = cand
		}
	}

	if !priority {
		spacingBase := s.lastNormal
		if spacingBase.IsZero() && usedAny {
			spacingBase = now
		}
		if !spacingBase.IsZero() {
			if spacingAt := spacingBase.Add(maxSpacing); spacingAt.After(next) {
				next = spacingAt
			}
		}
	}

	refs := make([]bucketRef, 0, len(s.buckets))
	for i := range s.buckets {
		id := s.buckets[i].add(next)
		refs = append(refs, bucketRef{index: i, id: id})
	}

	prevLast := s.lastNormal
	if !priority {
		s.lastNormal = next
	}

	canceled := false
	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if canceled {
			return
		}
		for _, ref := range refs {
			s.buckets[ref.index].remove(ref.id)
		}
		if !priority && s.lastNormal.Equal(next) {
			s.lastNormal = prevLast
		}
		canceled = true
	}

	return next, cancel
}

// UpdateFromHeaders refreshes bucket definitions when Riot returns limits.
// Existing reservations remain; future reservations use the new limits.
func (s *State) UpdateFromHeaders(h http.Header) {
	newBuckets := UpdateBucketsFromHeaders(h)
	if len(newBuckets) == 0 {
		return
	}

	now := time.Now()
	methodCounts := parseCountHeader(h.Get("X-Method-Rate-Limit-Count"))
	appCounts := parseCountHeader(h.Get("X-App-Rate-Limit-Count"))

	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range newBuckets {
		win := newBuckets[i].Window
		count, ok := methodCounts[win]
		if !ok {
			count = appCounts[win]
		}
		if count > 0 {
			newBuckets[i].entries = make([]bucketEntry, count)
			for j := 0; j < count; j++ {
				newBuckets[i].entries[j] = bucketEntry{
					at: now,
					id: newBuckets[i].nextID,
				}
				newBuckets[i].nextID++
			}
		}
	}

	s.buckets = newBuckets
	s.prune(now)
}

func (s *State) prune(now time.Time) {
	for i := range s.buckets {
		s.buckets[i].prune(now)
	}
}
