package freshness

import (
	"errors"
	"sync"
	"time"
)

// Circuit states
const (
	StateClosed   = "closed"
	StateOpen     = "open"
	StateHalfOpen = "half_open"
)

// ErrCircuitOpen is returned when the circuit breaker rejects outbound requests.
var ErrCircuitOpen = errors.New("circuit breaker is open; service temporarily unavailable")

// CircuitBreaker guards against cascading failures when external APIs fail repeatedly.
type CircuitBreaker struct {
	mu            sync.RWMutex
	state         string
	failureCount  int
	failureLimit  int
	cooldown      time.Duration
	lastStateTime time.Time
}

// NewCircuitBreaker creates a circuit breaker.
func NewCircuitBreaker(failureThreshold int, cooldownSeconds int) *CircuitBreaker {
	if failureThreshold <= 0 {
		failureThreshold = 5
	}
	if cooldownSeconds <= 0 {
		cooldownSeconds = 60
	}

	return &CircuitBreaker{
		state:         StateClosed,
		failureLimit:  failureThreshold,
		cooldown:      time.Duration(cooldownSeconds) * time.Second,
		lastStateTime: time.Now(),
	}
}

// Allow checks if an execution is permitted. Returns nil if allowed, ErrCircuitOpen if blocked.
func (cb *CircuitBreaker) Allow() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()

	switch cb.state {
	case StateClosed:
		return nil

	case StateOpen:
		// Check if cooldown has elapsed to allow a probe
		if now.Sub(cb.lastStateTime) >= cb.cooldown {
			cb.state = StateHalfOpen
			cb.lastStateTime = now
			return nil
		}
		return ErrCircuitOpen

	case StateHalfOpen:
		// In half-open state, allow probe
		return nil

	default:
		return nil
	}
}

// RecordSuccess reports that a request completed successfully.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount = 0
	cb.state = StateClosed
	cb.lastStateTime = time.Now()
}

// RecordFailure reports that a request failed.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failureCount++
	now := time.Now()

	if cb.state == StateHalfOpen || cb.failureCount >= cb.failureLimit {
		cb.state = StateOpen
		cb.lastStateTime = now
	}
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() string {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Reset restores the breaker to Closed state.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = StateClosed
	cb.failureCount = 0
	cb.lastStateTime = time.Now()
}

