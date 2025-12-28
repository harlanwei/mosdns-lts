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

package server

import (
	"context"
	"net"
	"net/netip"
	"runtime"

	"github.com/harlanwei/mosdns-lts/v5/pkg/pool"
	"github.com/miekg/dns"
	"go.uber.org/zap"
)

type udpRequest struct {
	q               *dns.Msg
	clientAddr      netip.Addr
	dstIpFromCm     net.IP
	remoteAddr      netip.AddrPort
	oobWriter       writeSrcAddrToOOB
	responsePayload *[]byte
}

type udpWorker struct {
	workerID    int
	cpuAffinity bool
	conn        *net.UDPConn
	handler     Handler
	listenerCtx context.Context
	logger      *zap.Logger
	requestChan chan udpRequest
}

func newUDPWorker(id int, cpuAffinity bool, conn *net.UDPConn, h Handler, ctx context.Context, logger *zap.Logger) *udpWorker {
	w := &udpWorker{
		workerID:    id,
		cpuAffinity: cpuAffinity,
		conn:        conn,
		handler:     h,
		listenerCtx: ctx,
		logger:      logger,
		requestChan: make(chan udpRequest, 128),
	}
	go w.run()
	return w
}

func (w *udpWorker) run() {
	if w.cpuAffinity {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
	}

	for {
		select {
		case req := <-w.requestChan:
			w.handleRequest(req)
		case <-w.listenerCtx.Done():
			return
		}
	}
}

func (w *udpWorker) handleRequest(req udpRequest) {
	payload := w.handler.Handle(w.listenerCtx, req.q, QueryMeta{ClientAddr: req.clientAddr, FromUDP: true}, pool.PackBuffer)
	if payload == nil {
		return
	}
	defer pool.ReleaseBuf(payload)

	// Check if this is an IPv4-mapped address on an IPv6-only socket
	// If oobWriter is nil on an IPv6 socket, it means IPV6_V6ONLY=1 is set
	localAddr := w.conn.LocalAddr().(*net.UDPAddr)
	if localAddr.IP.To4() == nil && isIPv4Mapped(req.remoteAddr.Addr()) && req.oobWriter == nil {
		// IPv4-mapped address on IPv6-only socket - drop silently
		// This shouldn't happen if IPV6_V6ONLY is set correctly, but handle gracefully
		w.logger.Debug("dropping IPv4-mapped address on IPv6-only socket", zap.Stringer("client", req.remoteAddr))
		return
	}

	var oob []byte
	if req.oobWriter != nil && req.dstIpFromCm != nil {
		oob = req.oobWriter(req.dstIpFromCm)
	}
	if _, _, err := w.conn.WriteMsgUDPAddrPort(*payload, oob, req.remoteAddr); err != nil {
		w.logger.Warn("failed to write response", zap.Stringer("client", req.remoteAddr), zap.Error(err))
	}
}

func (w *udpWorker) submit(req udpRequest) {
	w.requestChan <- req
}

func (w *udpWorker) stop() {
	close(w.requestChan)
}

type udpWorkerPool struct {
	workers     []*udpWorker
	nextWorker  int
	cpuAffinity bool
	oobWriter   writeSrcAddrToOOB
}

func newUDPWorkerPool(size int, cpuAffinity bool, conn *net.UDPConn, h Handler, ctx context.Context, logger *zap.Logger, oobWriter writeSrcAddrToOOB) *udpWorkerPool {
	pool := &udpWorkerPool{
		workers:     make([]*udpWorker, size),
		cpuAffinity: cpuAffinity,
		oobWriter:   oobWriter,
	}

	for i := 0; i < size; i++ {
		pool.workers[i] = newUDPWorker(i, cpuAffinity, conn, h, ctx, logger)
	}

	return pool
}

func (p *udpWorkerPool) submit(q *dns.Msg, clientAddr, remoteAddr netip.AddrPort, dstIpFromCm net.IP) {
	worker := p.workers[p.nextWorker]
	p.nextWorker = (p.nextWorker + 1) % len(p.workers)

	worker.submit(udpRequest{
		q:           q,
		clientAddr:  clientAddr.Addr(),
		dstIpFromCm: dstIpFromCm,
		remoteAddr:  remoteAddr,
		oobWriter:   p.oobWriter,
	})
}

func (p *udpWorkerPool) stop() {
	for _, w := range p.workers {
		w.stop()
	}
}
