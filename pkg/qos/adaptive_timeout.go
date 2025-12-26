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
	"math"
	"sync"
	"sync/atomic"
	"time"
)

type AdaptiveTimeout struct {
	mu sync.RWMutex

	baseTimeout    time.Duration
	minTimeout     time.Duration
	maxTimeout     time.Duration
	congestionMult float64

	srtt                time.Duration
	rttVar              time.Duration
	samples             atomic.Int64
	consecutiveTimeouts atomic.Int64
}

type TimeoutConfig struct {
	BaseTimeout    time.Duration
	MinTimeout     time.Duration
	MaxTimeout     time.Duration
	CongestionMult float64
}

func NewAdaptiveTimeout(cfg TimeoutConfig) *AdaptiveTimeout {
	if cfg.BaseTimeout <= 0 {
		cfg.BaseTimeout = 2 * time.Second
	}
	if cfg.MinTimeout <= 0 {
		cfg.MinTimeout = 500 * time.Millisecond
	}
	if cfg.MaxTimeout <= 0 {
		cfg.MaxTimeout = 30 * time.Second
	}
	if cfg.CongestionMult <= 1.0 {
		cfg.CongestionMult = 4.0
	}

	return &AdaptiveTimeout{
		baseTimeout:    cfg.BaseTimeout,
		minTimeout:     cfg.MinTimeout,
		maxTimeout:     cfg.MaxTimeout,
		congestionMult: cfg.CongestionMult,
		srtt:           cfg.BaseTimeout,
		rttVar:         cfg.BaseTimeout / 2,
	}
}

func (a *AdaptiveTimeout) RecordSuccess(duration time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.consecutiveTimeouts.Store(0)

	if a.samples.Load() == 0 {
		a.srtt = duration
		a.rttVar = duration / 2
	} else {
		alpha := 0.125
		beta := 0.25

		a.srtt = time.Duration(float64(alpha)*float64(duration) + float64(1-alpha)*float64(a.srtt))

		deviation := duration - a.srtt
		if deviation < 0 {
			deviation = -deviation
		}
		a.rttVar = time.Duration(float64(beta)*float64(deviation) + float64(1-beta)*float64(a.rttVar))
	}

	a.samples.Add(1)
}

func (a *AdaptiveTimeout) RecordTimeout() {
	a.mu.Lock()
	defer a.mu.Unlock()

	count := a.consecutiveTimeouts.Add(1)

	if count >= 3 {
		multiplier := math.Min(a.congestionMult, 1.0+float64(count)*0.5)
		a.srtt = time.Duration(float64(a.srtt) * multiplier)
	}
}

func (a *AdaptiveTimeout) GetTimeout() time.Duration {
	a.mu.RLock()
	defer a.mu.RUnlock()

	timeout := a.srtt + 4*a.rttVar

	if timeout < a.minTimeout {
		return a.minTimeout
	}
	if timeout > a.maxTimeout {
		return a.maxTimeout
	}

	return timeout
}

func (a *AdaptiveTimeout) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.srtt = a.baseTimeout
	a.rttVar = a.baseTimeout / 2
	a.samples.Store(0)
	a.consecutiveTimeouts.Store(0)
}

func (a *AdaptiveTimeout) GetStats() (srtt time.Duration, rttVar time.Duration, samples int64, timeouts int64) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.srtt, a.rttVar, a.samples.Load(), a.consecutiveTimeouts.Load()
}
