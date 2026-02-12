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
	"sync/atomic"
	"time"

	"github.com/dgraph-io/ristretto/v2"
	"github.com/harlanwei/mosdns-lts/v5/pkg/concurrent_lru"
	"github.com/harlanwei/mosdns-lts/v5/pkg/utils"
)

const (
	defaultCleanerInterval = time.Second * 10
)

type Key interface {
	concurrent_lru.Hashable
}

type Value interface {
	any
}

type Cache[K Key, V Value] struct {
	opts Opts

	closed    atomic.Bool
	ristretto *ristretto.Cache[uint64, *elem[V]]
}

type Opts struct {
	Size            int
	CleanerInterval time.Duration
}

func (opts *Opts) init() {
	utils.SetDefaultNum(&opts.Size, 1024)
	utils.SetDefaultNum(&opts.CleanerInterval, defaultCleanerInterval)
}

type elem[V Value] struct {
	v              V
	expirationTime time.Time
}

func New[K Key, V Value](opts Opts) *Cache[K, V] {
	opts.init()

	rc, err := ristretto.NewCache[uint64, *elem[V]](&ristretto.Config[uint64, *elem[V]]{
		NumCounters: int64(opts.Size) * 100,
		MaxCost:     int64(opts.Size),
		BufferItems: 64,
		Metrics:     true,
	})
	if err != nil {
		panic(err)
	}

	c := &Cache[K, V]{
		opts:      opts,
		ristretto: rc,
	}
	return c
}

func (c *Cache[K, V]) Close() error {
	if ok := c.closed.CompareAndSwap(false, true); ok {
		c.ristretto.Close()
	}
	return nil
}

func (c *Cache[K, V]) Get(key K) (v V, expirationTime time.Time, ok bool) {
	h := key.Sum()
	if e, found := c.ristretto.Get(h); found {
		if e.expirationTime.Before(time.Now()) {
			c.ristretto.Del(h)
			return
		}
		return e.v, e.expirationTime, true
	}
	return
}

func (c *Cache[K, V]) Range(f func(key K, v V, expirationTime time.Time) error) error {
	return nil
}

func (c *Cache[K, V]) Store(key K, v V, expirationTime time.Time) {
	now := time.Now()
	if now.After(expirationTime) {
		return
	}

	e := &elem[V]{
		v:              v,
		expirationTime: expirationTime,
	}
	h := key.Sum()
	ttl := time.Until(expirationTime)
	c.ristretto.SetWithTTL(h, e, 1, ttl)
	c.ristretto.Wait()
}

func (c *Cache[K, V]) Len() int {
	return int(c.ristretto.Metrics.KeysAdded() - c.ristretto.Metrics.KeysEvicted())
}

func (c *Cache[K, V]) Flush() {
	c.ristretto.Clear()
}
