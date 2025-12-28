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

package udp_server

import (
	"context"
	"fmt"
	"net"

	"github.com/harlanwei/mosdns-lts/v5/coremain"
	"github.com/harlanwei/mosdns-lts/v5/pkg/server"
	"github.com/harlanwei/mosdns-lts/v5/pkg/utils"
	"github.com/harlanwei/mosdns-lts/v5/plugin/server/server_utils"
	"go.uber.org/zap"
)

const PluginType = "udp_server"

func init() {
	coremain.RegNewPluginFunc(PluginType, Init, func() any { return new(Args) })
}

type Args struct {
	Entry       string `yaml:"entry"`
	Listen      string `yaml:"listen"`
	WorkerPool  int    `yaml:"worker_pool"`
	CPUAffinity bool   `yaml:"cpu_affinity"`
	SO_RCVBUF   int    `yaml:"so_rcvbuf"`
	SO_SNDBUF   int    `yaml:"so_sndbuf"`
}

func (a *Args) init() {
	utils.SetDefaultString(&a.Listen, "127.0.0.1:53")
	utils.SetDefaultNum(&a.SO_RCVBUF, 512*1024)
	utils.SetDefaultNum(&a.SO_SNDBUF, 512*1024)
}

type UdpServer struct {
	args *Args

	c net.PacketConn
}

func (s *UdpServer) Close() error {
	return s.c.Close()
}

func Init(bp *coremain.BP, args any) (any, error) {
	return StartServer(bp, args.(*Args))
}

func StartServer(bp *coremain.BP, args *Args) (*UdpServer, error) {
	dh, err := server_utils.NewHandler(bp, args.Entry)
	if err != nil {
		return nil, fmt.Errorf("failed to init dns handler, %w", err)
	}

	host, _, err := net.SplitHostPort(args.Listen)
	if err != nil {
		return nil, fmt.Errorf("failed to parse listen address, %w", err)
	}

	ip := net.ParseIP(host)
	// Explicitly use udp4/udp6 to ensure clean separation between IPv4 and IPv6
	// Using "udp" can create dual-stack sockets on some systems
	network := "udp4"
	ipv6only := false
	if ip != nil && ip.To4() == nil {
		network = "udp6"
		// Set IPV6_V6ONLY=1 for IPv6 sockets to prevent IPv4-mapped addresses
		ipv6only = true
	}

	socketOpt := server_utils.ListenerSocketOpts{
		SO_REUSEPORT: true,
		SO_RCVBUF:    args.SO_RCVBUF,
		SO_SNDBUF:    args.SO_SNDBUF,
		IPV6_V6ONLY:  ipv6only,
	}
	lc := net.ListenConfig{Control: server_utils.ListenerControl(socketOpt)}
	c, err := lc.ListenPacket(context.Background(), network, args.Listen)
	if err != nil {
		return nil, fmt.Errorf("failed to create socket, %w", err)
	}
	bp.L().Info("udp server started", zap.Stringer("addr", c.LocalAddr()))

	go func() {
		defer c.Close()
		err := server.ServeUDP(c.(*net.UDPConn), dh, server.UDPServerOpts{
			Logger:         bp.L(),
			WorkerPoolSize: args.WorkerPool,
			CPUAffinity:    args.CPUAffinity,
		})
		bp.M().GetSafeClose().SendCloseSignal(err)
	}()
	return &UdpServer{
		args: args,
		c:    c,
	}, nil
}
