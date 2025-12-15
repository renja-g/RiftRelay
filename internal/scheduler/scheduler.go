package scheduler

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/renja-g/rp/internal/ratelimit"
)

type requestPermit struct {
	ctx      context.Context
	priority bool
	res      chan error
}

type scheduled struct {
	req    *requestPermit
	when   time.Time
	cancel func()
}

type perKeyScheduler struct {
	state    *ratelimit.State
	incoming chan *requestPermit
	clock    func() time.Time
}

func newPerKeyScheduler(state *ratelimit.State) *perKeyScheduler {
	s := &perKeyScheduler{
		state:    state,
		incoming: make(chan *requestPermit, 256),
		clock:    time.Now,
	}
	go s.run()
	return s
}

func (s *perKeyScheduler) run() {
	var current *scheduled
	var timer *time.Timer
	var timerC <-chan time.Time
	priorityQ := []*requestPermit{}
	normalQ := []*requestPermit{}

	for {
		if current == nil {
			if len(priorityQ) > 0 {
				current = &scheduled{req: priorityQ[0]}
				priorityQ = priorityQ[1:]
			} else if len(normalQ) > 0 {
				current = &scheduled{req: normalQ[0]}
				normalQ = normalQ[1:]
			}
		}

		if current != nil && timer == nil {
			when, cancel := s.state.Reserve(s.clock(), current.req.priority)
			current.when = when
			current.cancel = cancel

			delay := time.Until(when)
			if delay < 0 {
				delay = 0
			}
			timer = time.NewTimer(delay)
			timerC = timer.C
		}

		select {
		case req := <-s.incoming:
			if req.priority {
				priorityQ = append(priorityQ, req)
				// Preempt a waiting normal request so priority can jump ahead.
				if current != nil && !current.req.priority {
					if timer != nil {
						timer.Stop()
					}
					if current.cancel != nil {
						current.cancel()
					}
					normalQ = append([]*requestPermit{current.req}, normalQ...)
					current = nil
					timer = nil
					timerC = nil
				}
			} else {
				normalQ = append(normalQ, req)
			}

		case <-timerC:
			if timer != nil {
				timer.Stop()
			}
			current.req.res <- nil
			current = nil
			timer = nil
			timerC = nil

		case <-func() <-chan struct{} {
			if current == nil {
				return nil
			}
			return current.req.ctx.Done()
		}():
			if current != nil {
				if timer != nil {
					timer.Stop()
				}
				if current.cancel != nil {
					current.cancel()
				}
				current.req.res <- current.req.ctx.Err()
				current = nil
				timer = nil
				timerC = nil
			}
		}
	}
}

// RateScheduler manages per-key queues with priority and normal traffic.
type RateScheduler struct {
	mu       sync.Mutex
	perKey   map[string]*perKeyScheduler
	newState func() *ratelimit.State
}

func NewRateScheduler(newState func() *ratelimit.State) *RateScheduler {
	return &RateScheduler{
		perKey:   make(map[string]*perKeyScheduler),
		newState: newState,
	}
}

func (s *RateScheduler) Acquire(ctx context.Context, key string, priority bool) error {
	s.mu.Lock()
	sched, ok := s.perKey[key]
	if !ok {
		sched = newPerKeyScheduler(s.newState())
		s.perKey[key] = sched
	}
	s.mu.Unlock()

	req := &requestPermit{
		ctx:      ctx,
		priority: priority,
		res:      make(chan error, 1),
	}

	select {
	case sched.incoming <- req:
	case <-ctx.Done():
		return ctx.Err()
	}

	return <-req.res
}

func (s *RateScheduler) UpdateFromHeaders(key string, h http.Header) {
	s.mu.Lock()
	sched, ok := s.perKey[key]
	s.mu.Unlock()
	if !ok {
		return
	}
	sched.state.UpdateFromHeaders(h)
}
