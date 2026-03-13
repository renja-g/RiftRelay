package limiter

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func intPtr(i int) *int { return &i }

func TestLimiterPinnedKeySelection(t *testing.T) {
	tests := []struct {
		name       string
		tokenIndex int
	}{
		{"pinned_to_key_0", 0},
		{"pinned_to_key_1", 1},
		{"pinned_to_key_2", 2},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			l, err := New(Config{
				KeyCount:         3,
				QueueCapacity:    8,
				AdditionalWindow: 0,
			})
			if err != nil {
				t.Fatalf("new limiter: %v", err)
			}
			defer l.Close()

			headers := make(http.Header)
			headers.Set("X-Method-Rate-Limit", "20:1")
			headers.Set("X-Method-Rate-Limit-Count", "0:1")
			l.Observe(Observation{
				Region:     "na1",
				Bucket:     "na1:lol/status/v4/platform-data",
				KeyIndex:   tt.tokenIndex,
				StatusCode: http.StatusOK,
				Header:     headers,
			})
			time.Sleep(20 * time.Millisecond)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			ticket, err := l.Admit(ctx, Admission{
				Region:     "na1",
				Bucket:     "na1:lol/status/v4/platform-data",
				Priority:   PriorityNormal,
				TokenIndex: intPtr(tt.tokenIndex),
			})
			if err != nil {
				t.Fatalf("admit failed: %v", err)
			}
			if ticket.KeyIndex != tt.tokenIndex {
				t.Fatalf("expected KeyIndex=%d, got %d", tt.tokenIndex, ticket.KeyIndex)
			}
		})
	}
}

func TestLimiterPinnedDoesNotBlock(t *testing.T) {
	// When a pinned request at the head cannot be served (its key exhausted),
	// subsequent non-pinned requests must be served by other keys.
	l, err := New(Config{
		KeyCount:         2,
		QueueCapacity:    8,
		AdditionalWindow: 0,
	})
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	defer l.Close()

	// Exhaust key 0 (method limit 1:1, count 1:1)
	exhausted := make(http.Header)
	exhausted.Set("X-Method-Rate-Limit", "1:1")
	exhausted.Set("X-Method-Rate-Limit-Count", "1:1")
	l.Observe(Observation{
		Region:     "na1",
		Bucket:     "na1:lol/status/v4/platform-data",
		KeyIndex:   0,
		StatusCode: http.StatusOK,
		Header:     exhausted,
	})
	// Give key 1 capacity
	available := make(http.Header)
	available.Set("X-Method-Rate-Limit", "1:1")
	available.Set("X-Method-Rate-Limit-Count", "0:1")
	l.Observe(Observation{
		Region:     "na1",
		Bucket:     "na1:lol/status/v4/platform-data",
		KeyIndex:   1,
		StatusCode: http.StatusOK,
		Header:     available,
	})
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	type result struct {
		ticket Ticket
		err    error
	}
	pinnedCh := make(chan result, 1)
	nonPinnedCh := make(chan result, 1)

	go func() {
		ticket, err := l.Admit(ctx, Admission{
			Region:     "na1",
			Bucket:     "na1:lol/status/v4/platform-data",
			Priority:   PriorityNormal,
			TokenIndex: intPtr(0),
		})
		pinnedCh <- result{ticket: ticket, err: err}
	}()
	time.Sleep(10 * time.Millisecond)

	go func() {
		ticket, err := l.Admit(ctx, Admission{
			Region:   "na1",
			Bucket:   "na1:lol/status/v4/platform-data",
			Priority: PriorityNormal,
		})
		nonPinnedCh <- result{ticket: ticket, err: err}
	}()

	// Non-pinned should be admitted first (within 500ms) using key 1
	select {
	case r := <-nonPinnedCh:
		if r.err != nil {
			t.Fatalf("non-pinned request failed: %v", r.err)
		}
		if r.ticket.KeyIndex != 1 {
			t.Fatalf("expected non-pinned to use key 1, got key %d", r.ticket.KeyIndex)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("non-pinned request did not complete within 500ms (pinned blocked the queue)")
	}

	// Pinned is still queued (key 0 exhausted). Wait for it with longer timeout or cancel.
	select {
	case r := <-pinnedCh:
		if r.err != nil {
			t.Fatalf("pinned request eventually failed: %v", r.err)
		}
		if r.ticket.KeyIndex != 0 {
			t.Fatalf("expected pinned to use key 0, got key %d", r.ticket.KeyIndex)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("pinned request did not complete within 2s (key 0 may need observation to recover)")
	}
}

func TestLimiterPinnedMultipleDoesNotBlock(t *testing.T) {
	// Multiple pinned requests to the same exhausted key at the head must not
	// block a non-pinned request behind them.
	l, err := New(Config{
		KeyCount:         2,
		QueueCapacity:    8,
		AdditionalWindow: 0,
	})
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	defer l.Close()

	exhausted := make(http.Header)
	exhausted.Set("X-Method-Rate-Limit", "1:1")
	exhausted.Set("X-Method-Rate-Limit-Count", "1:1")
	l.Observe(Observation{
		Region:     "na1",
		Bucket:     "na1:lol/status/v4/platform-data",
		KeyIndex:   0,
		StatusCode: http.StatusOK,
		Header:     exhausted,
	})
	available := make(http.Header)
	available.Set("X-Method-Rate-Limit", "1:1")
	available.Set("X-Method-Rate-Limit-Count", "0:1")
	l.Observe(Observation{
		Region:     "na1",
		Bucket:     "na1:lol/status/v4/platform-data",
		KeyIndex:   1,
		StatusCode: http.StatusOK,
		Header:     available,
	})
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	nonPinnedCh := make(chan *Ticket, 1)
	go func() {
		ticket, err := l.Admit(ctx, Admission{
			Region:   "na1",
			Bucket:   "na1:lol/status/v4/platform-data",
			Priority: PriorityNormal,
		})
		if err != nil {
			return
		}
		nonPinnedCh <- &ticket
	}()

	// Enqueue two pinned (to key 0) before the non-pinned gets a chance.
	// Launch pinned first, small delay, launch second pinned, small delay, non-pinned is already running.
	for i := 0; i < 2; i++ {
		go func() {
			_, _ = l.Admit(ctx, Admission{
				Region:     "na1",
				Bucket:     "na1:lol/status/v4/platform-data",
				Priority:   PriorityNormal,
				TokenIndex: intPtr(0),
			})
		}()
		time.Sleep(5 * time.Millisecond)
	}

	// Non-pinned should be admitted using key 1 within 500ms
	select {
	case ticket := <-nonPinnedCh:
		if ticket.KeyIndex != 1 {
			t.Fatalf("expected non-pinned to use key 1, got key %d", ticket.KeyIndex)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("non-pinned request did not complete within 500ms (pinned requests blocked the queue)")
	}
}

func TestLimiterPinnedInvalidTokenIndexRejected(t *testing.T) {
	tests := []struct {
		name       string
		tokenIndex int
	}{
		{"index_out_of_range", 2},
		{"index_negative", -1},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			l, err := New(Config{
				KeyCount:         2,
				QueueCapacity:    8,
				AdditionalWindow: 0,
			})
			if err != nil {
				t.Fatalf("new limiter: %v", err)
			}
			defer l.Close()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			_, err = l.Admit(ctx, Admission{
				Region:     "na1",
				Bucket:     "na1:lol/status/v4/platform-data",
				Priority:   PriorityNormal,
				TokenIndex: intPtr(tt.tokenIndex),
			})
			rejected, ok := err.(*RejectedError)
			if !ok {
				t.Fatalf("expected RejectedError, got %T (%v)", err, err)
			}
			if rejected.Reason != "invalid_token_index" {
				t.Fatalf("expected Reason=invalid_token_index, got %q", rejected.Reason)
			}
		})
	}
}

func TestLimiterPinnedEventuallyServedWhenKeyRecovers(t *testing.T) {
	// Pinned requests are retried and served when their key becomes available.
	l, err := New(Config{
		KeyCount:         2,
		QueueCapacity:    8,
		AdditionalWindow: 0,
	})
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	defer l.Close()

	exhausted := make(http.Header)
	exhausted.Set("X-Method-Rate-Limit", "1:1")
	exhausted.Set("X-Method-Rate-Limit-Count", "1:1")
	l.Observe(Observation{
		Region:     "na1",
		Bucket:     "na1:lol/status/v4/platform-data",
		KeyIndex:   0,
		StatusCode: http.StatusOK,
		Header:     exhausted,
	})
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	type pinnedResult struct {
		ticket Ticket
		err    error
	}
	pinnedCh := make(chan pinnedResult, 1)
	go func() {
		ticket, err := l.Admit(ctx, Admission{
			Region:     "na1",
			Bucket:     "na1:lol/status/v4/platform-data",
			Priority:   PriorityNormal,
			TokenIndex: intPtr(0),
		})
		pinnedCh <- pinnedResult{ticket: ticket, err: err}
	}()

	// Feed observation to reset key 0's method limit (simulating upstream response)
	recovered := make(http.Header)
	recovered.Set("X-Method-Rate-Limit", "1:1")
	recovered.Set("X-Method-Rate-Limit-Count", "0:1")
	l.Observe(Observation{
		Region:     "na1",
		Bucket:     "na1:lol/status/v4/platform-data",
		KeyIndex:   0,
		StatusCode: http.StatusOK,
		Header:     recovered,
	})

	select {
	case r := <-pinnedCh:
		if r.err != nil {
			t.Fatalf("pinned request failed: %v", r.err)
		}
		if r.ticket.KeyIndex != 0 {
			t.Fatalf("expected pinned to use key 0, got key %d", r.ticket.KeyIndex)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("pinned request did not complete within 2s after key recovery")
	}
}

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

func TestDefaultRateLimitsAppliedBeforeObservation(t *testing.T) {
	l, err := New(Config{
		KeyCount:         1,
		QueueCapacity:    8,
		AdditionalWindow: 0,
		DefaultAppLimits: "20:1,100:120",
	})
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	defer l.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	if _, err := l.Admit(ctx, Admission{
		Region:   "europe",
		Bucket:   "europe:riot/account/v1/accounts/by-riot-id/test/123",
		Priority: PriorityNormal,
	}); err != nil {
		t.Fatalf("first admit failed: %v", err)
	}
	firstDuration := time.Since(start)

	start = time.Now()
	if _, err := l.Admit(ctx, Admission{
		Region:   "europe",
		Bucket:   "europe:riot/account/v1/accounts/by-riot-id/test/456",
		Priority: PriorityNormal,
	}); err != nil {
		t.Fatalf("second admit failed: %v", err)
	}
	secondWait := time.Since(start)

	if firstDuration > 10*time.Millisecond {
		t.Logf("first request took %s, expected near-instant", firstDuration)
	}

	if secondWait < 20*time.Millisecond {
		t.Fatalf("second request was not paced by default limits: wait=%s (expected >20ms)", secondWait)
	}
}

func TestDefaultRateLimitsPreventBurstOnStartup(t *testing.T) {
	l, err := New(Config{
		KeyCount:         1,
		QueueCapacity:    16,
		AdditionalWindow: 0,
		DefaultAppLimits: "20:1",
	})
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	defer l.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	const numRequests = 10
	type result struct {
		idx  int
		err  error
		wait time.Duration
	}
	results := make(chan result, numRequests)

	start := time.Now()
	for i := 0; i < numRequests; i++ {
		go func(idx int) {
			reqStart := time.Now()
			_, err := l.Admit(ctx, Admission{
				Region:   "europe",
				Bucket:   fmt.Sprintf("europe:riot/account/v1/accounts/by-riot-id/test/%d", idx),
				Priority: PriorityNormal,
			})
			results <- result{idx: idx, err: err, wait: time.Since(reqStart)}
		}(i)
	}

	successCount := 0
	var lastCompletion time.Duration
	for i := 0; i < numRequests; i++ {
		r := <-results
		if r.err != nil {
			t.Logf("request %d failed: %v", r.idx, r.err)
			continue
		}
		successCount++
		if r.wait > lastCompletion {
			lastCompletion = r.wait
		}
	}

	totalDuration := time.Since(start)

	if successCount != numRequests {
		t.Fatalf("expected %d successful requests, got %d", numRequests, successCount)
	}

	minExpectedDuration := 200 * time.Millisecond
	if totalDuration < minExpectedDuration {
		t.Fatalf("requests were not paced by default limits: total=%s (expected >%s)", totalDuration, minExpectedDuration)
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

func TestLimiterRecoveryAfterBurst(t *testing.T) {
	const burstSize = 200

	l, err := New(Config{
		KeyCount:         1,
		QueueCapacity:    burstSize + 16,
		AdditionalWindow: 0,
		DefaultAppLimits: "20:1",
	})
	if err != nil {
		t.Fatalf("new limiter: %v", err)
	}
	defer l.Close()

	// Seed with initial observation so method limits are established.
	seed := make(http.Header)
	seed.Set("X-Method-Rate-Limit", "20:1")
	seed.Set("X-Method-Rate-Limit-Count", "0:1")
	seed.Set("X-App-Rate-Limit", "20:1")
	seed.Set("X-App-Rate-Limit-Count", "0:1")
	l.Observe(Observation{
		Region:     "europe",
		Bucket:     "europe:riot/account/v1/accounts/by-riot-id/test/123",
		KeyIndex:   0,
		StatusCode: http.StatusOK,
		Header:     seed,
	})
	time.Sleep(30 * time.Millisecond)

	// Fire burst of high-priority requests.
	type burstResult struct {
		ticket Ticket
		err    error
	}
	results := make(chan burstResult, burstSize)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for i := 0; i < burstSize; i++ {
		go func() {
			ticket, err := l.Admit(ctx, Admission{
				Region:   "europe",
				Bucket:   "europe:riot/account/v1/accounts/by-riot-id/test/123",
				Priority: PriorityHigh,
			})
			results <- burstResult{ticket: ticket, err: err}
		}()
	}

	// Collect admitted requests and feed observations back (simulating upstream responses).
	admitted := 0
	for i := 0; i < burstSize; i++ {
		select {
		case r := <-results:
			if r.err != nil {
				continue
			}
			admitted++
			// Feed observation back with realistic headers.
			obs := make(http.Header)
			obs.Set("X-Method-Rate-Limit", "20:1")
			obs.Set("X-Method-Rate-Limit-Count", fmt.Sprintf("%d:1", (admitted%20)+1))
			obs.Set("X-App-Rate-Limit", "20:1")
			obs.Set("X-App-Rate-Limit-Count", fmt.Sprintf("%d:1", (admitted%20)+1))
			l.Observe(Observation{
				Region:     "europe",
				Bucket:     "europe:riot/account/v1/accounts/by-riot-id/test/123",
				KeyIndex:   r.ticket.KeyIndex,
				StatusCode: http.StatusOK,
				Header:     obs,
			})
		case <-time.After(25 * time.Second):
			t.Fatalf("timed out collecting burst results (got %d/%d)", admitted, burstSize)
		}
	}

	if admitted == 0 {
		t.Fatalf("no requests were admitted from the burst")
	}
	t.Logf("burst: %d/%d admitted", admitted, burstSize)

	// The critical check: after the burst, a normal-priority request should be
	// admitted within a reasonable time (not minutes).
	normalCtx, normalCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer normalCancel()

	start := time.Now()
	_, err = l.Admit(normalCtx, Admission{
		Region:   "europe",
		Bucket:   "europe:riot/account/v1/accounts/by-riot-id/test/123",
		Priority: PriorityNormal,
	})
	waited := time.Since(start)

	if err != nil {
		t.Fatalf("normal request after burst failed (waited %s): %v", waited, err)
	}
	t.Logf("normal request after burst admitted in %s", waited)
}
