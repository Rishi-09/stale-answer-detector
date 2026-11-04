package freshness

import (
	"testing"
	"time"
)

func TestCircuitBreaker_StateTransitions(t *testing.T) {
	// Breaker with 2 failures threshold, 100ms cooldown
	cb := &CircuitBreaker{
		state:         StateClosed,
		failureLimit:  2,
		cooldown:      100 * time.Millisecond,
		lastStateTime: time.Now(),
	}

	// 1. Initial Closed state
	if err := cb.Allow(); err != nil {
		t.Fatalf("expected allowed in initial Closed state: %v", err)
	}

	// 2. First failure: remains Closed
	cb.RecordFailure()
	if cb.State() != StateClosed {
		t.Errorf("expected Closed after 1 failure, got %s", cb.State())
	}

	// 3. Second failure: trips to Open
	cb.RecordFailure()
	if cb.State() != StateOpen {
		t.Errorf("expected Open after 2 failures, got %s", cb.State())
	}

	// 4. Immediately blocked
	if err := cb.Allow(); err != ErrCircuitOpen {
		t.Errorf("expected ErrCircuitOpen, got %v", err)
	}

	// 5. Wait for cooldown to expire
	time.Sleep(120 * time.Millisecond)

	// 6. Next allow transitions to HalfOpen and permits probe
	if err := cb.Allow(); err != nil {
		t.Fatalf("expected probe allowed after cooldown: %v", err)
	}
	if cb.State() != StateHalfOpen {
		t.Errorf("expected HalfOpen state during probe, got %s", cb.State())
	}

	// 7. Probe succeeds: resets to Closed
	cb.RecordSuccess()
	if cb.State() != StateClosed {
		t.Errorf("expected Closed after probe success, got %s", cb.State())
	}
}

