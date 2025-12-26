/*
 * Copyright (C) 2025, Wei Chen
 *
 * This file is part of mosdns.
 *
 * mosdns is free software: you can redistribute it and/or modify
 * it under terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * mosdns is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <https://www.gnu.org/licenses/>.
 */

package qos

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrCircuitBreakerOpen = errors.New("circuit breaker is open")
)

type CircuitState int

const (
	StateClosed CircuitState = iota
	StateHalfOpen
	StateOpen
)

func (s CircuitState) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateHalfOpen:
		return "half-open"
	case StateOpen:
		return "open"
	default:
		return "unknown"
	}
}

type CircuitBreaker struct {
	mu sync.RWMutex

	maxFailures      int
	resetTimeout     time.Duration
	halfOpenAttempts int

	state           CircuitState
	failures        atomic.Int64
	successCount    atomic.Int64
	lastFailureTime atomic.Value
	halfOpenSuccess atomic.Int64

	onStateChange atomic.Pointer[func(CircuitState, CircuitState)]
}

type CircuitBreakerConfig struct {
	MaxFailures      int
	ResetTimeout     time.Duration
	HalfOpenAttempts int
}

func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	if cfg.MaxFailures <= 0 {
		cfg.MaxFailures = 10
	}
	if cfg.ResetTimeout <= 0 {
		cfg.ResetTimeout = 60 * time.Second
	}
	if cfg.HalfOpenAttempts <= 0 {
		cfg.HalfOpenAttempts = 3
	}

	return &CircuitBreaker{
		maxFailures:      cfg.MaxFailures,
		resetTimeout:     cfg.ResetTimeout,
		halfOpenAttempts: cfg.HalfOpenAttempts,
		state:            StateClosed,
	}
}

func (cb *CircuitBreaker) Execute(fn func() error) error {
	if cb.beforeExecute() {
		return ErrCircuitBreakerOpen
	}

	err := fn()
	cb.afterExecute(err != nil)
	return err
}

func (cb *CircuitBreaker) beforeExecute() bool {
	state := cb.State()

	if state == StateOpen {
		if cb.shouldAttemptReset() {
			cb.transitionTo(StateHalfOpen)
			return false
		}
		return true
	}

	return false
}

func (cb *CircuitBreaker) afterExecute(failed bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	state := cb.state

	if failed {
		cb.failures.Add(1)
		cb.recordFailure()
	} else {
		cb.successCount.Add(1)

		if state == StateHalfOpen {
			cb.halfOpenSuccess.Add(1)
			if cb.halfOpenSuccess.Load() >= int64(cb.halfOpenAttempts) {
				cb.transitionTo(StateClosed)
			}
		} else {
			cb.failures.Store(0)
		}
	}
}

func (cb *CircuitBreaker) recordFailure() {
	now := time.Now()
	cb.lastFailureTime.Store(&now)

	if cb.state == StateClosed && cb.failures.Load() >= int64(cb.maxFailures) {
		cb.transitionTo(StateOpen)
	} else if cb.state == StateHalfOpen {
		cb.transitionTo(StateOpen)
	}
}

func (cb *CircuitBreaker) shouldAttemptReset() bool {
	lastFailure := cb.lastFailureTime.Load()
	if lastFailure == nil {
		return false
	}

	last, ok := lastFailure.(time.Time)
	if !ok {
		return false
	}

	return time.Since(last) >= cb.resetTimeout
}

func (cb *CircuitBreaker) transitionTo(newState CircuitState) {
	oldState := cb.state
	cb.state = newState

	if newState == StateClosed {
		cb.failures.Store(0)
		cb.halfOpenSuccess.Store(0)
	} else if newState == StateOpen {
		cb.halfOpenSuccess.Store(0)
	}

	if fn := cb.onStateChange.Load(); fn != nil {
		(*fn)(oldState, newState)
	}
}

func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

func (cb *CircuitBreaker) Failures() int64 {
	return cb.failures.Load()
}

func (cb *CircuitBreaker) Successes() int64 {
	return cb.successCount.Load()
}

func (cb *CircuitBreaker) SetStateChangeCallback(fn func(CircuitState, CircuitState)) {
	cb.onStateChange.Store(&fn)
}

func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = StateClosed
	cb.failures.Store(0)
	cb.halfOpenSuccess.Store(0)
}

func (cb *CircuitBreaker) Stats() (state CircuitState, failures int64, successes int64) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state, cb.failures.Load(), cb.successCount.Load()
}
