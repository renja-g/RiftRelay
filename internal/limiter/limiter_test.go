package limiter

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

func TestNewValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "invalid key count",
			cfg: Config{
				KeyCount:      0,
				QueueCapacity: 1,
			},
		},
		{
			name: "invalid queue capacity",
			cfg: Config{
				KeyCount:      1,
				QueueCapacity: 0,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l, err := New(tt.cfg)
			if err == nil {
				_ = l.Close()
				t.Fatal("New() error = nil, want non-nil")
			}
		})
	}
}

func TestLimiterAdmitImmediate(t *testing.T) {
	t.Parallel()

	l, err := New(Config{
		KeyCount:         2,
		QueueCapacity:    2,
		DefaultAppLimits: "20:1",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = l.Close() }()

	ticket, err := l.Admit(context.Background(), Admission{
		Region:   "europe",
		Bucket:   "europe:riot/account/v1/accounts/me",
		Priority: PriorityNormal,
	})
	if err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	if ticket.KeyIndex < 0 || ticket.KeyIndex >= 2 {
		t.Fatalf("Ticket.KeyIndex = %d, want valid index", ticket.KeyIndex)
	}
}

func TestLimiterRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	l, err := New(Config{
		KeyCount:         1,
		QueueCapacity:    1,
		DefaultAppLimits: "20:1",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = l.Close() }()

	tests := []struct {
		name      string
		admission Admission
		want      string
	}{
		{
			name: "missing route",
			admission: Admission{
				Region: "",
				Bucket: "",
			},
			want: "invalid_route",
		},
		{
			name: "invalid token index",
			admission: Admission{
				Region:     "europe",
				Bucket:     "europe:riot/account/v1/accounts/me",
				TokenIndex: intPtr(2),
			},
			want: "invalid_token_index",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := l.Admit(context.Background(), tt.admission)
			var rejected *RejectedError
			if !errors.As(err, &rejected) {
				t.Fatalf("Admit() error = %v, want RejectedError", err)
			}
			if got := rejected.Reason; got != tt.want {
				t.Fatalf("RejectedError.Reason = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLimiterQueueFull(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l, err := New(Config{
			KeyCount:         1,
			QueueCapacity:    1,
			DefaultAppLimits: "1:60",
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		baseAdmission := Admission{
			Region:   "europe",
			Bucket:   "europe:riot/account/v1/accounts/me",
			Priority: PriorityNormal,
		}

		if _, err := l.Admit(context.Background(), baseAdmission); err != nil {
			t.Fatalf("first Admit() error = %v", err)
		}

		waitingDone := make(chan error, 1)
		go func() {
			_, admitErr := l.Admit(context.Background(), baseAdmission)
			waitingDone <- admitErr
		}()

		synctest.Wait()

		_, err = l.Admit(context.Background(), baseAdmission)
		var rejected *RejectedError
		if !errors.As(err, &rejected) {
			t.Fatalf("queued overflow Admit() error = %v, want RejectedError", err)
		}
		if got, want := rejected.Reason, "queue_full"; got != want {
			t.Fatalf("RejectedError.Reason = %q, want %q", got, want)
		}
		if rejected.RetryAfter < time.Second {
			t.Fatalf("RejectedError.RetryAfter = %v, want >= 1s", rejected.RetryAfter)
		}

		_ = l.Close()
		synctest.Wait()

		var shutdownErr error
		select {
		case shutdownErr = <-waitingDone:
		default:
			t.Fatal("waiting admission did not finish after Close()")
		}

		if !errors.As(shutdownErr, &rejected) || rejected.Reason != "shutting_down" {
			t.Fatalf("waiting admission error = %v, want shutting_down RejectedError", shutdownErr)
		}
	})
}

func TestLimiterHighPriorityBypassesQueuedNormal(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l, err := New(Config{
			KeyCount:         1,
			QueueCapacity:    4,
			DefaultAppLimits: "1:1",
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer func() { _ = l.Close() }()

		bucket := "europe:riot/account/v1/accounts/me"
		if _, err := l.Admit(context.Background(), Admission{Region: "europe", Bucket: bucket, Priority: PriorityNormal}); err != nil {
			t.Fatalf("initial Admit() error = %v", err)
		}

		order := make(chan string, 2)
		go func() {
			_, admitErr := l.Admit(context.Background(), Admission{Region: "europe", Bucket: bucket, Priority: PriorityNormal})
			if admitErr == nil {
				order <- "normal"
			}
		}()
		go func() {
			_, admitErr := l.Admit(context.Background(), Admission{Region: "europe", Bucket: bucket, Priority: PriorityHigh})
			if admitErr == nil {
				order <- "high"
			}
		}()

		synctest.Wait()

		time.Sleep(time.Second)
		synctest.Wait()

		first := <-order
		if first != "high" {
			t.Fatalf("first granted priority = %q, want high", first)
		}
	})
}

func TestLimiterBudgetPacing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l, err := New(Config{
			KeyCount:         1,
			QueueCapacity:    4,
			DefaultAppLimits: "10:10",
			RateBudgets: map[string]BudgetConfig{
				"worker": {Share: 0.8},
			},
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer func() { _ = l.Close() }()

		admission := Admission{
			Region:   "europe",
			Bucket:   "europe:riot/account/v1/accounts/me",
			BudgetID: "worker",
			Priority: PriorityNormal,
		}
		if _, err := l.Admit(context.Background(), admission); err != nil {
			t.Fatalf("first Admit() error = %v", err)
		}

		done := make(chan error, 1)
		go func() {
			_, admitErr := l.Admit(context.Background(), admission)
			done <- admitErr
		}()

		synctest.Wait()

		time.Sleep(1249 * time.Millisecond)
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("second Admit() finished too early with %v", err)
		default:
		}

		time.Sleep(time.Millisecond)
		synctest.Wait()
		if err := <-done; err != nil {
			t.Fatalf("second Admit() error = %v", err)
		}
	})
}

func TestLimiterBudgetFIFOInterleave(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 22, 12, 0, 0, 0, time.UTC)
	clock := &mutableClock{now: now}
	l := &Limiter{
		cfg: Config{
			AdditionalWindow: 0,
			Clock:            clock,
			RateBudgets: map[string]BudgetConfig{
				"worker": {Share: 0.8},
			},
		},
	}

	region := "europe"
	bucketName := "europe:riot/account/v1/accounts/me"
	keys := []keyState{newKeyState(parseRateHeader("10:10", ""))}
	if !keys[0].app(region, now, 0).consume(now, defaultBudgetID) {
		t.Fatal("initial consume() = false, want true")
	}

	defaultReq := &admitRequest{
		ctx:         context.Background(),
		admission:   Admission{Region: region, Bucket: bucketName, BudgetID: defaultBudgetID, Priority: PriorityNormal},
		budgetShare: 1,
		resp:        make(chan admitResponse, 1),
	}
	workerReq := &admitRequest{
		ctx:         context.Background(),
		admission:   Admission{Region: region, Bucket: bucketName, BudgetID: "worker", Priority: PriorityNormal},
		budgetShare: 0.8,
		resp:        make(chan admitResponse, 1),
	}
	bucket := &bucketQueue{
		region:    region,
		bucket:    bucketName,
		heapIndex: -1,
	}
	bucket.enqueue(defaultReq)
	bucket.enqueue(workerReq)

	wakeups := make(wakeHeap, 0)
	l.dispatch(bucket, keys, &wakeups)

	select {
	case out := <-workerReq.resp:
		t.Fatalf("worker admitted before earlier default request: %#v", out)
	default:
	}
	select {
	case out := <-defaultReq.resp:
		t.Fatalf("default admitted too early: %#v", out)
	default:
	}

	clock.now = now.Add(time.Second)
	l.dispatch(bucket, keys, &wakeups)

	if out := <-defaultReq.resp; out.err != nil {
		t.Fatalf("default response error = %v", out.err)
	}
	if out := <-workerReq.resp; out.err != nil {
		t.Fatalf("worker response error = %v", out.err)
	}
}

func TestLimiterRejectsUnknownBudget(t *testing.T) {
	t.Parallel()

	l, err := New(Config{
		KeyCount:         1,
		QueueCapacity:    1,
		DefaultAppLimits: "20:1",
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() { _ = l.Close() }()

	_, err = l.Admit(context.Background(), Admission{
		Region:   "europe",
		Bucket:   "europe:riot/account/v1/accounts/me",
		BudgetID: "worker",
		Priority: PriorityNormal,
	})
	var rejected *RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("Admit() error = %v, want RejectedError", err)
	}
	if got, want := rejected.Reason, "invalid_budget"; got != want {
		t.Fatalf("RejectedError.Reason = %q, want %q", got, want)
	}
}

func TestLimiterObserveRetryAfterBlocksUntilWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l, err := New(Config{
			KeyCount:         1,
			QueueCapacity:    2,
			DefaultAppLimits: "20:1",
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer func() { _ = l.Close() }()

		bucket := "europe:riot/account/v1/accounts/me"
		l.Observe(Observation{
			Region:     "europe",
			Bucket:     bucket,
			KeyIndex:   0,
			StatusCode: http.StatusTooManyRequests,
			Header: http.Header{
				"Retry-After":       []string{"2"},
				"X-Rate-Limit-Type": []string{"application"},
			},
		})

		synctest.Wait()

		done := make(chan error, 1)
		go func() {
			_, admitErr := l.Admit(context.Background(), Admission{Region: "europe", Bucket: bucket, Priority: PriorityNormal})
			done <- admitErr
		}()

		synctest.Wait()

		select {
		case err := <-done:
			t.Fatalf("Admit() finished too early with %v", err)
		default:
		}

		time.Sleep(2 * time.Second)
		synctest.Wait()

		if err := <-done; err != nil {
			t.Fatalf("Admit() after Retry-After error = %v", err)
		}
	})
}

func intPtr(v int) *int {
	return &v
}

type mutableClock struct {
	now time.Time
}

func (c *mutableClock) Now() time.Time {
	return c.now
}
