/*
 * Copyright (C) 2025, Wei Chen
 *
 * This file is part of mosdns.
 *
 * mosdns is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
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

package adaptive_doh

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/harlanwei/mosdns-lts/v5/pkg/upstream/doh"
	"go.uber.org/zap"
)

const (
	defaultSampleSize      = 20
	defaultPreferenceRatio = 0.8
	defaultTrialCount      = 10
)

type protocol string

const (
	protocolDoH  protocol = "doh"
	protocolDoH3 protocol = "doh3"
)

type protocolStats struct {
	totalRequests   atomic.Uint64
	successRequests atomic.Uint64
	failedRequests  atomic.Uint64
	totalLatency    atomic.Int64
	preferredCount  atomic.Uint64
	fallbackCount   atomic.Uint64
}

type Upstream struct {
	doh  *doh.Upstream
	doh3 *doh.Upstream

	mu         sync.RWMutex
	current    protocol
	preferred  protocol
	stats      map[protocol]*protocolStats
	sampleSize int
	preference float64
	trialCount int
	trialDone  atomic.Bool
	addr       string
	logger     *zap.Logger
}

type Opt struct {
	SampleSize int
	Preference float64
	TrialCount int
	Addr       string
	Logger     *zap.Logger
}

func NewUpstream(dohUpstream, doh3Upstream *doh.Upstream, opt Opt) (*Upstream, error) {
	if dohUpstream == nil || doh3Upstream == nil {
		return nil, io.ErrClosedPipe
	}

	if opt.SampleSize <= 0 {
		opt.SampleSize = defaultSampleSize
	}
	if opt.Preference <= 0 || opt.Preference > 1 {
		opt.Preference = defaultPreferenceRatio
	}
	if opt.TrialCount <= 0 {
		opt.TrialCount = defaultTrialCount
	}
	if opt.Logger == nil {
		opt.Logger = zap.NewNop()
	}

	return &Upstream{
		doh:       dohUpstream,
		doh3:      doh3Upstream,
		current:   protocolDoH,
		preferred: protocolDoH,
		stats: map[protocol]*protocolStats{
			protocolDoH:  {},
			protocolDoH3: {},
		},
		sampleSize: opt.SampleSize,
		preference: opt.Preference,
		trialCount: opt.TrialCount,
		addr:       opt.Addr,
		logger:     opt.Logger.With(zap.String("upstream", opt.Addr)),
	}, nil
}

func (u *Upstream) ExchangeContext(ctx context.Context, q []byte) (*[]byte, error) {
	selectedProtocol := u.selectProtocol()

	u.logger.Debug("using protocol for query",
		zap.String("protocol", string(selectedProtocol)),
		zap.String("preferred", string(u.preferred)),
	)

	var r *[]byte
	var err error
	var latency time.Duration

	start := time.Now()
	if selectedProtocol == protocolDoH {
		r, err = u.doh.ExchangeContext(ctx, q)
	} else {
		r, err = u.doh3.ExchangeContext(ctx, q)
	}
	latency = time.Since(start)

	u.stats[selectedProtocol].totalRequests.Add(1)

	if err != nil {
		u.stats[selectedProtocol].failedRequests.Add(1)
		u.logger.Warn("query failed",
			zap.String("protocol", string(selectedProtocol)),
			zap.Duration("latency", latency),
			zap.Error(err),
		)
		u.recordFailure(selectedProtocol)
		return nil, err
	}

	u.stats[selectedProtocol].successRequests.Add(1)
	u.stats[selectedProtocol].totalLatency.Add(int64(latency.Milliseconds()))

	u.logger.Debug("query succeeded",
		zap.String("protocol", string(selectedProtocol)),
		zap.Duration("latency", latency),
	)

	if !u.trialDone.Load() {
		u.evaluatePreference()
	} else if selectedProtocol == u.preferred {
		u.stats[selectedProtocol].preferredCount.Add(1)
	} else {
		u.stats[selectedProtocol].fallbackCount.Add(1)
		u.logger.Debug("using fallback protocol",
			zap.String("fallback", string(selectedProtocol)),
			zap.String("preferred", string(u.preferred)),
		)
	}

	return r, nil
}

func (u *Upstream) selectProtocol() protocol {
	u.mu.RLock()
	defer u.mu.RUnlock()

	if !u.trialDone.Load() {
		if u.current == protocolDoH {
			u.current = protocolDoH3
		} else {
			u.current = protocolDoH
		}
		return u.current
	}

	stats := u.stats[u.preferred]
	totalRequests := stats.totalRequests.Load()
	if totalRequests == 0 {
		return u.preferred
	}

	doHStats := u.stats[protocolDoH]
	doH3Stats := u.stats[protocolDoH3]

	doH3Available := doH3Stats.totalRequests.Load() > 0 &&
		float64(doH3Stats.failedRequests.Load())/float64(doH3Stats.totalRequests.Load()) < 0.5

	if u.preferred == protocolDoH3 && doH3Available {
		doH3FasterCount := doH3Stats.preferredCount.Load()
		doHFallbackCount := doHStats.fallbackCount.Load()
		total := doH3FasterCount + doHFallbackCount
		if total > 0 && float64(doH3FasterCount)/float64(total) >= u.preference {
			return protocolDoH3
		}
	}

	return u.preferred
}

func (u *Upstream) recordFailure(p protocol) {
	if p == u.preferred {
		stats := u.stats[getOtherProtocol(p)]
		if stats.totalRequests.Load() > 0 {
			otherSuccessRate := float64(stats.successRequests.Load()) / float64(stats.totalRequests.Load())
			currentSuccessRate := float64(u.stats[p].successRequests.Load()) / float64(u.stats[p].totalRequests.Load())
			u.logger.Debug("comparing protocol success rates",
				zap.String("protocol", string(p)),
				zap.Float64("current_success_rate", currentSuccessRate),
				zap.String("other_protocol", string(getOtherProtocol(p))),
				zap.Float64("other_success_rate", otherSuccessRate),
			)
			if otherSuccessRate > currentSuccessRate {
				u.mu.Lock()
				u.preferred = getOtherProtocol(p)
				u.mu.Unlock()
				u.logger.Warn("switching preferred protocol due to failures",
					zap.String("from", string(p)),
					zap.String("to", string(u.preferred)),
					zap.Float64("old_success_rate", currentSuccessRate),
					zap.Float64("new_success_rate", otherSuccessRate),
					zap.Uint64("old_failed", u.stats[p].failedRequests.Load()),
					zap.Uint64("old_total", u.stats[p].totalRequests.Load()),
					zap.Uint64("new_failed", stats.failedRequests.Load()),
					zap.Uint64("new_total", stats.totalRequests.Load()),
				)
			}
		}
	}
}

func (u *Upstream) evaluatePreference() {
	u.mu.Lock()
	defer u.mu.Unlock()

	doHStats := u.stats[protocolDoH]
	doH3Stats := u.stats[protocolDoH3]

	totalSamples := doHStats.totalRequests.Load() + doH3Stats.totalRequests.Load()
	if totalSamples < uint64(u.trialCount) {
		return
	}

	u.trialDone.Store(true)

	if doH3Stats.totalRequests.Load() == 0 {
		u.preferred = protocolDoH
		u.logger.Info("DoH3 not available, using DoH")
		return
	}

	doH3FailureRate := float64(doH3Stats.failedRequests.Load()) / float64(doH3Stats.totalRequests.Load())
	if doH3FailureRate >= 0.5 {
		u.preferred = protocolDoH
		u.logger.Info("DoH3 failure rate too high, using DoH",
			zap.Float64("failure_rate", doH3FailureRate),
			zap.Uint64("doh3_failed", doH3Stats.failedRequests.Load()),
			zap.Uint64("doh3_total", doH3Stats.totalRequests.Load()),
		)
		return
	}

	if doHStats.totalRequests.Load() == 0 {
		u.preferred = protocolDoH3
		u.logger.Info("only DoH3 available")
		return
	}

	doHAvgLatency := float64(doHStats.totalLatency.Load()) / float64(doHStats.successRequests.Load())
	doH3AvgLatency := float64(doH3Stats.totalLatency.Load()) / float64(doH3Stats.successRequests.Load())

	u.logger.Info("protocol evaluation",
		zap.Float64("doh_latency", doHAvgLatency),
		zap.Uint64("doh_success", doHStats.successRequests.Load()),
		zap.Uint64("doh_failed", doHStats.failedRequests.Load()),
		zap.Float64("doh3_latency", doH3AvgLatency),
		zap.Uint64("doh3_success", doH3Stats.successRequests.Load()),
		zap.Uint64("doh3_failed", doH3Stats.failedRequests.Load()),
		zap.Float64("preference_threshold", u.preference),
	)

	if doH3AvgLatency < doHAvgLatency*u.preference {
		u.preferred = protocolDoH3
		u.logger.Info("switched preferred protocol to DoH3 (faster)",
			zap.Float64("doh_latency", doHAvgLatency),
			zap.Float64("doh3_latency", doH3AvgLatency),
			zap.Float64("improvement", (doHAvgLatency-doH3AvgLatency)/doHAvgLatency*100),
		)
	} else {
		u.preferred = protocolDoH
		u.logger.Info("kept preferred protocol as DoH",
			zap.Float64("doh_latency", doHAvgLatency),
			zap.Float64("doh3_latency", doH3AvgLatency),
			zap.String("reason", "DoH3 not sufficiently faster"),
		)
	}
}

func getOtherProtocol(p protocol) protocol {
	if p == protocolDoH {
		return protocolDoH3
	}
	return protocolDoH
}

func (u *Upstream) Close() error {
	return nil
}

func (u *Upstream) GetStats() map[protocol]*protocolStats {
	return u.stats
}

func (u *Upstream) GetCurrentProtocol() protocol {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.current
}

func (u *Upstream) GetPreferredProtocol() protocol {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.preferred
}

func CreateAdaptiveUpstream(addr string, dohRT, doh3RT http.RoundTripper, opt Opt) (*Upstream, error) {
	dohUpstream, err := doh.NewUpstream(addr, dohRT, opt.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create doh upstream: %w", err)
	}

	doh3Upstream, err := doh.NewUpstream(addr, doh3RT, opt.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create doh3 upstream: %w", err)
	}

	opt.Addr = addr
	return NewUpstream(dohUpstream, doh3Upstream, opt)
}
