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

package http3_pool

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	"go.uber.org/zap"
)

type PooledTransport struct {
	pool       *ConnPool
	quicConfig *quic.Config
	tlsConfig  *tls.Config
	dialer     func(ctx context.Context) (*quic.Conn, *http3.Transport, error)
}

type TransportConfig struct {
	QUICConfig  *quic.Config
	TLSConfig   *tls.Config
	Dialer      func(ctx context.Context) (*quic.Conn, *http3.Transport, error)
	MinConns    int
	MaxConns    int
	IdleTimeout time.Duration
	Logger      *zap.Logger
}

func NewPooledTransport(cfg TransportConfig) (*PooledTransport, error) {
	pool, err := NewConnPool(PoolConfig{
		MinConnections: cfg.MinConns,
		MaxConnections: cfg.MaxConns,
		IdleTimeout:    cfg.IdleTimeout,
		Dialer:         cfg.Dialer,
		Logger:         cfg.Logger,
	})
	if err != nil {
		return nil, err
	}

	return &PooledTransport{
		pool:       pool,
		quicConfig: cfg.QUICConfig,
		tlsConfig:  cfg.TLSConfig,
		dialer:     cfg.Dialer,
	}, nil
}

func (p *PooledTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()

	pc, err := p.pool.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get connection from pool: %w", err)
	}

	resp, err := pc.transport.RoundTrip(req)
	if err != nil {
		p.pool.Release(pc, false)
		return nil, err
	}

	return resp, nil
}

func (p *PooledTransport) Close() error {
	return p.pool.Close()
}

func (p *PooledTransport) Stats() (active int, total int) {
	return p.pool.Stats()
}
