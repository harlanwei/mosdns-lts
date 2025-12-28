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

package shuffle

import (
	"context"
	"net"
	"testing"

	"github.com/harlanwei/mosdns-lts/v5/pkg/query_context"
	"github.com/miekg/dns"
)

func TestShuffle(t *testing.T) {
	ip1 := net.ParseIP("1.1.1.1").To4()
	ip2 := net.ParseIP("2.2.2.2").To4()
	ip3 := net.ParseIP("3.3.3.3").To4()
	ip4 := net.ParseIP("4.4.4.4").To4()

	r := &dns.Msg{
		Answer: []dns.RR{
			&dns.A{A: ip1},
			&dns.A{A: ip2},
			&dns.A{A: ip3},
			&dns.A{A: ip4},
		},
		Ns: []dns.RR{
			&dns.NS{Ns: "ns1.example.com."},
			&dns.NS{Ns: "ns2.example.com."},
		},
	}

	originalAnswerCount := len(r.Answer)
	originalNsCount := len(r.Ns)

	s := NewShuffle(true, true, false)
	qCtx := query_context.NewContext(r)

	err := s.Exec(context.Background(), qCtx)
	if err != nil {
		t.Fatal(err)
	}

	if len(r.Answer) != originalAnswerCount {
		t.Errorf("Answer count changed: expected %d, got %d", originalAnswerCount, len(r.Answer))
	}
	if len(r.Ns) != originalNsCount {
		t.Errorf("Ns count changed: expected %d, got %d", originalNsCount, len(r.Ns))
	}
}

func TestQuickSetup(t *testing.T) {
	_, err := QuickSetup(nil, "")
	if err != nil {
		t.Fatal(err)
	}
}

func TestEmptyResponse(t *testing.T) {
	r := &dns.Msg{}

	s := NewShuffle(true, true, true)
	qCtx := query_context.NewContext(r)

	err := s.Exec(context.Background(), qCtx)
	if err != nil {
		t.Fatal(err)
	}
}
