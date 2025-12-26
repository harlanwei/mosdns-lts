/*
 * Copyright (C) 2025, Wei Chen
 *
 * This file is part of mosdns.
 *
 * mosdns is free software: you can redistribute it and/or modify
 * it under terms of the GNU General Public License as published by
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

package qos

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrQueueFull = errors.New("request queue is full")
)

type Request struct {
	Execute  func(context.Context) error
	Priority int
	Timer    time.Time
}

type RequestQueue struct {
	mu sync.Mutex

	queue       []*Request
	maxSize     int
	maxWaitTime time.Duration

	droppedCount   atomic.Int64
	processedCount atomic.Int64
}

type QueueConfig struct {
	MaxSize     int
	MaxWaitTime time.Duration
}

func NewRequestQueue(cfg QueueConfig) *RequestQueue {
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = 1000
	}
	if cfg.MaxWaitTime <= 0 {
		cfg.MaxWaitTime = 30 * time.Second
	}

	return &RequestQueue{
		queue:       make([]*Request, 0, cfg.MaxSize),
		maxSize:     cfg.MaxSize,
		maxWaitTime: cfg.MaxWaitTime,
	}
}

func (q *RequestQueue) Enqueue(req *Request) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.queue) >= q.maxSize {
		q.droppedCount.Add(1)
		return ErrQueueFull
	}

	q.queue = append(q.queue, req)
	return nil
}

func (q *RequestQueue) Dequeue(ctx context.Context) (*Request, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.queue) == 0 {
		return nil, nil
	}

	now := time.Now()
	bestIdx := -1
	bestPriority := int(^uint(0) >> 1)

	for i, req := range q.queue {
		if now.Sub(req.Timer) > q.maxWaitTime {
			continue
		}
		if req.Priority < bestPriority {
			bestPriority = req.Priority
			bestIdx = i
		}
	}

	if bestIdx >= 0 {
		req := q.queue[bestIdx]
		q.queue = append(q.queue[:bestIdx], q.queue[bestIdx+1:]...)
		return req, nil
	}

	return nil, nil
}

func (q *RequestQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.queue)
}

func (q *RequestQueue) Cap() int {
	return q.maxSize
}

func (q *RequestQueue) DroppedCount() int64 {
	return q.droppedCount.Load()
}

func (q *RequestQueue) ProcessedCount() int64 {
	return q.processedCount.Load()
}

func (q *RequestQueue) Clear() {
	q.mu.Lock()
	defer q.mu.Unlock()

	for _, req := range q.queue {
		req.Timer = time.Time{}
	}
	q.queue = q.queue[:0]
}

func (q *RequestQueue) Process(ctx context.Context) error {
	req, err := q.Dequeue(ctx)
	if err != nil || req == nil {
		return err
	}

	if req.Execute == nil {
		return nil
	}

	err = req.Execute(ctx)
	if err == nil {
		q.processedCount.Add(1)
	} else {
		q.droppedCount.Add(1)
	}

	return err
}
