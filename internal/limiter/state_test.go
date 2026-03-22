package limiter

import (
	"testing"
	"time"
)

func TestRateStateNextAllowed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 22, 12, 0, 0, 0, time.UTC)

	t.Run("respects blocked until", func(t *testing.T) {
		t.Parallel()

		state := rateState{blockedUntil: now.Add(2 * time.Second)}
		if got, want := state.nextAllowed(now, false), now.Add(2*time.Second); !got.Equal(want) {
			t.Fatalf("nextAllowed() = %v, want %v", got, want)
		}
	})

	t.Run("paces when last grant exists", func(t *testing.T) {
		t.Parallel()

		state := rateState{
			lastGranted: now,
			windows: []limitWindow{
				{limit: 4, used: 1, window: 4 * time.Second, resetAt: now.Add(4 * time.Second)},
			},
		}
		if got, want := state.nextAllowed(now, false), now.Add(time.Second); !got.Equal(want) {
			t.Fatalf("nextAllowed() = %v, want %v", got, want)
		}
		if got, want := state.nextAllowed(now, true), now; !got.Equal(want) {
			t.Fatalf("nextAllowed() with bypass = %v, want %v", got, want)
		}
	})
}

func TestRateStateConsumeAndApply(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 22, 12, 0, 0, 0, time.UTC)

	t.Run("consume increments all windows until exhausted", func(t *testing.T) {
		t.Parallel()

		state := rateState{
			windows: []limitWindow{
				{limit: 2, window: time.Second, resetAt: now.Add(time.Second)},
				{limit: 2, window: 2 * time.Second, resetAt: now.Add(2 * time.Second)},
			},
		}
		if !state.consume(now) {
			t.Fatal("first consume() = false, want true")
		}
		if !state.consume(now) {
			t.Fatal("second consume() = false, want true")
		}
		if state.consume(now) {
			t.Fatal("third consume() = true, want false")
		}
		if got, want := state.windows[0].used, 2; got != want {
			t.Fatalf("used = %d, want %d", got, want)
		}
	})

	t.Run("apply sets windows and blocked until", func(t *testing.T) {
		t.Parallel()

		state := rateState{}
		retryAfter := now.Add(5 * time.Second)
		state.apply(
			[]parsedWindow{{limit: 10, count: 3, window: 2 * time.Second}},
			&retryAfter,
			true,
			now,
			150*time.Millisecond,
		)

		if got, want := len(state.windows), 1; got != want {
			t.Fatalf("len(windows) = %d, want %d", got, want)
		}
		if got, want := state.windows[0].window, 2150*time.Millisecond; got != want {
			t.Fatalf("window = %v, want %v", got, want)
		}
		if got, want := state.windows[0].used, 3; got != want {
			t.Fatalf("used = %d, want %d", got, want)
		}
		if !state.blockedUntil.Equal(retryAfter) {
			t.Fatalf("blockedUntil = %v, want %v", state.blockedUntil, retryAfter)
		}
		if state.lastGranted.IsZero() {
			t.Fatal("lastGranted = zero, want anchored timestamp")
		}
	})
}
