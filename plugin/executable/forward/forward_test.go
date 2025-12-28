package fastforward

import (
	"sync/atomic"
	"testing"
)

func TestSelectUpstreams(t *testing.T) {
	f := &Forward{
		us: []*upstreamWrapper{
			{emaLatency: atomic.Int64{}},
			{emaLatency: atomic.Int64{}},
			{emaLatency: atomic.Int64{}},
			{emaLatency: atomic.Int64{}},
		},
	}

	f.us[0].emaLatency.Store(50)
	f.us[1].emaLatency.Store(100)
	f.us[2].emaLatency.Store(200)
	f.us[3].emaLatency.Store(400)

	selectionCount := make(map[int]int)
	iterations := 10000

	for i := 0; i < iterations; i++ {
		selected := f.selectUpstreams(f.us, 1)
		if len(selected) != 1 {
			t.Fatalf("expected 1 selection, got %d", len(selected))
		}
		selectionCount[selected[0]]++
	}

	t.Logf("Selection distribution (lower latency should be selected more often):")
	for i := 0; i < len(f.us); i++ {
		t.Logf("  Upstream %d (latency %dms): %d times (%.2f%%)",
			i, f.us[i].emaLatency.Load(), selectionCount[i],
			float64(selectionCount[i])/float64(iterations)*100)
	}

	if selectionCount[0] < selectionCount[3] {
		t.Errorf("Upstream 0 (faster) should be selected more often than Upstream 3 (slower)")
	}
}

func TestSelectUpstreamsWithNoise(t *testing.T) {
	f := &Forward{
		us: []*upstreamWrapper{
			{emaLatency: atomic.Int64{}},
			{emaLatency: atomic.Int64{}},
			{emaLatency: atomic.Int64{}},
		},
	}

	f.us[0].emaLatency.Store(10)
	f.us[1].emaLatency.Store(1000)
	f.us[2].emaLatency.Store(2000)

	selected := make(map[int]bool)
	for i := 0; i < 1000; i++ {
		indices := f.selectUpstreams(f.us, 1)
		selected[indices[0]] = true
	}

	if len(selected) < 3 {
		t.Errorf("All upstreams should be selected occasionally (noise factor). Got %d unique selections", len(selected))
	}
}

func TestSelectUpstreamsMultiple(t *testing.T) {
	f := &Forward{
		us: []*upstreamWrapper{
			{emaLatency: atomic.Int64{}},
			{emaLatency: atomic.Int64{}},
			{emaLatency: atomic.Int64{}},
			{emaLatency: atomic.Int64{}},
		},
	}

	for i := range f.us {
		f.us[i].emaLatency.Store(int64(50 * (i + 1)))
	}

	indices := f.selectUpstreams(f.us, 2)
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
	f := &Forward{
		us: []*upstreamWrapper{
			{emaLatency: atomic.Int64{}},
			{emaLatency: atomic.Int64{}},
			{emaLatency: atomic.Int64{}},
		},
	}

	indices := f.selectUpstreams(f.us, 10)
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
	f := &Forward{
		us: []*upstreamWrapper{
			{emaLatency: atomic.Int64{}},
			{emaLatency: atomic.Int64{}},
		},
	}

	f.us[1].emaLatency.Store(100)

	selected := f.selectUpstreams(f.us, 1)
	if len(selected) != 1 {
		t.Fatalf("expected 1 selection, got %d", len(selected))
	}

	if selected[0] < 0 || selected[0] >= len(f.us) {
		t.Errorf("invalid upstream index: %d", selected[0])
	}
}

func TestSelectUpstreamsWithErrorPenalty(t *testing.T) {
	f := &Forward{
		us: []*upstreamWrapper{
			{emaLatency: atomic.Int64{}, queryCount: atomic.Int64{}, errorCount: atomic.Int64{}},
			{emaLatency: atomic.Int64{}, queryCount: atomic.Int64{}, errorCount: atomic.Int64{}},
		},
	}

	f.us[0].emaLatency.Store(50)
	f.us[0].queryCount.Store(100)
	f.us[0].errorCount.Store(0)

	f.us[1].emaLatency.Store(50)
	f.us[1].queryCount.Store(100)
	f.us[1].errorCount.Store(50)

	selectionCount := make(map[int]int)
	iterations := 10000

	for i := 0; i < iterations; i++ {
		selected := f.selectUpstreams(f.us, 1)
		if len(selected) != 1 {
			t.Fatalf("expected 1 selection, got %d", len(selected))
		}
		selectionCount[selected[0]]++
	}

	t.Logf("Selection distribution (upstream with errors should be selected less often):")
	for i := 0; i < len(f.us); i++ {
		errorRate := float64(f.us[i].errorCount.Load()) / float64(f.us[i].queryCount.Load())
		t.Logf("  Upstream %d (latency %dms, error rate %.2f%%): %d times (%.2f%%)",
			i, f.us[i].emaLatency.Load(), errorRate*100, selectionCount[i],
			float64(selectionCount[i])/float64(iterations)*100)
	}

	if selectionCount[0] <= selectionCount[1] {
		t.Errorf("Upstream 0 (no errors) should be selected more often than Upstream 1 (50%% errors)")
	}
}
