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

package concurrent_lru

import (
	"sync"

	lru "github.com/hashicorp/golang-lru/v2/simplelru"
)

type Hashable interface {
	comparable
	Sum() uint64
}

type ShardedLRU[K Hashable, V any] struct {
	l []*ConcurrentLRU[K, V]
}

func NewShardedLRU[K Hashable, V any](
	shardNum, maxSizePerShard int,
	onEvict func(key K, v V),
) *ShardedLRU[K, V] {
	cl := &ShardedLRU[K, V]{
		l: make([]*ConcurrentLRU[K, V], 0, shardNum),
	}

	for i := 0; i < shardNum; i++ {
		cl.l = append(cl.l, NewConcurrentLRU[K, V](maxSizePerShard, onEvict))
	}

	return cl
}

func (c *ShardedLRU[K, V]) Add(key K, v V) {
	sl := c.getShard(key)
	sl.Add(key, v)
}

func (c *ShardedLRU[K, V]) Del(key K) {
	sl := c.getShard(key)
	sl.Del(key)
}

func (c *ShardedLRU[K, V]) Clean(f func(key K, v V) (remove bool)) (removed int) {
	for _, l := range c.l {
		removed += l.Clean(f)
	}
	return removed
}

func (c *ShardedLRU[K, V]) Flush() {
	for _, l := range c.l {
		l.Flush()
	}
}

func (c *ShardedLRU[K, V]) Get(key K) (v V, ok bool) {
	sl := c.getShard(key)
	v, ok = sl.Get(key)
	return
}

func (c *ShardedLRU[K, V]) Len() int {
	sum := 0
	for _, l := range c.l {
		sum += l.Len()
	}
	return sum
}

func (c *ShardedLRU[K, V]) shardNum() int {
	return len(c.l)
}

func (c *ShardedLRU[K, V]) getShard(key K) *ConcurrentLRU[K, V] {
	return c.l[key.Sum()%uint64(c.shardNum())]
}

type ConcurrentLRU[K comparable, V any] struct {
	mu       sync.RWMutex
	lru      *lru.LRU[K, V]
	onEvict  func(key K, v V)
}

func NewConcurrentLRU[K comparable, V any](maxSize int, onEvict func(key K, v V)) *ConcurrentLRU[K, V] {
	evictCb := func(key K, v V) {
		if onEvict != nil {
			onEvict(key, v)
		}
	}
	
	l, err := lru.NewLRU[K, V](maxSize, evictCb)
	if err != nil {
		panic(err)
	}

	return &ConcurrentLRU[K, V]{
		lru:     l,
		onEvict: onEvict,
	}
}

func (c *ConcurrentLRU[K, V]) Add(key K, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lru.Add(key, v)
}

func (c *ConcurrentLRU[K, V]) Del(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lru.Remove(key)
}

func (c *ConcurrentLRU[K, V]) Clean(f func(key K, v V) (remove bool)) (removed int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	keys := c.lru.Keys()
	for _, key := range keys {
		if val, ok := c.lru.Get(key); ok {
			if f(key, val) {
				c.lru.Remove(key)
				removed++
			}
		}
	}
	return removed
}

func (c *ConcurrentLRU[K, V]) Flush() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lru.Purge()
}

func (c *ConcurrentLRU[K, V]) Get(key K) (v V, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lru.Get(key)
}

func (c *ConcurrentLRU[K, V]) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lru.Len()
}
