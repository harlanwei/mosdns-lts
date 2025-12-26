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

package http3_pool

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"go.uber.org/zap"
)

type pooledConn struct {
	conn      *quic.Conn
	transport *http3.Transport
	lastUsed  time.Time
	healthy   atomic.Bool
}

type ConnPool struct {
	minConnections int
	maxConnections int
	idleTimeout    time.Duration

	mu     sync.Mutex
	conns  []*pooledConn
	dialer func(ctx context.Context) (*quic.Conn, *http3.Transport, error)

	logger *zap.Logger
	closed atomic.Bool
}

type PoolConfig struct {
	MinConnections int
	MaxConnections int
	IdleTimeout    time.Duration
	Dialer         func(ctx context.Context) (*quic.Conn, *http3.Transport, error)
	Logger         *zap.Logger
}

func NewConnPool(cfg PoolConfig) (*ConnPool, error) {
	if cfg.MinConnections < 0 {
		return nil, fmt.Errorf("MinConnections cannot be negative")
	}
	if cfg.MaxConnections <= 0 {
		cfg.MaxConnections = 4
	}
	if cfg.MinConnections > cfg.MaxConnections {
		cfg.MinConnections = cfg.MaxConnections
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 60 * time.Second
	}

	pool := &ConnPool{
		minConnections: cfg.MinConnections,
		maxConnections: cfg.MaxConnections,
		idleTimeout:    cfg.IdleTimeout,
		dialer:         cfg.Dialer,
		logger:         cfg.Logger,
		conns:          make([]*pooledConn, 0, cfg.MaxConnections),
	}

	if pool.logger == nil {
		pool.logger = zap.NewNop()
	}

	go pool.healthCheckLoop()
	go pool.idleCleanupLoop()

	return pool, nil
}

func (p *ConnPool) Get(ctx context.Context) (*pooledConn, error) {
	if p.closed.Load() {
		return nil, fmt.Errorf("connection pool is closed")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()

	for i := len(p.conns) - 1; i >= 0; i-- {
		pc := p.conns[i]
		if pc.healthy.Load() && now.Sub(pc.lastUsed) < p.idleTimeout {
			pc.lastUsed = now
			return pc, nil
		}
		p.removeConn(i)
	}

	if len(p.conns) < p.maxConnections {
		conn, transport, err := p.dialer(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to dial new connection: %w", err)
		}
		pc := &pooledConn{
			conn:      conn,
			transport: transport,
			lastUsed:  now,
		}
		pc.healthy.Store(true)
		p.conns = append(p.conns, pc)
		return pc, nil
	}

	return nil, fmt.Errorf("connection pool exhausted (max: %d)", p.maxConnections)
}

func (p *ConnPool) Release(pc *pooledConn, healthy bool) {
	if pc == nil {
		return
	}

	pc.healthy.Store(healthy)
	pc.lastUsed = time.Now()

	if !healthy {
		pc.conn.CloseWithError(0, "unhealthy")
		p.mu.Lock()
		for i := range p.conns {
			if p.conns[i] == pc {
				p.removeConn(i)
				break
			}
		}
		p.mu.Unlock()
	}
}

func (p *ConnPool) removeConn(index int) {
	pc := p.conns[index]
	pc.conn.CloseWithError(0, "")
	p.conns = append(p.conns[:index], p.conns[index+1:]...)
}

func (p *ConnPool) Close() error {
	p.closed.Store(true)

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, pc := range p.conns {
		pc.conn.CloseWithError(0, "pool closed")
	}
	p.conns = p.conns[:0]

	return nil
}

func (p *ConnPool) healthCheckLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if p.closed.Load() {
				return
			}
			p.checkHealth()
		}
	}
}

func (p *ConnPool) checkHealth() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	for i := len(p.conns) - 1; i >= 0; i-- {
		pc := p.conns[i]
		if now.Sub(pc.lastUsed) > p.idleTimeout || !p.checkConnHealth(pc) {
			p.removeConn(i)
		}
	}

	for len(p.conns) < p.minConnections {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		conn, transport, err := p.dialer(ctx)
		cancel()

		if err != nil {
			p.logger.Warn("failed to maintain minimum connections", zap.Error(err))
			break
		}

		pc := &pooledConn{
			conn:      conn,
			transport: transport,
			lastUsed:  now,
		}
		pc.healthy.Store(true)
		p.conns = append(p.conns, pc)
	}
}

func (p *ConnPool) checkConnHealth(pc *pooledConn) bool {
	if pc.conn.Context().Err() != nil {
		pc.healthy.Store(false)
		return false
	}

	pc.healthy.Store(true)
	return true
}

func (p *ConnPool) idleCleanupLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if p.closed.Load() {
				return
			}
			p.cleanupIdle()
		}
	}
}

func (p *ConnPool) cleanupIdle() {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	for i := len(p.conns) - 1; i >= 0; i-- {
		if len(p.conns) <= p.minConnections {
			break
		}
		pc := p.conns[i]
		if now.Sub(pc.lastUsed) > p.idleTimeout {
			p.removeConn(i)
		}
	}
}

func (p *ConnPool) Stats() (active int, total int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now()
	for _, pc := range p.conns {
		if pc.healthy.Load() && now.Sub(pc.lastUsed) < p.idleTimeout {
			active++
		}
	}
	return active, len(p.conns)
}
