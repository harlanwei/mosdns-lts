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
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/harlanwei/mosdns-lts/v5/pkg/upstream/doh"
	"github.com/miekg/dns"
	"go.uber.org/zap"
)

func TestAdaptiveDoH(t *testing.T) {
	doHResponse := createTestServer(t, false)
	doH3Response := createTestServer(t, true)
	defer doHResponse.Close()
	defer doH3Response.Close()

	doHClient := doHResponse.Client()
	doH3Client := doH3Response.Client()

	dohUpstream, err := doh.NewUpstream(doHResponse.URL, doHClient.Transport, zap.NewNop())
	if err != nil {
		t.Fatalf("failed to create DoH upstream: %v", err)
	}

	doh3Upstream, err := doh.NewUpstream(doH3Response.URL, doH3Client.Transport, zap.NewNop())
	if err != nil {
		t.Fatalf("failed to create DoH3 upstream: %v", err)
	}

	adaptive, err := NewUpstream(dohUpstream, doh3Upstream, Opt{
		TrialCount: 5,
		Logger:     zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("failed to create adaptive upstream: %v", err)
	}
	defer adaptive.Close()

	ctx := context.Background()
	msg := new(dns.Msg)
	msg.SetQuestion("example.com.", dns.TypeA)

	for i := 0; i < 10; i++ {
		q, err := msg.Pack()
		if err != nil {
			t.Fatalf("failed to pack query: %v", err)
		}

		resp, err := adaptive.ExchangeContext(ctx, q)
		if err != nil {
			t.Errorf("query %d failed: %v", i, err)
			continue
		}

		if resp == nil {
			t.Errorf("query %d returned nil response", i)
			continue
		}
	}

	stats := adaptive.GetStats()
	t.Logf("DoH stats: total=%d, success=%d, failed=%d",
		stats[protocolDoH].totalRequests.Load(),
		stats[protocolDoH].successRequests.Load(),
		stats[protocolDoH].failedRequests.Load())
	t.Logf("DoH3 stats: total=%d, success=%d, failed=%d",
		stats[protocolDoH3].totalRequests.Load(),
		stats[protocolDoH3].successRequests.Load(),
		stats[protocolDoH3].failedRequests.Load())
	t.Logf("Preferred protocol: %s", adaptive.GetPreferredProtocol())
	t.Logf("Current protocol: %s", adaptive.GetCurrentProtocol())
}

func TestAdaptiveDoHProtocolSwitch(t *testing.T) {
	slowServer := createDelayedServer(t, 100*time.Millisecond, false)
	fastServer := createDelayedServer(t, 10*time.Millisecond, false)
	defer slowServer.Close()
	defer fastServer.Close()

	slowClient := slowServer.Client()
	fastClient := fastServer.Client()

	slowUpstream, err := doh.NewUpstream(slowServer.URL, slowClient.Transport, zap.NewNop())
	if err != nil {
		t.Fatalf("failed to create slow upstream: %v", err)
	}

	fastUpstream, err := doh.NewUpstream(fastServer.URL, fastClient.Transport, zap.NewNop())
	if err != nil {
		t.Fatalf("failed to create fast upstream: %v", err)
	}

	adaptive, err := NewUpstream(slowUpstream, fastUpstream, Opt{
		TrialCount: 6,
		Preference: 0.8,
		Logger:     zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("failed to create adaptive upstream: %v", err)
	}
	defer adaptive.Close()

	ctx := context.Background()
	msg := new(dns.Msg)
	msg.SetQuestion("example.com.", dns.TypeA)

	for i := 0; i < 10; i++ {
		q, err := msg.Pack()
		if err != nil {
			t.Fatalf("failed to pack query: %v", err)
		}

		_, err = adaptive.ExchangeContext(ctx, q)
		if err != nil {
			t.Errorf("query %d failed: %v", i, err)
		}
	}

	stats := adaptive.GetStats()
	t.Logf("Slow protocol stats: total=%d, success=%d, latency=%d",
		stats[protocolDoH].totalRequests.Load(),
		stats[protocolDoH].successRequests.Load(),
		stats[protocolDoH].totalLatency.Load())
	t.Logf("Fast protocol stats: total=%d, success=%d, latency=%d",
		stats[protocolDoH3].totalRequests.Load(),
		stats[protocolDoH3].successRequests.Load(),
		stats[protocolDoH3].totalLatency.Load())
	t.Logf("Preferred protocol: %s", adaptive.GetPreferredProtocol())
}

func createTestServer(t *testing.T, doh3 bool) *httptest.Server {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		dnsParam := r.URL.Query().Get("dns")
		if dnsParam == "" {
			http.Error(w, "missing dns parameter", http.StatusBadRequest)
			return
		}

		msg := new(dns.Msg)
		msg.SetReply(msg)
		msg.SetRcode(msg, dns.RcodeSuccess)
		msg.SetQuestion("example.com.", dns.TypeA)
		msg.Answer = []dns.RR{
			&dns.A{
				Hdr: dns.RR_Header{
					Name:   "example.com.",
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    300,
				},
				A: net.ParseIP("93.184.216.34"),
			},
		}

		packed, err := msg.Pack()
		if err != nil {
			http.Error(w, "failed to pack response", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(packed)
	})

	return httptest.NewServer(handler)
}

func createDelayedServer(t *testing.T, delay time.Duration, doh3 bool) *httptest.Server {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)

		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		dnsParam := r.URL.Query().Get("dns")
		if dnsParam == "" {
			http.Error(w, "missing dns parameter", http.StatusBadRequest)
			return
		}

		msg := new(dns.Msg)
		msg.SetReply(msg)
		msg.SetRcode(msg, dns.RcodeSuccess)
		msg.SetQuestion("example.com.", dns.TypeA)
		msg.Answer = []dns.RR{
			&dns.A{
				Hdr: dns.RR_Header{
					Name:   "example.com.",
					Rrtype: dns.TypeA,
					Class:  dns.ClassINET,
					Ttl:    300,
				},
				A: net.ParseIP("93.184.216.34"),
			},
		}

		packed, err := msg.Pack()
		if err != nil {
			http.Error(w, "failed to pack response", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/dns-message")
		w.Write(packed)
	})

	return httptest.NewServer(handler)
}
