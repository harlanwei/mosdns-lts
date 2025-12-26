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

package transport

import (
	"context"
	"encoding/binary"
	"time"

	"github.com/IrineSistiana/mosdns/v5/pkg/dnsutils"
	"github.com/IrineSistiana/mosdns/v5/pkg/pool"
	"github.com/IrineSistiana/mosdns/v5/pkg/qos"
	"github.com/quic-go/quic-go"
)

type ResilientQuicConn struct {
	conn    *quic.Conn
	timeout *qos.AdaptiveTimeout
	breaker *qos.CircuitBreaker
}

type ResilientConnConfig struct {
	BaseTimeout     time.Duration
	MinTimeout      time.Duration
	MaxTimeout      time.Duration
	CongestionMult  float64
	CircuitFailures int
	CircuitReset    time.Duration
}

func NewResilientQuicConn(conn *quic.Conn, cfg ResilientConnConfig) *ResilientQuicConn {
	timeout := qos.NewAdaptiveTimeout(qos.TimeoutConfig{
		BaseTimeout:    cfg.BaseTimeout,
		MinTimeout:     cfg.MinTimeout,
		MaxTimeout:     cfg.MaxTimeout,
		CongestionMult: cfg.CongestionMult,
	})

	breaker := qos.NewCircuitBreaker(qos.CircuitBreakerConfig{
		MaxFailures:      cfg.CircuitFailures,
		ResetTimeout:     cfg.CircuitReset,
		HalfOpenAttempts: 3,
	})

	return &ResilientQuicConn{
		conn:    conn,
		timeout: timeout,
		breaker: breaker,
	}
}

func (c *ResilientQuicConn) Close() error {
	return c.conn.CloseWithError(0, "")
}

func (c *ResilientQuicConn) ReserveNewQuery() (_ ReservedExchanger, closed bool) {
	select {
	case <-c.conn.Context().Done():
		return nil, true
	default:
	}

	if c.breaker.State() == qos.StateOpen {
		return nil, false
	}

	s, err := c.conn.OpenStream()
	if err != nil {
		return nil, false
	}

	return c.wrapExchanger(s), false
}

type resilientExchanger struct {
	stream    *quic.Stream
	conn      *ResilientQuicConn
	startTime time.Time
}

func (c *ResilientQuicConn) wrapExchanger(s *quic.Stream) ReservedExchanger {
	return &resilientExchanger{
		stream:    s,
		conn:      c,
		startTime: time.Now(),
	}
}

func (re *resilientExchanger) ExchangeReserved(ctx context.Context, q []byte) (resp *[]byte, err error) {
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		if err != nil {
			re.conn.timeout.RecordTimeout()
		} else {
			re.conn.timeout.RecordSuccess(duration)
		}
	}()

	deadline := startTime.Add(re.conn.timeout.GetTimeout())
	re.stream.SetDeadline(deadline)

	type res struct {
		resp *[]byte
		err  error
	}
	rc := make(chan res, 1)

	go func() {
		r, err := exchangeOnStream(re.stream, q)
		rc <- res{resp: r, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-rc:
		return r.resp, r.err
	}
}

func exchangeOnStream(stream *quic.Stream, q []byte) (resp *[]byte, err error) {
	payload, err := copyMsgWithLenHdr(q)
	if err != nil {
		stream.CancelRead(_DOQ_REQUEST_CANCELLED)
		stream.CancelWrite(_DOQ_REQUEST_CANCELLED)
		return nil, err
	}

	orgQid := binary.BigEndian.Uint16((*payload)[2:])
	binary.BigEndian.PutUint16((*payload)[2:], 0)

	_, err = stream.Write(*payload)
	pool.ReleaseBuf(payload)
	if err != nil {
		stream.CancelRead(_DOQ_REQUEST_CANCELLED)
		stream.CancelWrite(_DOQ_REQUEST_CANCELLED)
		return nil, err
	}

	stream.Close()

	r, err := dnsutils.ReadRawMsgFromTCP(stream)
	if resp != nil {
		binary.BigEndian.PutUint16((*resp), orgQid)
	}
	stream.CancelRead(_DOQ_NO_ERROR)
	return r, err
}

func (re *resilientExchanger) WithdrawReserved() {
	re.stream.CancelRead(_DOQ_REQUEST_CANCELLED)
	re.stream.CancelWrite(_DOQ_REQUEST_CANCELLED)
}
