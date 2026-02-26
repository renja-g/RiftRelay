package limiter

import (
	"testing"
	"time"
)

func TestRateStateNextAllowed(t *testing.T) {
	now := time.Date(2026, 2, 26, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name         string
		state        rateState
		bypassPacing bool
		want         time.Time
	}{
		{
			name:  "no windows allows immediately",
			state: rateState{},
			bypassPacing: false,
			want:  now,
		},
		{
			name: "blocked until overrides immediate access",
			state: rateState{
				blockedUntil: now.Add(2 * time.Second),
			},
			bypassPacing: false,
			want: now.Add(2 * time.Second),
		},
		{
			name: "window exhaustion waits for reset",
			state: rateState{
				windows: []limitWindow{
					{
						limit:   2,
						used:    2,
						window:  5 * time.Second,
						resetAt: now.Add(5 * time.Second),
					},
				},
			},
			bypassPacing: false,
			want: now.Add(5 * time.Second),
		},
		{
			name: "pacing spreads requests inside window",
			state: rateState{
				lastGranted: now,
				windows: []limitWindow{
					{
						limit:   5,
						used:    1,
						window:  4 * time.Second,
						resetAt: now.Add(4 * time.Second),
					},
				},
			},
			bypassPacing: false,
			want: now.Add(1 * time.Second),
		},
		{
			name: "bypass pacing allows immediate execution when window has budget",
			state: rateState{
				lastGranted: now,
				windows: []limitWindow{
					{
						limit:   5,
						used:    1,
						window:  4 * time.Second,
						resetAt: now.Add(4 * time.Second),
					},
				},
			},
			bypassPacing: true,
			want:         now,
		},
		{
			name: "expired windows refresh and allow immediately",
			state: rateState{
				windows: []limitWindow{
					{
						limit:   4,
						used:    4,
						window:  2 * time.Second,
						resetAt: now.Add(-100 * time.Millisecond),
					},
				},
			},
			bypassPacing: false,
			want: now,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := tt.state.nextAllowed(now, tt.bypassPacing)
			if !got.Equal(tt.want) {
				t.Fatalf("expected %s, got %s", tt.want, got)
			}
		})
	}
}

func TestRateStateConsume(t *testing.T) {
	now := time.Date(2026, 2, 26, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		state     rateState
		wantOK    bool
		wantUsed  int
		wantGrant bool
	}{
		{
			name: "blocked state denies consume",
			state: rateState{
				blockedUntil: now.Add(2 * time.Second),
				windows: []limitWindow{
					{limit: 5, used: 0, window: 5 * time.Second, resetAt: now.Add(5 * time.Second)},
				},
			},
			wantOK:    false,
			wantUsed:  0,
			wantGrant: false,
		},
		{
			name: "exhausted window denies consume",
			state: rateState{
				windows: []limitWindow{
					{limit: 1, used: 1, window: 5 * time.Second, resetAt: now.Add(5 * time.Second)},
				},
			},
			wantOK:    false,
			wantUsed:  1,
			wantGrant: false,
		},
		{
			name: "valid consume increments counters and records grant",
			state: rateState{
				windows: []limitWindow{
					{limit: 5, used: 2, window: 5 * time.Second, resetAt: now.Add(5 * time.Second)},
				},
			},
			wantOK:    true,
			wantUsed:  3,
			wantGrant: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ok := tt.state.consume(now)
			if ok != tt.wantOK {
				t.Fatalf("expected consume=%v, got %v", tt.wantOK, ok)
			}

			if len(tt.state.windows) > 0 && tt.state.windows[0].used != tt.wantUsed {
				t.Fatalf("expected used=%d, got %d", tt.wantUsed, tt.state.windows[0].used)
			}

			gotGranted := !tt.state.lastGranted.IsZero()
			if gotGranted != tt.wantGrant {
				t.Fatalf("expected lastGranted set=%v, got %v", tt.wantGrant, gotGranted)
			}
		})
	}
}

func TestRateStateApplyWindowAnchoring(t *testing.T) {
	start := time.Date(2026, 2, 26, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name             string
		initialNow       time.Time
		updateNow        time.Time
		wantInitialReset time.Time
		wantUpdateReset  time.Time
	}{
		{
			name:             "keeps reset anchored during same upstream window",
			initialNow:       start,
			updateNow:        start.Add(100 * time.Millisecond),
			wantInitialReset: start.Add(1 * time.Second),
			wantUpdateReset:  start.Add(1 * time.Second),
		},
		{
			name:             "reanchors reset after upstream window elapsed",
			initialNow:       start,
			updateNow:        start.Add(1100 * time.Millisecond),
			wantInitialReset: start.Add(1 * time.Second),
			wantUpdateReset:  start.Add(2100 * time.Millisecond),
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			var s rateState

			s.apply([]parsedWindow{{limit: 20, count: 1, window: time.Second}}, nil, false, tt.initialNow, 0)
			if len(s.windows) != 1 {
				t.Fatalf("expected one window after initial apply, got %d", len(s.windows))
			}
			if got := s.windows[0].resetAt; !got.Equal(tt.wantInitialReset) {
				t.Fatalf("unexpected initial resetAt: want=%s got=%s", tt.wantInitialReset, got)
			}

			s.apply([]parsedWindow{{limit: 20, count: 2, window: time.Second}}, nil, false, tt.updateNow, 0)
			if len(s.windows) != 1 {
				t.Fatalf("expected one window after update apply, got %d", len(s.windows))
			}
			if got := s.windows[0].resetAt; !got.Equal(tt.wantUpdateReset) {
				t.Fatalf("unexpected updated resetAt: want=%s got=%s", tt.wantUpdateReset, got)
			}
		})
	}
}
