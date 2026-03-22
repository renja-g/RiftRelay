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
