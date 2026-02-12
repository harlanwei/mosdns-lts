package cache

import (
	"fmt"
	"hash/maphash"
	"testing"
	"time"
)

type benchKey string

var benchSeed = maphash.MakeSeed()

func (k benchKey) Sum() uint64 {
	return maphash.String(benchSeed, string(k))
}

func BenchmarkCacheStore(b *testing.B) {
	sizes := []int{1000, 10000, 100000}
	for _, size := range sizes {
		b.Run(fmt.Sprintf("size_%d", size), func(b *testing.B) {
			c := New[benchKey, []byte](Opts{Size: size})
			defer c.Close()

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				key := benchKey(fmt.Sprintf("key_%d", i%size))
				val := []byte(fmt.Sprintf("value_%d", i))
				c.Store(key, val, time.Now().Add(time.Hour))
			}
		})
	}
}

func BenchmarkCacheGet(b *testing.B) {
	sizes := []int{1000, 10000, 100000}
	for _, size := range sizes {
		b.Run(fmt.Sprintf("size_%d", size), func(b *testing.B) {
			c := New[benchKey, []byte](Opts{Size: size})
			defer c.Close()

			for i := 0; i < size; i++ {
				key := benchKey(fmt.Sprintf("key_%d", i))
				val := []byte(fmt.Sprintf("value_%d", i))
				c.Store(key, val, time.Now().Add(time.Hour))
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				key := benchKey(fmt.Sprintf("key_%d", i%size))
				c.Get(key)
			}
		})
	}
}

func BenchmarkCacheConcurrent(b *testing.B) {
	sizes := []int{1000, 10000, 100000}
	for _, size := range sizes {
		b.Run(fmt.Sprintf("size_%d", size), func(b *testing.B) {
			c := New[benchKey, []byte](Opts{Size: size})
			defer c.Close()

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					key := benchKey(fmt.Sprintf("key_%d", i%size))
					if i%2 == 0 {
						val := []byte(fmt.Sprintf("value_%d", i))
						c.Store(key, val, time.Now().Add(time.Hour))
					} else {
						c.Get(key)
					}
					i++
				}
			})
		})
	}
}

func BenchmarkCacheMixed(b *testing.B) {
	c := New[benchKey, []byte](Opts{Size: 10000})
	defer c.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := benchKey(fmt.Sprintf("key_%d", i%10000))
		switch i % 10 {
		case 0, 1, 2:
			val := []byte(fmt.Sprintf("value_%d", i))
			c.Store(key, val, time.Now().Add(time.Hour))
		default:
			c.Get(key)
		}
	}
}
