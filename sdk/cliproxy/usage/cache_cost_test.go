package usage

import "testing"

func TestRedistributeCacheReadKeepsInputTotal(t *testing.T) {
	breakdown := NewSubsetTokenBreakdown(100, 90, 0, 20, 0, 120)
	detail := Detail{
		InputTokens:     100,
		OutputTokens:    20,
		CacheReadTokens: 90,
		TotalTokens:     120,
		TokenBreakdown:  breakdown,
	}
	got := RedistributeCacheRead(detail, 0.9)

	if got.CacheReadTokens != 81 {
		t.Fatalf("cache read tokens = %d, want 81", got.CacheReadTokens)
	}
	if got.InputTokens != 100 {
		t.Fatalf("input tokens = %d, want 100 (total must not change)", got.InputTokens)
	}
	if got.TotalTokens != 120 {
		t.Fatalf("total tokens = %d, want 120", got.TotalTokens)
	}
	if got.TokenBreakdown.Input.UncachedTokens != 19 {
		t.Fatalf("uncached tokens = %d, want 19", got.TokenBreakdown.Input.UncachedTokens)
	}
	if !got.TokenBreakdown.Valid() {
		t.Fatalf("breakdown invalid after redistribution: %+v", got.TokenBreakdown)
	}
}

func TestRedistributeCacheReadCases(t *testing.T) {
	cases := []struct {
		name        string
		ratio       float64
		cacheRead   int64
		cacheWrite  int64
		wantCache   int64
		wantUncache int64
	}{
		{name: "zero ratio removes cache", ratio: 0, cacheRead: 90, wantCache: 0, wantUncache: 100},
		{name: "ratio one keeps values", ratio: 1, cacheRead: 90, wantCache: 90, wantUncache: 10},
		{name: "half splits evenly", ratio: 0.5, cacheRead: 90, wantCache: 45, wantUncache: 55},
		{name: "floor keeps remainder in uncached", ratio: 0.3, cacheRead: 90, wantCache: 27, wantUncache: 73},
		{name: "cache write is untouched", ratio: 0.5, cacheRead: 80, cacheWrite: 20, wantCache: 40, wantUncache: 40},
		{name: "negative ratio falls back to default", ratio: -1, cacheRead: 90, wantCache: 81, wantUncache: 19},
		{name: "ratio above one falls back to default", ratio: 2, cacheRead: 90, wantCache: 81, wantUncache: 19},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			detail := Detail{
				InputTokens:         100,
				CacheReadTokens:     tc.cacheRead,
				CacheCreationTokens: tc.cacheWrite,
				TokenBreakdown:      NewSubsetTokenBreakdown(100, tc.cacheRead, tc.cacheWrite, 0, 0, 100),
			}
			got := RedistributeCacheRead(detail, tc.ratio)
			if got.CacheReadTokens != tc.wantCache {
				t.Fatalf("cache read tokens = %d, want %d", got.CacheReadTokens, tc.wantCache)
			}
			if got.TokenBreakdown.Input.UncachedTokens != tc.wantUncache {
				t.Fatalf("uncached tokens = %d, want %d", got.TokenBreakdown.Input.UncachedTokens, tc.wantUncache)
			}
			if got.TokenBreakdown.Input.CacheWriteTokens != tc.cacheWrite {
				t.Fatalf("cache write tokens = %d, want %d", got.TokenBreakdown.Input.CacheWriteTokens, tc.cacheWrite)
			}
			if !got.TokenBreakdown.Valid() {
				t.Fatalf("breakdown invalid: %+v", got.TokenBreakdown)
			}
		})
	}
}

func TestRedistributeCacheReadSkipsEmptyCache(t *testing.T) {
	detail := Detail{
		InputTokens:    100,
		TokenBreakdown: NewSubsetTokenBreakdown(100, 0, 0, 0, 0, 100),
	}
	got := RedistributeCacheRead(detail, 0.9)
	if got.TokenBreakdown.Input.UncachedTokens != 100 {
		t.Fatalf("uncached tokens = %d, want 100", got.TokenBreakdown.Input.UncachedTokens)
	}
}

func TestRedistributeCacheReadWithoutBreakdown(t *testing.T) {
	detail := Detail{InputTokens: 100, CachedTokens: 90, CacheReadTokens: 90}
	got := RedistributeCacheRead(detail, 0.5)
	if got.CachedTokens != 45 {
		t.Fatalf("cached tokens = %d, want 45", got.CachedTokens)
	}
	if got.CacheReadTokens != 90 {
		t.Fatalf("cache read tokens = %d, want 90 (untouched without a valid breakdown)", got.CacheReadTokens)
	}
}
