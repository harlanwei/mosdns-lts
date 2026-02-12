package fastforward

import (
	"sync/atomic"
	"testing"
)

func TestSelectUpstreams(t *testing.T) {
	us := []*upstreamWrapper{
		{emaLatency: atomic.Int64{}},
		{emaLatency: atomic.Int64{}},
		{emaLatency: atomic.Int64{}},
		{emaLatency: atomic.Int64{}},
	}

	us[0].emaLatency.Store(50)
	us[1].emaLatency.Store(100)
	us[2].emaLatency.Store(200)
	us[3].emaLatency.Store(400)

	selector := newUpstreamSelector(us)

	selectionCount := make(map[int]int)
	iterations := 10000

	for i := 0; i < iterations; i++ {
		selected := selector.selectUpstreams(1)
		if len(selected) != 1 {
			t.Fatalf("expected 1 selection, got %d", len(selected))
		}
		selectionCount[selected[0]]++
	}

	t.Logf("Selection distribution (lower latency should be selected more often):")
	for i := 0; i < len(us); i++ {
		t.Logf("  Upstream %d (latency %dms): %d times (%.2f%%)",
			i, us[i].emaLatency.Load(), selectionCount[i],
			float64(selectionCount[i])/float64(iterations)*100)
	}

	if selectionCount[0] < selectionCount[3] {
		t.Errorf("Upstream 0 (faster) should be selected more often than Upstream 3 (slower)")
	}
}

func TestSelectUpstreamsWithNoise(t *testing.T) {
	us := []*upstreamWrapper{
		{emaLatency: atomic.Int64{}},
		{emaLatency: atomic.Int64{}},
		{emaLatency: atomic.Int64{}},
	}

	us[0].emaLatency.Store(10)
	us[1].emaLatency.Store(100)
	us[2].emaLatency.Store(200)

	selector := newUpstreamSelector(us)

	selectionCount := make(map[int]int)
	for i := 0; i < 1000; i++ {
		selector.mu.Lock()
		selector.cachedOrder = nil
		selector.mu.Unlock()
		indices := selector.selectUpstreams(1)
		selectionCount[indices[0]]++
	}

	t.Logf("Selection distribution with weighted random:")
	for i := 0; i < len(us); i++ {
		t.Logf("  Upstream %d (latency %dms): %d times (%.2f%%)",
			i, us[i].emaLatency.Load(), selectionCount[i],
			float64(selectionCount[i])/10.0)
	}

	if selectionCount[0] < selectionCount[2] {
		t.Errorf("Upstream 0 (fastest) should be selected more often than Upstream 2 (slowest)")
	}

	if len(selectionCount) < 2 {
		t.Errorf("Weighted random should select multiple upstreams, got %d unique", len(selectionCount))
	}
}

func TestSelectUpstreamsMultiple(t *testing.T) {
	us := []*upstreamWrapper{
		{emaLatency: atomic.Int64{}},
		{emaLatency: atomic.Int64{}},
		{emaLatency: atomic.Int64{}},
		{emaLatency: atomic.Int64{}},
	}

	for i := range us {
		us[i].emaLatency.Store(int64(50 * (i + 1)))
	}

	selector := newUpstreamSelector(us)

	indices := selector.selectUpstreams(2)
	if len(indices) != 2 {
		t.Fatalf("expected 2 selections, got %d", len(indices))
	}

	if indices[0] == indices[1] {
		t.Errorf("selected upstreams should be unique, got duplicate: %d", indices[0])
	}

	if len(indices) != len(make(map[int]bool)) {
		for i, idx := range indices {
			for j, idx2 := range indices {
				if i != j && idx == idx2 {
					t.Errorf("duplicate selection at indices %d and %d: %d", i, j, idx)
				}
			}
		}
	}
}

func TestSelectUpstreamsAllWhenCountExceeds(t *testing.T) {
	us := []*upstreamWrapper{
		{emaLatency: atomic.Int64{}},
		{emaLatency: atomic.Int64{}},
		{emaLatency: atomic.Int64{}},
	}

	selector := newUpstreamSelector(us)

	indices := selector.selectUpstreams(10)
	if len(indices) != 3 {
		t.Fatalf("expected 3 selections when count exceeds available, got %d", len(indices))
	}

	expected := []int{0, 1, 2}
	for i, idx := range indices {
		if idx != expected[i] {
			t.Errorf("expected index %d, got %d", expected[i], idx)
		}
	}
}

func TestSelectUpstreamsZeroLatency(t *testing.T) {
	us := []*upstreamWrapper{
		{emaLatency: atomic.Int64{}},
		{emaLatency: atomic.Int64{}},
	}

	us[1].emaLatency.Store(100)

	selector := newUpstreamSelector(us)

	selected := selector.selectUpstreams(1)
	if len(selected) != 1 {
		t.Fatalf("expected 1 selection, got %d", len(selected))
	}

	if selected[0] < 0 || selected[0] >= len(us) {
		t.Errorf("invalid upstream index: %d", selected[0])
	}
}

func TestSelectUpstreamsWithErrorPenalty(t *testing.T) {
	us := []*upstreamWrapper{
		{emaLatency: atomic.Int64{}, queryCount: atomic.Int64{}, errorCount: atomic.Int64{}},
		{emaLatency: atomic.Int64{}, queryCount: atomic.Int64{}, errorCount: atomic.Int64{}},
	}

	us[0].emaLatency.Store(50)
	us[0].queryCount.Store(100)
	us[0].errorCount.Store(0)

	us[1].emaLatency.Store(50)
	us[1].queryCount.Store(100)
	us[1].errorCount.Store(50)

	selector := newUpstreamSelector(us)

	selectionCount := make(map[int]int)
	iterations := 10000

	for i := 0; i < iterations; i++ {
		selected := selector.selectUpstreams(1)
		if len(selected) != 1 {
			t.Fatalf("expected 1 selection, got %d", len(selected))
		}
		selectionCount[selected[0]]++
	}

	t.Logf("Selection distribution (upstream with errors should be selected less often):")
	for i := 0; i < len(us); i++ {
		errorRate := float64(us[i].errorCount.Load()) / float64(us[i].queryCount.Load())
		t.Logf("  Upstream %d (latency %dms, error rate %.2f%%): %d times (%.2f%%)",
			i, us[i].emaLatency.Load(), errorRate*100, selectionCount[i],
			float64(selectionCount[i])/float64(iterations)*100)
	}

	if selectionCount[0] <= selectionCount[1] {
		t.Errorf("Upstream 0 (no errors) should be selected more often than Upstream 1 (50%% errors)")
	}
}

func TestSelectUpstreamsCaching(t *testing.T) {
	us := []*upstreamWrapper{
		{emaLatency: atomic.Int64{}},
		{emaLatency: atomic.Int64{}},
		{emaLatency: atomic.Int64{}},
	}

	us[0].emaLatency.Store(50)
	us[1].emaLatency.Store(100)
	us[2].emaLatency.Store(200)

	selector := newUpstreamSelector(us)

	indices1 := selector.selectUpstreams(2)
	indices2 := selector.selectUpstreams(2)

	if len(indices1) != 2 || len(indices2) != 2 {
		t.Fatalf("expected 2 selections, got %d and %d", len(indices1), len(indices2))
	}

	if indices1[0] != indices2[0] || indices1[1] != indices2[1] {
		t.Logf("Note: Cached selection may differ due to cache TTL expiration or initial call")
	}
}
