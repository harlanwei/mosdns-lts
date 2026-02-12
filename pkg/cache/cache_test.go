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

package cache

import (
	"sync"
	"testing"
	"time"
)

type testKey int

func (t testKey) Sum() uint64 {
	return uint64(t)
}

func Test_Cache(t *testing.T) {
	c := New[testKey, int](Opts{
		Size: 1024,
	})
	defer c.Close()

	for i := 0; i < 128; i++ {
		key := testKey(i)
		c.Store(key, i, time.Now().Add(time.Second*10))
	}

	for i := 0; i < 128; i++ {
		key := testKey(i)
		c.Store(key, i, time.Now().Add(time.Second*10))
	}

	hitCount := 0
	for i := 0; i < 128; i++ {
		key := testKey(i)
		_, _, ok := c.Get(key)
		if ok {
			hitCount++
		}
	}

	if hitCount == 0 {
		t.Fatal("no items were cached")
	}

	t.Logf("Cache hit rate: %d/128 items admitted", hitCount)

	for i := 0; i < 1024*4; i++ {
		key := testKey(i)
		c.Store(key, i, time.Now().Add(time.Second*10))
	}

	if l := c.Len(); l > 1024 {
		t.Fatalf("cache overflow: got %d, want <= 1024", l)
	}
}

func Test_memCache_cleaner(t *testing.T) {
	c := New[testKey, int](Opts{
		Size:            1024,
		CleanerInterval: time.Millisecond * 10,
	})
	defer c.Close()
	for i := 0; i < 64; i++ {
		key := testKey(i)
		c.Store(key, i, time.Now().Add(time.Millisecond*100))
	}

	time.Sleep(time.Millisecond * 200)

	hitCount := 0
	for i := 0; i < 64; i++ {
		key := testKey(i)
		_, _, ok := c.Get(key)
		if ok {
			hitCount++
		}
	}

	t.Logf("After expiration: %d items still accessible (should be 0)", hitCount)
	if hitCount > 0 {
		t.Errorf("expected all items to be expired, but %d were still accessible", hitCount)
	}
}

func Test_memCache_race(t *testing.T) {
	c := New[testKey, int](Opts{
		Size: 1024,
	})
	defer c.Close()

	wg := sync.WaitGroup{}
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 256; i++ {
				key := testKey(i)
				c.Store(key, i, time.Now().Add(time.Minute))
				_, _, _ = c.Get(key)
			}
		}()
	}
	wg.Wait()
}
