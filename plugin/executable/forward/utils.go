/*
 * Copyright (C) 2020-2022, IrineSistiana
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

package fastforward

import (
	"context"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/harlanwei/mosdns-lts/v5/pkg/pool"
	"github.com/harlanwei/mosdns-lts/v5/pkg/upstream"
	"github.com/miekg/dns"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap/zapcore"
)

const (
	weightCacheTTL   = time.Second * 5
	noiseFactor      = 0.125
	errorPenaltyMult = 8.0
	defaultLatency   = 10.0
)

type upstreamScore struct {
	idx   int
	score float64
}

type upstreamSelector struct {
	us []*upstreamWrapper

	mu          sync.RWMutex
	cachedOrder []int
	lastUpdate  time.Time
}

func newUpstreamSelector(us []*upstreamWrapper) *upstreamSelector {
	return &upstreamSelector{
		us: us,
	}
}

func (s *upstreamSelector) selectUpstreams(count int) []int {
	if len(s.us) <= count {
		indices := make([]int, len(s.us))
		for i := range indices {
			indices[i] = i
		}
		return indices
	}

	s.mu.RLock()
	if s.cachedOrder != nil && time.Since(s.lastUpdate) < weightCacheTTL {
		order := make([]int, count)
		copy(order, s.cachedOrder[:count])
		s.mu.RUnlock()
		return order
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cachedOrder != nil && time.Since(s.lastUpdate) < weightCacheTTL {
		order := make([]int, count)
		copy(order, s.cachedOrder[:count])
		return order
	}

	scores := s.calculateScores()

	totalWeight := 0.0
	for _, sc := range scores {
		totalWeight += sc.score
	}

	selected := make([]int, 0, count)
	used := make(map[int]bool)

	for len(selected) < count {
		r := rand.Float64() * totalWeight
		cumulative := 0.0

		for _, sc := range scores {
			if used[sc.idx] {
				continue
			}
			cumulative += sc.score
			if r <= cumulative {
				selected = append(selected, sc.idx)
				used[sc.idx] = true
				totalWeight -= sc.score
				break
			}
		}
	}

	s.cachedOrder = selected
	s.lastUpdate = time.Now()

	result := make([]int, count)
	copy(result, selected)
	return result
}

func (s *upstreamSelector) calculateScores() []upstreamScore {
	scores := make([]upstreamScore, len(s.us))

	for i, uw := range s.us {
		latency := float64(uw.getEmaLatency())
		if latency == 0 {
			latency = defaultLatency
		}

		queryTotal := uw.queryCount.Load()
		errorTotal := uw.errorCount.Load()

		var errorRate float64
		if queryTotal > 0 {
			errorRate = float64(errorTotal) / float64(queryTotal)
		}

		noise := (rand.Float64()*2 - 1) * noiseFactor
		penaltyFactor := 1.0 + errorRate*errorPenaltyMult
		score := (1.0 / (latency * penaltyFactor)) * (1 + noise)

		scores[i] = upstreamScore{
			idx:   i,
			score: score,
		}
	}

	return scores
}

type upstreamWrapper struct {
	idx             int
	u               upstream.Upstream
	cfg             UpstreamConfig
	queryTotal      prometheus.Counter
	errTotal        prometheus.Counter
	thread          prometheus.Gauge
	responseLatency prometheus.Histogram

	connOpened prometheus.Counter
	connClosed prometheus.Counter
	usedTotal  prometheus.Counter

	emaLatency atomic.Int64
	queryCount atomic.Int64
	errorCount atomic.Int64
}

func (uw *upstreamWrapper) OnEvent(typ upstream.Event) {
	switch typ {
	case upstream.EventConnOpen:
		uw.connOpened.Inc()
	case upstream.EventConnClose:
		uw.connClosed.Inc()
	}
}

// newWrapper inits all metrics.
// Note: upstreamWrapper.u still needs to be set.
func newWrapper(idx int, cfg UpstreamConfig, pluginTag string) *upstreamWrapper {
	lb := map[string]string{"upstream": cfg.Tag, "tag": pluginTag}
	return &upstreamWrapper{
		cfg: cfg,
		queryTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "query_total",
			Help:        "The total number of queries processed by this upstream",
			ConstLabels: lb,
		}),
		errTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "err_total",
			Help:        "The total number of queries failed",
			ConstLabels: lb,
		}),
		thread: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "thread",
			Help:        "The number of threads (queries) that are currently being processed",
			ConstLabels: lb,
		}),
		responseLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:        "response_latency_millisecond",
			Help:        "The response latency in millisecond",
			Buckets:     []float64{1, 5, 10, 20, 50, 100, 200, 500, 1000, 2000, 5000},
			ConstLabels: lb,
		}),

		connOpened: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "conn_opened_total",
			Help:        "The total number of connections that are opened",
			ConstLabels: lb,
		}),
		connClosed: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "conn_closed_total",
			Help:        "The total number of connections that are closed",
			ConstLabels: lb,
		}),
		usedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "used_total",
			Help:        "The total number of queries where this upstream's response was used",
			ConstLabels: lb,
		}),
	}
}

func (uw *upstreamWrapper) registerMetricsTo(r prometheus.Registerer) error {
	for _, collector := range [...]prometheus.Collector{
		uw.queryTotal,
		uw.errTotal,
		uw.thread,
		uw.responseLatency,
		uw.connOpened,
		uw.connClosed,
		uw.usedTotal,
	} {
		if err := r.Register(collector); err != nil {
			return err
		}
	}
	return nil
}

// name returns upstream tag if it was set in the config.
// Otherwise, it returns upstream address.
func (uw *upstreamWrapper) name() string {
	if t := uw.cfg.Tag; len(t) > 0 {
		return uw.cfg.Tag
	}
	return uw.cfg.Addr
}

func (uw *upstreamWrapper) ExchangeContext(ctx context.Context, m []byte) (*[]byte, error) {
	uw.queryTotal.Inc()
	uw.queryCount.Add(1)

	start := time.Now()
	uw.thread.Inc()
	r, err := uw.u.ExchangeContext(ctx, m)
	uw.thread.Dec()

	latency := time.Since(start).Milliseconds()

	if err != nil {
		uw.errTotal.Inc()
		uw.errorCount.Add(1)
	} else {
		uw.responseLatency.Observe(float64(latency))
		uw.updateEmaLatency(latency)
	}
	return r, err
}

func (uw *upstreamWrapper) IncrementUsedTotal() {
	uw.usedTotal.Inc()
}

func (uw *upstreamWrapper) Close() error {
	return uw.u.Close()
}

func (uw *upstreamWrapper) updateEmaLatency(latency int64) {
	const alpha = 0.3

	current := uw.emaLatency.Load()
	if current == 0 {
		uw.emaLatency.Store(latency)
	} else {
		newLatency := int64(float64(current)*(1-alpha) + float64(latency)*alpha)
		uw.emaLatency.Store(newLatency)
	}
}

func (uw *upstreamWrapper) getEmaLatency() int64 {
	return uw.emaLatency.Load()
}

type queryInfo dns.Msg

func (q *queryInfo) MarshalLogObject(encoder zapcore.ObjectEncoder) error {
	if len(q.Question) != 1 {
		encoder.AddBool("odd_question", true)
	} else {
		question := q.Question[0]
		encoder.AddString("qname", question.Name)
		encoder.AddUint16("qtype", question.Qtype)
		encoder.AddUint16("qclass", question.Qclass)
	}
	return nil
}

func copyPayload(b *[]byte) *[]byte {
	bc := pool.GetBuf(len(*b))
	copy(*bc, *b)
	return bc
}
