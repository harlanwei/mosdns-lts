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

package server

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"runtime"

	"github.com/harlanwei/mosdns-lts/v5/pkg/pool"
	"github.com/miekg/dns"
	"go.uber.org/zap"
)

type UDPServerOpts struct {
	Logger         *zap.Logger
	WorkerPoolSize int
	CPUAffinity    bool
}

// ServeUDP starts a server at c. It returns if c had a read error.
// It always returns a non-nil error.
// h is required. logger is optional.
func ServeUDP(c *net.UDPConn, h Handler, opts UDPServerOpts) error {
	logger := opts.Logger
	if logger == nil {
		logger = nopLogger
	}

	listenerCtx, cancel := context.WithCancelCause(context.Background())
	defer cancel(errListenerCtxCanceled)

	oobReader, oobWriter, err := initOobHandler(c)
	if err != nil {
		return fmt.Errorf("failed to init oob handler, %w", err)
	}

	workerPoolSize := opts.WorkerPoolSize
	if workerPoolSize <= 0 {
		workerPoolSize = runtime.NumCPU()
	}

	var workerPool *udpWorkerPool
	if opts.WorkerPoolSize != 0 {
		workerPool = newUDPWorkerPool(workerPoolSize, opts.CPUAffinity, c, h, listenerCtx, logger, oobWriter)
		defer workerPool.stop()
	}

	oobPool := make([][]byte, 2)
	for i := range oobPool {
		oobPool[i] = make([]byte, 1024)
	}
	oobPoolIdx := 0

	for {
		rb := pool.GetBuf(dns.MaxMsgSize)
		n, oobn, _, remoteAddr, err := c.ReadMsgUDPAddrPort(*rb, oobPool[oobPoolIdx])
		if err != nil {
			pool.ReleaseBuf(rb)
			if n == 0 {
				return fmt.Errorf("unexpected read err: %w", err)
			}
			logger.Warn("read err", zap.Error(err))
			continue
		}

		// Read OOB data from the CURRENT buffer before switching
		oobBuf := oobPool[oobPoolIdx]
		oobPoolIdx = (oobPoolIdx + 1) % len(oobPool)

		q := pool.GetDNSMsg()
		if err := q.Unpack((*rb)[:n]); err != nil {
			logger.Warn("invalid msg", zap.Error(err), zap.Binary("msg", (*rb)[:n]), zap.Stringer("from", remoteAddr))
			pool.ReleaseBuf(rb)
			pool.ReleaseDNSMsg(q)
			continue
		}

		var dstIpFromCm net.IP
		if oobReader != nil {
			var err error
			dstIpFromCm, err = oobReader(oobBuf[:oobn])
			if err != nil {
				logger.Error("failed to get dst address from oob", zap.Error(err))
			}
		}

		if workerPool != nil {
			workerPool.submit(q, remoteAddr, remoteAddr, dstIpFromCm)
			pool.ReleaseBuf(rb)
			pool.ReleaseDNSMsg(q)
		} else {
			go func() {
				payload := h.Handle(listenerCtx, q, QueryMeta{ClientAddr: remoteAddr.Addr(), FromUDP: true}, pool.PackBuffer)
				if payload == nil {
					pool.ReleaseBuf(rb)
					pool.ReleaseDNSMsg(q)
					return
				}
				defer pool.ReleaseBuf(payload)
				pool.ReleaseBuf(rb)
				pool.ReleaseDNSMsg(q)

				// Check if this is an IPv4-mapped address on an IPv6-only socket
				// If oobWriter is nil on an IPv6 socket, it means IPV6_V6ONLY=1 is set
				localAddr := c.LocalAddr().(*net.UDPAddr)
				if localAddr.IP.To4() == nil && isIPv4Mapped(remoteAddr.Addr()) && oobWriter == nil {
					// IPv4-mapped address on IPv6-only socket - drop silently
					// This shouldn't happen if IPV6_V6ONLY is set correctly, but handle gracefully
					logger.Debug("dropping IPv4-mapped address on IPv6-only socket", zap.Stringer("client", remoteAddr))
					return
				}

				var oob []byte
				if oobWriter != nil && dstIpFromCm != nil {
					oob = oobWriter(dstIpFromCm)
				}
				if _, _, err := c.WriteMsgUDPAddrPort(*payload, oob, remoteAddr); err != nil {
					logger.Warn("failed to write response", zap.Stringer("client", remoteAddr), zap.Error(err))
				}
			}()
		}
	}
}

type getSrcAddrFromOOB func(oob []byte) (net.IP, error)
type writeSrcAddrToOOB func(a net.IP) []byte

// isIPv4Mapped checks if an address is an IPv4-mapped IPv6 address (::ffff:x.x.x.x)
func isIPv4Mapped(addr netip.Addr) bool {
	if !addr.Is6() {
		return false
	}
	// IPv4-mapped addresses have the form ::ffff:x.x.x.x
	// Check if the first 12 bytes are zero and bytes 12-13 are 0xff, 0xff
	bytes := addr.As16()
	return bytes[0] == 0 && bytes[1] == 0 && bytes[2] == 0 && bytes[3] == 0 &&
		bytes[4] == 0 && bytes[5] == 0 && bytes[6] == 0 && bytes[7] == 0 &&
		bytes[8] == 0 && bytes[9] == 0 && bytes[10] == 0xff && bytes[11] == 0xff
}
