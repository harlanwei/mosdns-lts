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
	"container/heap"
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
	index    int // index in the heap, maintained by heap.Interface
}

// requestHeap implements heap.Interface for priority queue
type requestHeap []*Request

func (h requestHeap) Len() int { return len(h) }

func (h requestHeap) Less(i, j int) bool {
	return h[i].Priority < h[j].Priority
}

func (h requestHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *requestHeap) Push(x any) {
	n := len(*h)
	req := x.(*Request)
	req.index = n
	*h = append(*h, req)
}

func (h *requestHeap) Pop() any {
	old := *h
	n := len(old)
	req := old[n-1]
	old[n-1] = nil
	req.index = -1
	*h = old[:n-1]
	return req
}

type RequestQueue struct {
	mu sync.Mutex

	heap        requestHeap
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
		heap:        make(requestHeap, 0, cfg.MaxSize),
		maxSize:     cfg.MaxSize,
		maxWaitTime: cfg.MaxWaitTime,
	}
}

func (q *RequestQueue) Enqueue(req *Request) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.heap) >= q.maxSize {
		q.droppedCount.Add(1)
		return ErrQueueFull
	}

	heap.Push(&q.heap, req)
	return nil
}

func (q *RequestQueue) Dequeue(ctx context.Context) (*Request, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := time.Now()

	// Remove expired or invalid items from the top of the heap
	for len(q.heap) > 0 {
		req := q.heap[0]
		if now.Sub(req.Timer) > q.maxWaitTime {
			heap.Pop(&q.heap)
			continue
		}
		// Found a valid request
		heap.Pop(&q.heap)
		return req, nil
	}

	return nil, nil
}

func (q *RequestQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.heap)
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

	for _, req := range q.heap {
		req.Timer = time.Time{}
	}
	q.heap = q.heap[:0]
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
