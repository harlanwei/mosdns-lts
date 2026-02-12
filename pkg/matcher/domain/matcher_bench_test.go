package domain

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkKeywordMatcherOld(b *testing.B) {
	kwCounts := []int{100, 1000, 10000}
	for _, kwCount := range kwCounts {
		b.Run(fmt.Sprintf("keywords_%d", kwCount), func(b *testing.B) {
			kws := make([]string, kwCount)
			for i := 0; i < kwCount; i++ {
				kws[i] = fmt.Sprintf("keyword%d", i)
			}

			domain := "www.examplekeyword500test.com"

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, kw := range kws {
					if strings.Contains(domain, kw) {
						break
					}
				}
			}
		})
	}
}

func BenchmarkKeywordMatcherNew(b *testing.B) {
	kwCounts := []int{100, 1000, 10000}
	for _, kwCount := range kwCounts {
		b.Run(fmt.Sprintf("keywords_%d", kwCount), func(b *testing.B) {
			m := NewKeywordMatcher[int]()
			for i := 0; i < kwCount; i++ {
				m.Add(fmt.Sprintf("keyword%d", i), i)
			}

			domain := "www.examplekeyword500test.com"

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.Match(domain)
			}
		})
	}
}

func BenchmarkKeywordMatcherMatch(b *testing.B) {
	kwCounts := []int{100, 1000, 10000}
	for _, kwCount := range kwCounts {
		b.Run(fmt.Sprintf("keywords_%d_match", kwCount), func(b *testing.B) {
			m := NewKeywordMatcher[int]()
			for i := 0; i < kwCount; i++ {
				m.Add(fmt.Sprintf("keyword%d", i), i)
			}

			domain := "www.examplekeyword500test.com"

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.Match(domain)
			}
		})
	}
}

func BenchmarkKeywordMatcherNoMatch(b *testing.B) {
	kwCounts := []int{100, 1000, 10000}
	for _, kwCount := range kwCounts {
		b.Run(fmt.Sprintf("keywords_%d_nomatch", kwCount), func(b *testing.B) {
			m := NewKeywordMatcher[int]()
			for i := 0; i < kwCount; i++ {
				m.Add(fmt.Sprintf("notfound%d", i), i)
			}

			domain := "www.examplekeyword500test.com"

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.Match(domain)
			}
		})
	}
}

func BenchmarkKeywordMatcherConcurrent(b *testing.B) {
	m := NewKeywordMatcher[int]()
	for i := 0; i < 1000; i++ {
		m.Add(fmt.Sprintf("keyword%d", i), i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			domain := fmt.Sprintf("www.examplekeyword%dtest.com", i%1000)
			m.Match(domain)
			i++
		}
	})
}
