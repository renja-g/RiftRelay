package limiter

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestLimiterRejectsWhenQueueFull(t *testing.T) {
	l, err := New(Config{
		KeyCount:         1,
		QueueCapacity:    1,
		AdditionalWindow: 0,
	})
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	defer l.Close()

	headers := make(http.Header)
	headers.Set("Retry-After", "2")
	headers.Set("X-Rate-Limit-Type", "method")
	l.Observe(Observation{
		Region:     "na1",
		Bucket:     "na1:lol/status/v4/platform-data",
		KeyIndex:   0,
		StatusCode: http.StatusTooManyRequests,
		Header:     headers,
	})
	time.Sleep(20 * time.Millisecond)

	// First request should remain queued until its context expires.
	firstCtx, cancelFirst := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancelFirst()
	firstDone := make(chan error, 1)
	go func() {
		_, err := l.Admit(firstCtx, Admission{
			Region:   "na1",
			Bucket:   "na1:lol/status/v4/platform-data",
			Priority: PriorityNormal,
		})
		firstDone <- err
	}()

	// Give the loop a tiny moment to enqueue the first request.
	time.Sleep(10 * time.Millisecond)

	secondCtx, cancelSecond := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelSecond()
	_, err = l.Admit(secondCtx, Admission{
		Region:   "na1",
		Bucket:   "na1:lol/status/v4/platform-data",
		Priority: PriorityNormal,
	})
	rejected, ok := err.(*RejectedError)
	if !ok {
		t.Fatalf("expected RejectedError, got %T (%v)", err, err)
	}
	if rejected.Reason != "queue_full" {
		t.Fatalf("expected queue_full, got %q", rejected.Reason)
	}

	if err := <-firstDone; err == nil {
		t.Fatalf("expected first request to timeout")
	}
}

func TestLimiterHighPriorityWinsAfterWait(t *testing.T) {
	l, err := New(Config{
		KeyCount:         1,
		QueueCapacity:    8,
		AdditionalWindow: 0,
	})
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	defer l.Close()

	headers := make(http.Header)
	headers.Set("X-Method-Rate-Limit", "1:1")
	headers.Set("X-Method-Rate-Limit-Count", "1:1")
	l.Observe(Observation{
		Region:     "na1",
		Bucket:     "na1:lol/match/v5/matches/by-puuid/abc/ids",
		KeyIndex:   0,
		StatusCode: http.StatusOK,
		Header:     headers,
	})
	time.Sleep(20 * time.Millisecond)

	results := make(chan string, 2)

	launch := func(name string, priority Priority) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
			defer cancel()
			_, err := l.Admit(ctx, Admission{
				Region:   "na1",
				Bucket:   "na1:lol/match/v5/matches/by-puuid/abc/ids",
				Priority: priority,
			})
			if err != nil {
				results <- "error:" + name
				return
			}
			results <- name
		}()
	}

	launch("normal", PriorityNormal)
	time.Sleep(5 * time.Millisecond)
	launch("high", PriorityHigh)

	first := <-results
	second := <-results

	if first != "high" || second != "normal" {
		t.Fatalf("expected high before normal, got first=%q second=%q", first, second)
	}
}

func TestLimiterPriorityPacingBehavior(t *testing.T) {
	tests := []struct {
		name          string
		priority      Priority
		minSecondWait time.Duration
		maxSecondWait time.Duration
	}{
		{
			name:          "normal priority remains paced",
			priority:      PriorityNormal,
			minSecondWait: 150 * time.Millisecond,
			maxSecondWait: 600 * time.Millisecond,
		},
		{
			name:          "high priority bypasses pacing",
			priority:      PriorityHigh,
			minSecondWait: 0,
			maxSecondWait: 120 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			l, err := New(Config{
				KeyCount:         1,
				QueueCapacity:    8,
				AdditionalWindow: 0,
			})
			if err != nil {
				t.Fatalf("new limiter: %v", err)
			}
			defer l.Close()

			headers := make(http.Header)
			headers.Set("X-Method-Rate-Limit", "5:1")
			headers.Set("X-Method-Rate-Limit-Count", "0:1")
			l.Observe(Observation{
				Region:     "na1",
				Bucket:     "na1:lol/status/v4/platform-data",
				KeyIndex:   0,
				StatusCode: http.StatusOK,
				Header:     headers,
			})
			time.Sleep(20 * time.Millisecond)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			if _, err := l.Admit(ctx, Admission{
				Region:   "na1",
				Bucket:   "na1:lol/status/v4/platform-data",
				Priority: tt.priority,
			}); err != nil {
				t.Fatalf("first admit failed: %v", err)
			}

			start := time.Now()
			if _, err := l.Admit(ctx, Admission{
				Region:   "na1",
				Bucket:   "na1:lol/status/v4/platform-data",
				Priority: tt.priority,
			}); err != nil {
				t.Fatalf("second admit failed: %v", err)
			}
			waited := time.Since(start)

			if waited < tt.minSecondWait || waited > tt.maxSecondWait {
				t.Fatalf("unexpected second admit wait: %s (expected %s..%s)", waited, tt.minSecondWait, tt.maxSecondWait)
			}
		})
	}
}

func TestLimiterResumeAfterIdleTightensPacing(t *testing.T) {
	type scenario struct {
		name             string
		idleBeforeSecond time.Duration
	}

	scenarios := []scenario{
		{
			name:             "no idle before second request",
			idleBeforeSecond: 0,
		},
		{
			name:             "idle before second request",
			idleBeforeSecond: 600 * time.Millisecond,
		},
	}

	measureThirdWait := func(t *testing.T, idleBeforeSecond time.Duration) time.Duration {
		t.Helper()

		l, err := New(Config{
			KeyCount:         1,
			QueueCapacity:    8,
			AdditionalWindow: 0,
		})
		if err != nil {
			t.Fatalf("new limiter: %v", err)
		}
		defer l.Close()

		headers := make(http.Header)
		headers.Set("X-Method-Rate-Limit", "5:1")
		headers.Set("X-Method-Rate-Limit-Count", "0:1")
		l.Observe(Observation{
			Region:     "na1",
			Bucket:     "na1:lol/status/v4/platform-data",
			KeyIndex:   0,
			StatusCode: http.StatusOK,
			Header:     headers,
		})
		time.Sleep(20 * time.Millisecond)

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		admit := func(label string) {
			if _, err := l.Admit(ctx, Admission{
				Region:   "na1",
				Bucket:   "na1:lol/status/v4/platform-data",
				Priority: PriorityNormal,
			}); err != nil {
				t.Fatalf("%s admit failed: %v", label, err)
			}
		}

		admit("first")
		if idleBeforeSecond > 0 {
			time.Sleep(idleBeforeSecond)
		}
		admit("second")

		start := time.Now()
		admit("third")
		return time.Since(start)
	}

	waits := make(map[string]time.Duration, len(scenarios))
	for _, tt := range scenarios {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			waits[tt.name] = measureThirdWait(t, tt.idleBeforeSecond)
		})
	}

	noIdleWait := waits["no idle before second request"]
	idleWait := waits["idle before second request"]

	if idleWait >= noIdleWait {
		t.Fatalf("expected tighter pacing after idle; no-idle wait=%s idle wait=%s", noIdleWait, idleWait)
	}

	const minTightening = 50 * time.Millisecond
	if noIdleWait-idleWait < minTightening {
		t.Fatalf(
			"expected idle case to tighten by at least %s; no-idle wait=%s idle wait=%s",
			minTightening,
			noIdleWait,
			idleWait,
		)
	}
}

func TestLimiterQueuedRequestRecalculatesPacing(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, l *Limiter)
	}{
		{
			name: "queued request can execute sooner as interval shrinks",
			run: func(t *testing.T, l *Limiter) {
				headers := make(http.Header)
				headers.Set("X-Method-Rate-Limit", "2:1")
				headers.Set("X-Method-Rate-Limit-Count", "0:1")
				l.Observe(Observation{
					Region:     "na1",
					Bucket:     "na1:lol/status/v4/platform-data",
					KeyIndex:   0,
					StatusCode: http.StatusOK,
					Header:     headers,
				})
				time.Sleep(20 * time.Millisecond)

				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()

				if _, err := l.Admit(ctx, Admission{
					Region:   "na1",
					Bucket:   "na1:lol/status/v4/platform-data",
					Priority: PriorityNormal,
				}); err != nil {
					t.Fatalf("first admit failed: %v", err)
				}

				start := time.Now()
				if _, err := l.Admit(ctx, Admission{
					Region:   "na1",
					Bucket:   "na1:lol/status/v4/platform-data",
					Priority: PriorityNormal,
				}); err != nil {
					t.Fatalf("second admit failed: %v", err)
				}
				waited := time.Since(start)

				if waited < 350*time.Millisecond || waited > 950*time.Millisecond {
					t.Fatalf("unexpected queued wait after recalculation: %s", waited)
				}
			},
		},
		{
			name: "queued request can be delayed after stricter observation",
			run: func(t *testing.T, l *Limiter) {
				initial := make(http.Header)
				initial.Set("X-Method-Rate-Limit", "4:2")
				initial.Set("X-Method-Rate-Limit-Count", "0:2")
				l.Observe(Observation{
					Region:     "na1",
					Bucket:     "na1:lol/status/v4/platform-data",
					KeyIndex:   0,
					StatusCode: http.StatusOK,
					Header:     initial,
				})
				time.Sleep(20 * time.Millisecond)

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				if _, err := l.Admit(ctx, Admission{
					Region:   "na1",
					Bucket:   "na1:lol/status/v4/platform-data",
					Priority: PriorityNormal,
				}); err != nil {
					t.Fatalf("first admit failed: %v", err)
				}

				done := make(chan time.Duration, 1)
				errCh := make(chan error, 1)
				go func() {
					start := time.Now()
					_, err := l.Admit(ctx, Admission{
						Region:   "na1",
						Bucket:   "na1:lol/status/v4/platform-data",
						Priority: PriorityNormal,
					})
					if err != nil {
						errCh <- err
						return
					}
					done <- time.Since(start)
				}()

				time.Sleep(200 * time.Millisecond)
				stricter := make(http.Header)
				stricter.Set("X-Method-Rate-Limit", "4:2")
				stricter.Set("X-Method-Rate-Limit-Count", "3:2")
				l.Observe(Observation{
					Region:     "na1",
					Bucket:     "na1:lol/status/v4/platform-data",
					KeyIndex:   0,
					StatusCode: http.StatusOK,
					Header:     stricter,
				})

				select {
				case err := <-errCh:
					t.Fatalf("second admit failed: %v", err)
				case waited := <-done:
					if waited < 900*time.Millisecond {
						t.Fatalf("expected stricter update to delay queued request, got wait=%s", waited)
					}
					if waited > 3*time.Second {
						t.Fatalf("queued wait exceeded expected upper bound: %s", waited)
					}
				case <-time.After(4 * time.Second):
					t.Fatalf("timed out waiting for queued request to complete")
				}
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			l, err := New(Config{
				KeyCount:         1,
				QueueCapacity:    8,
				AdditionalWindow: 0,
			})
			if err != nil {
				t.Fatalf("new limiter: %v", err)
			}
			defer l.Close()

			tt.run(t, l)
		})
	}
}

func TestLimiterPriorityBurstSlowsLaterNormalPacing(t *testing.T) {
	type scenario struct {
		name           string
		secondPriority Priority
	}

	scenarios := []scenario{
		{
			name:           "second request is normal",
			secondPriority: PriorityNormal,
		},
		{
			name:           "second request is high priority",
			secondPriority: PriorityHigh,
		},
	}

	measureThirdNormalWait := func(t *testing.T, secondPriority Priority) time.Duration {
		t.Helper()

		l, err := New(Config{
			KeyCount:         1,
			QueueCapacity:    8,
			AdditionalWindow: 0,
		})
		if err != nil {
			t.Fatalf("new limiter: %v", err)
		}
		defer l.Close()

		headers := make(http.Header)
		headers.Set("X-Method-Rate-Limit", "5:1")
		headers.Set("X-Method-Rate-Limit-Count", "0:1")
		l.Observe(Observation{
			Region:     "na1",
			Bucket:     "na1:lol/status/v4/platform-data",
			KeyIndex:   0,
			StatusCode: http.StatusOK,
			Header:     headers,
		})
		time.Sleep(20 * time.Millisecond)

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		admitWithPriority := func(label string, priority Priority) {
			if _, err := l.Admit(ctx, Admission{
				Region:   "na1",
				Bucket:   "na1:lol/status/v4/platform-data",
				Priority: priority,
			}); err != nil {
				t.Fatalf("%s admit failed: %v", label, err)
			}
		}

		admitWithPriority("first", PriorityNormal)
		admitWithPriority("second", secondPriority)

		start := time.Now()
		admitWithPriority("third", PriorityNormal)
		return time.Since(start)
	}

	waits := make(map[string]time.Duration, len(scenarios))
	for _, tt := range scenarios {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			waits[tt.name] = measureThirdNormalWait(t, tt.secondPriority)
		})
	}

	normalSecondWait := waits["second request is normal"]
	highSecondWait := waits["second request is high priority"]

	if highSecondWait <= normalSecondWait {
		t.Fatalf(
			"expected later normal request to wait longer after high-priority burst; normal-second wait=%s high-second wait=%s",
			normalSecondWait,
			highSecondWait,
		)
	}

	const minSlowdown = 40 * time.Millisecond
	if highSecondWait-normalSecondWait < minSlowdown {
		t.Fatalf(
			"expected slowdown of at least %s after high-priority burst; normal-second wait=%s high-second wait=%s",
			minSlowdown,
			normalSecondWait,
			highSecondWait,
		)
	}
}

func TestLimiterHighPriorityCutsInFrontOfQueuedNormals(t *testing.T) {
	const normalQueued = 10

	l, err := New(Config{
		KeyCount:         1,
		QueueCapacity:    64,
		AdditionalWindow: 500 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	defer l.Close()

	headers := make(http.Header)
	headers.Set("X-Method-Rate-Limit", "1:1")
	headers.Set("X-Method-Rate-Limit-Count", "1:1")
	l.Observe(Observation{
		Region:     "na1",
		Bucket:     "na1:lol/status/v4/platform-data",
		KeyIndex:   0,
		StatusCode: http.StatusOK,
		Header:     headers,
	})
	time.Sleep(20 * time.Millisecond)

	type result struct {
		name string
		err  error
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	results := make(chan result, normalQueued+1)
	launch := func(name string, priority Priority) {
		go func() {
			_, err := l.Admit(ctx, Admission{
				Region:   "na1",
				Bucket:   "na1:lol/status/v4/platform-data",
				Priority: priority,
			})
			results <- result{name: name, err: err}
		}()
	}

	for i := 0; i < normalQueued; i++ {
		launch("normal", PriorityNormal)
	}
	// Give the dispatcher a brief moment to queue normals before the priority request arrives.
	time.Sleep(30 * time.Millisecond)
	launch("high", PriorityHigh)

	timeout := time.After(3 * time.Second)
	for {
		select {
		case out := <-results:
			if out.err != nil {
				continue
			}
			if out.name != "high" {
				t.Fatalf("expected high-priority request to execute first, got %q", out.name)
			}
			return
		case <-timeout:
			t.Fatalf("timed out waiting for first successful admission")
		}
	}
}

func TestLimiterColdStartDefaultLimitsPaceRequests(t *testing.T) {
	// Verify that default app limits pace requests even before any observation is received.
	l, err := New(Config{
		KeyCount:      1,
		QueueCapacity: 64,
		DefaultAppLimits: []RateLimit{
			{Requests: 3, Window: 1 * time.Second},
		},
	})
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	defer l.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	admit := func(label string) {
		if _, err := l.Admit(ctx, Admission{
			Region:   "na1",
			Bucket:   "na1:lol/status/v4/platform-data",
			Priority: PriorityNormal,
		}); err != nil {
			t.Fatalf("%s admit failed: %v", label, err)
		}
	}

	// Admit 4 requests with no prior observation. With a 3 req/1s default app
	// limit, the 4th request must wait for the window to reset, so the total
	// elapsed time must be >= 1 second.
	start := time.Now()
	admit("first")
	admit("second")
	admit("third")
	admit("fourth") // must wait for the 1-second window to reset
	elapsed := time.Since(start)

	if elapsed < 700*time.Millisecond {
		t.Fatalf("expected cold-start default limits to pace 4 requests over >=700ms; elapsed=%s", elapsed)
	}
}

func TestLimiterColdStartDefaultLimitsOverriddenByObservation(t *testing.T) {
	// Default limits should be replaced by actual observed limits once an observation arrives.
	l, err := New(Config{
		KeyCount:      1,
		QueueCapacity: 64,
		DefaultAppLimits: []RateLimit{
			{Requests: 2, Window: 1 * time.Second},
		},
	})
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	defer l.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	admit := func(label string) {
		if _, err := l.Admit(ctx, Admission{
			Region:   "na1",
			Bucket:   "na1:lol/status/v4/platform-data",
			Priority: PriorityNormal,
		}); err != nil {
			t.Fatalf("%s admit failed: %v", label, err)
		}
	}

	// Seed an observation with a wider limit so the default is superseded.
	headers := make(http.Header)
	headers.Set("X-App-Rate-Limit", "10:1")
	headers.Set("X-App-Rate-Limit-Count", "1:1")
	l.Observe(Observation{
		Region:     "na1",
		Bucket:     "na1:lol/status/v4/platform-data",
		KeyIndex:   0,
		StatusCode: http.StatusOK,
		Header:     headers,
	})
	time.Sleep(20 * time.Millisecond)

	// With limit=10/1s we should be able to admit more than 2 requests quickly.
	for i := range 8 {
		start := time.Now()
		admit(fmt.Sprintf("req-%d", i+2))
		if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
			t.Fatalf("request %d was unexpectedly delayed by %s (default limit should be overridden)", i+2, elapsed)
		}
	}
}
