package subprocess

import (
	"fmt"
	"sync"
	"time"
)

// Supervisor monitors extension process crashes and implements exponential
// backoff with a circuit breaker. After too many crashes in a window, the
// extension is disabled until manually re-enabled via /reload.
//
// pig-specific: no upstream equivalent.
type Supervisor struct {
	mu sync.Mutex

	// Config
	maxCrashes    int           // Circuit breaker threshold
	crashWindow   time.Duration // Window in which maxCrashes triggers disable
	initialDelay  time.Duration // First restart delay
	maxDelay      time.Duration // Cap on exponential backoff
	backoffFactor float64       // Multiplier per crash

	// State
	crashes     []time.Time // Timestamps of recent crashes
	consecutive int         // Consecutive crash count for backoff
	disabled    bool        // Circuit breaker tripped
	disableMsg  string      // Reason for disable
}

// SupervisorConfig configures crash recovery behavior.
type SupervisorConfig struct {
	MaxCrashes    int           // Default: 5
	CrashWindow   time.Duration // Default: 60s
	InitialDelay  time.Duration // Default: 2s
	MaxDelay      time.Duration // Default: 30s
	BackoffFactor float64       // Default: 2.0
}

// DefaultSupervisorConfig returns production defaults.
// pig divergence (D56): crash-restart window and backoff supervise the
// subprocess boundary; every restart and disable is reported through onCrash.
func DefaultSupervisorConfig() SupervisorConfig {
	return SupervisorConfig{
		MaxCrashes:    5,
		CrashWindow:   60 * time.Second,
		InitialDelay:  2 * time.Second,
		MaxDelay:      30 * time.Second,
		BackoffFactor: 2.0,
	}
}

// NewSupervisor creates a crash supervisor with the given config.
func NewSupervisor(cfg SupervisorConfig) *Supervisor {
	if cfg.MaxCrashes <= 0 {
		cfg.MaxCrashes = 5
	}
	if cfg.CrashWindow <= 0 {
		cfg.CrashWindow = 60 * time.Second
	}
	if cfg.InitialDelay <= 0 {
		cfg.InitialDelay = 2 * time.Second
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 30 * time.Second
	}
	if cfg.BackoffFactor <= 0 {
		cfg.BackoffFactor = 2.0
	}

	return &Supervisor{
		maxCrashes:    cfg.MaxCrashes,
		crashWindow:   cfg.CrashWindow,
		initialDelay:  cfg.InitialDelay,
		maxDelay:      cfg.MaxDelay,
		backoffFactor: cfg.BackoffFactor,
	}
}

// RecordCrash records a crash and returns the restart delay, or an error if
// the circuit breaker has tripped.
func (s *Supervisor) RecordCrash() (time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.disabled {
		return 0, fmt.Errorf("extension disabled: %s", s.disableMsg)
	}

	now := time.Now()
	s.crashes = append(s.crashes, now)
	s.consecutive++

	// Prune crashes outside the window.
	cutoff := now.Add(-s.crashWindow)
	pruned := s.crashes[:0]
	for _, t := range s.crashes {
		if t.After(cutoff) {
			pruned = append(pruned, t)
		}
	}
	s.crashes = pruned

	// Check circuit breaker.
	if len(s.crashes) >= s.maxCrashes {
		s.disabled = true
		s.disableMsg = fmt.Sprintf(
			"%d crashes in %s: circuit breaker tripped",
			len(s.crashes), s.crashWindow)
		return 0, fmt.Errorf("extension disabled: %s", s.disableMsg)
	}

	// Calculate exponential backoff.
	delay := s.initialDelay
	for range s.consecutive - 1 {
		delay = time.Duration(float64(delay) * s.backoffFactor)
		if delay > s.maxDelay {
			delay = s.maxDelay
			break
		}
	}

	return delay, nil
}

// RecordSuccess resets the consecutive crash counter (process started and
// registered successfully).
func (s *Supervisor) RecordSuccess() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consecutive = 0
}

// Reset clears all crash history and re-enables the extension. Used by /reload.
func (s *Supervisor) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.crashes = nil
	s.consecutive = 0
	s.disabled = false
	s.disableMsg = ""
}

// IsDisabled returns whether the circuit breaker has tripped.
func (s *Supervisor) IsDisabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disabled
}

// DisableReason returns the circuit breaker message, or empty if not disabled.
func (s *Supervisor) DisableReason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.disableMsg
}
