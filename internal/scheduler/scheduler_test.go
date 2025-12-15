package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/renja-g/rp/internal/ratelimit"
)

func TestPerKeyScheduler_PriorityPreemptsNormal(t *testing.T) {
	state := ratelimit.NewState([]ratelimit.Bucket{{Limit: 1, Window: 30 * time.Millisecond}})

	// Consume the first slot to force scheduling in the future.
	state.Reserve(time.Now(), true)

	sched := newPerKeyScheduler(state)

	normal := &requestPermit{
		ctx:      context.Background(),
		priority: false,
		res:      make(chan error, 1),
	}
	priority := &requestPermit{
		ctx:      context.Background(),
		priority: true,
		res:      make(chan error, 1),
	}

	sched.incoming <- normal
	time.Sleep(5 * time.Millisecond) // allow normal to become current
	sched.incoming <- priority

	var normalErr, priorityErr error
	select {
	case priorityErr = <-priority.res:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("priority request did not complete")
	}
	select {
	case normalErr = <-normal.res:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("normal request did not complete")
	}

	if priorityErr != nil {
		t.Fatalf("priority request error = %v, want nil", priorityErr)
	}
	if normalErr != nil {
		t.Fatalf("normal request error = %v, want nil", normalErr)
	}
}
