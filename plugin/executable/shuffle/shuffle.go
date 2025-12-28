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
	"math/rand/v2"

	"github.com/harlanwei/mosdns-lts/v5/pkg/query_context"
	"github.com/harlanwei/mosdns-lts/v5/plugin/executable/sequence"
	"github.com/miekg/dns"
)

const PluginType = "shuffle"

func init() {
	sequence.MustRegExecQuickSetup(PluginType, QuickSetup)
}

var _ sequence.Executable = (*Shuffle)(nil)

type Shuffle struct {
	answer bool
	ns     bool
	extra  bool
}

func NewShuffle(answer, ns, extra bool) *Shuffle {
	return &Shuffle{
		answer: answer,
		ns:     ns,
		extra:  extra,
	}
}

func QuickSetup(_ sequence.BQ, s string) (any, error) {
	return NewShuffle(true, false, false), nil
}

func (s *Shuffle) Exec(_ context.Context, qCtx *query_context.Context) error {
	if r := qCtx.R(); r != nil {
		if s.answer && len(r.Answer) > 0 {
			rand.Shuffle(len(r.Answer), func(i, j int) {
				r.Answer[i], r.Answer[j] = r.Answer[j], r.Answer[i]
			})
		}
		if s.ns && len(r.Ns) > 0 {
			rand.Shuffle(len(r.Ns), func(i, j int) {
				r.Ns[i], r.Ns[j] = r.Ns[j], r.Ns[i]
			})
		}
		if s.extra && len(r.Extra) > 0 {
			filtered := []dns.RR{}
			for _, rr := range r.Extra {
				if rr.Header().Rrtype != dns.TypeOPT {
					filtered = append(filtered, rr)
				}
			}
			if len(filtered) > 0 {
				rand.Shuffle(len(filtered), func(i, j int) {
					filtered[i], filtered[j] = filtered[j], filtered[i]
				})
				newExtra := []dns.RR{}
				for _, rr := range r.Extra {
					if rr.Header().Rrtype == dns.TypeOPT {
						newExtra = append(newExtra, rr)
					} else if len(filtered) > 0 {
						newExtra = append(newExtra, filtered[0])
						filtered = filtered[1:]
					}
				}
				r.Extra = newExtra
			}
		}
	}
	return nil
}
