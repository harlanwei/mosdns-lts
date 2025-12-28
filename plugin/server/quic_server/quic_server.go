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

package quic_server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/harlanwei/mosdns-lts/v5/coremain"
	"github.com/harlanwei/mosdns-lts/v5/pkg/server"
	"github.com/harlanwei/mosdns-lts/v5/pkg/utils"
	"github.com/harlanwei/mosdns-lts/v5/plugin/server/server_utils"
	"github.com/quic-go/quic-go"
	"go.uber.org/zap"
)

const PluginType = "quic_server"

func init() {
	coremain.RegNewPluginFunc(PluginType, Init, func() any { return new(Args) })
}

type Args struct {
	Entry       string `yaml:"entry"`
	Listen      string `yaml:"listen"`
	Cert        string `yaml:"cert"`
	Key         string `yaml:"key"`
	IdleTimeout int    `yaml:"idle_timeout"`
}

func (a *Args) init() {
	utils.SetDefaultNum(&a.IdleTimeout, 30)
}

type QuicServer struct {
	args *Args

	l *quic.Listener
}

func (s *QuicServer) Close() error {
	return s.l.Close()
}

func Init(bp *coremain.BP, args any) (any, error) {
	return StartServer(bp, args.(*Args))
}

func StartServer(bp *coremain.BP, args *Args) (*QuicServer, error) {
	logger := bp.L()

	dh, err := server_utils.NewHandler(bp, args.Entry)
	if err != nil {
		return nil, fmt.Errorf("failed to init dns handler, %w", err)
	}

	// Init tls
	if len(args.Key) == 0 || len(args.Cert) == 0 {
		return nil, errors.New("quic server requires a tls certificate")
	}
	tlsConfig := new(tls.Config)
	if err := server.LoadCert(tlsConfig, args.Cert, args.Key); err != nil {
		return nil, fmt.Errorf("failed to read tls cert, %w", err)
	}
	tlsConfig.NextProtos = []string{"doq"}

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
		IPV6_V6ONLY: ipv6only,
	}
	lc := net.ListenConfig{Control: server_utils.ListenerControl(socketOpt)}
	uc, err := lc.ListenPacket(context.Background(), network, args.Listen)
	if err != nil {
		return nil, fmt.Errorf("failed to listen socket, %w", err)
	}

	idleTimeout := time.Duration(args.IdleTimeout) * time.Second

	quicConfig := &quic.Config{
		MaxIdleTimeout:                 idleTimeout,
		InitialStreamReceiveWindow:     64 * 1024,
		MaxStreamReceiveWindow:         64 * 1024,
		InitialConnectionReceiveWindow: 512 * 1024,
		MaxConnectionReceiveWindow:     2 * 1024 * 1024,
		Allow0RTT:                      true,

		MaxIncomingStreams:    100,
		MaxIncomingUniStreams: -1,

		KeepAlivePeriod: time.Second * 15,
	}

	srk, _, err := utils.InitQUICSrkFromIfaceMac()
	if err != nil {
		logger.Warn("failed to init quic stateless reset key, it will be disabled", zap.Error(err))
	}
	qt := &quic.Transport{
		Conn:              uc,
		StatelessResetKey: (*quic.StatelessResetKey)(srk),
	}

	quicListener, err := qt.Listen(tlsConfig, quicConfig)
	if err != nil {
		qt.Close()
		return nil, fmt.Errorf("failed to listen quic, %w", err)
	}
	bp.L().Info("quic server started", zap.Stringer("addr", quicListener.Addr()))

	go func() {
		defer quicListener.Close()
		serverOpts := server.DoQServerOpts{Logger: bp.L(), IdleTimeout: idleTimeout}
		err := server.ServeDoQ(quicListener, dh, serverOpts)
		bp.M().GetSafeClose().SendCloseSignal(err)
	}()
	return &QuicServer{
		args: args,
		l:    quicListener,
	}, nil
}
