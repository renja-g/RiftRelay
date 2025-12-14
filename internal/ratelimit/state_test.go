package ratelimit

import (
	"net/http"
	"testing"
	"time"
)

func TestParseAndMergeBuckets(t *testing.T) {
	h := http.Header{}
	h.Set("X-Method-Rate-Limit", "2:1,10:120")
	h.Set("X-App-Rate-Limit", "5:1,20:120")

	got := UpdateBucketsFromHeaders(h)
	if len(got) != 2 {
		t.Fatalf("UpdateBucketsFromHeaders() len = %d, want 2", len(got))
	}

	if got[0].Window != 1*time.Second || got[0].Limit != 2 {
		t.Errorf("first bucket = %+v, want limit 2 window 1s", got[0])
	}
}

func TestStateReserveRespectsCapacity(t *testing.T) {
	now := time.Unix(0, 0)
	state := NewState([]Bucket{{Limit: 1, Window: time.Second}})

	when1, cancel1 := state.Reserve(now, true)
	defer cancel1()
	if !when1.Equal(now) {
		t.Fatalf("first reserve when = %v, want %v", when1, now)
	}

	when2, _ := state.Reserve(now, true)
	if when2 != now.Add(time.Second) {
		t.Fatalf("second reserve when = %v, want %v", when2, now.Add(time.Second))
	}
}

func TestStateReserveSpreadsNormal(t *testing.T) {
	now := time.Unix(0, 0)
	state := NewState([]Bucket{{Limit: 2, Window: time.Second}})

	when1, _ := state.Reserve(now, false)
	if when1 != now {
		t.Fatalf("first normal reserve when = %v, want %v", when1, now)
	}

	when2, _ := state.Reserve(now, false)
	if when2.Sub(now) < time.Second {
		t.Fatalf("second normal reserve too soon: %v", when2.Sub(now))
	}
}

func TestStateReserveSpreadsAfterBurst(t *testing.T) {
	now := time.Unix(0, 0)
	// Limit 20 over 100s to simulate remaining budget spreading.
	state := NewState([]Bucket{{Limit: 20, Window: 100 * time.Second}})

	// Simulate 10 immediate priority requests already consumed at now.
	for i := 0; i < 10; i++ {
		state.Reserve(now, true)
	}

	// Next normal should schedule at 100s/20 = 5s spacing but adjusted
	// to remaining window: 10 tokens left over nearly full 100s window.
	when, _ := state.Reserve(now, false)
	if when.Sub(now) < 9*time.Second {
		t.Fatalf("normal reserve too soon: %v", when.Sub(now))
	}
}

func TestStateUpdateFromHeadersSeedsCounts(t *testing.T) {
	now := time.Now()
	h := http.Header{}
	h.Set("X-Method-Rate-Limit", "100:120")
	h.Set("X-Method-Rate-Limit-Count", "80:120")

	state := NewState(nil)
	state.UpdateFromHeaders(h)

	when, _ := state.Reserve(now, false)
	// 20 remaining over ~120s => expect ~6s spacing minimum.
	if when.Sub(now) < 5*time.Second {
		t.Fatalf("reserve after seeded count too soon: %v", when.Sub(now))
	}
}

func TestStateCancelFreesSlot(t *testing.T) {
	now := time.Unix(0, 0)
	state := NewState([]Bucket{{Limit: 1, Window: time.Second}})

	when1, cancel := state.Reserve(now, true)
	if when1 != now {
		t.Fatalf("reserve when = %v, want now", when1)
	}
	cancel()

	when2, _ := state.Reserve(now, true)
	if when2 != now {
		t.Fatalf("after cancel reserve when = %v, want now", when2)
	}
}
