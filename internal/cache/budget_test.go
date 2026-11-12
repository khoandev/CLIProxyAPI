package cache

import (
	"testing"
)

// withCacheMemoryLimit replaces the memory limit the budget is derived from
// and restores it afterwards.
func withCacheMemoryLimit(t *testing.T, limit int64) {
	t.Helper()
	previousLimit := cacheMemoryLimitFunc
	previousBudget := cacheBudgetBytes.Load()
	cacheMemoryLimitFunc = func() int64 { return limit }
	cacheBudgetBytes.Store(0)
	t.Cleanup(func() {
		cacheMemoryLimitFunc = previousLimit
		cacheBudgetBytes.Store(previousBudget)
	})
}

// TestCacheBudgetFollowsMemoryLimit verifies that the budget follows the
// memory limit of the host and that the per cache defaults stay in place when
// no limit is visible.
func TestCacheBudgetFollowsMemoryLimit(t *testing.T) {
	withCacheMemoryLimit(t, 0)
	if budget := CacheBudget(); budget != 0 {
		t.Fatalf("budget without a memory limit = %d, want 0", budget)
	}
	if limit := ScaleCacheLimit(CodexReasoningReplayCacheMaxTotalBytes); limit != CodexReasoningReplayCacheMaxTotalBytes {
		t.Fatalf("scaled limit without a budget = %d, want %d", limit, CodexReasoningReplayCacheMaxTotalBytes)
	}

	withCacheMemoryLimit(t, 512<<20)
	want := int64(512<<20) / CacheBudgetLimitDivisor
	if budget := CacheBudget(); budget != want {
		t.Fatalf("budget for a 512 MiB limit = %d, want %d", budget, want)
	}
	if want >= CacheBudgetMaxBytes {
		t.Fatalf("test setup: the 512 MiB budget must be below the maximum")
	}
	if limit := ScaleCacheLimit(CodexReasoningReplayCacheMaxTotalBytes); limit >= CodexReasoningReplayCacheMaxTotalBytes {
		t.Fatalf("scaled limit = %d, want less than %d", limit, CodexReasoningReplayCacheMaxTotalBytes)
	}

	withCacheMemoryLimit(t, 256<<20)
	want = int64(256<<20) / CacheBudgetLimitDivisor
	if budget := CacheBudget(); budget != want {
		t.Fatalf("budget for a 256 MiB limit = %d, want %d", budget, want)
	}
}

// TestCacheBudgetHasFloorAndCeiling verifies that the budget stays inside the
// documented bounds.
func TestCacheBudgetHasFloorAndCeiling(t *testing.T) {
	withCacheMemoryLimit(t, 16<<20)
	if budget := CacheBudget(); budget != CacheBudgetMinBytes {
		t.Fatalf("budget for a tiny limit = %d, want the floor %d", budget, CacheBudgetMinBytes)
	}

	withCacheMemoryLimit(t, 64<<30)
	if budget := CacheBudget(); budget != CacheBudgetMaxBytes {
		t.Fatalf("budget for a huge limit = %d, want the ceiling %d", budget, CacheBudgetMaxBytes)
	}
}

// TestCacheBudgetKeepsCachesInsideBudget verifies that the scaled limits of
// the caches stay inside the budget.
func TestCacheBudgetKeepsCachesInsideBudget(t *testing.T) {
	withCacheMemoryLimit(t, 256<<20)
	budget := CacheBudget()
	if budget <= 0 {
		t.Fatal("budget must be positive")
	}

	total := int64(0)
	total += ScaleCacheLimit(SignatureCacheMaxTotalBytes)
	total += ScaleCacheLimit(CodexReasoningReplayCacheMaxTotalBytes)
	total += ScaleCacheLimit(KimiThinkingReplayCacheMaxTotalBytes)
	total += ScaleCacheLimit(XAIReasoningReplayCacheMaxTotalBytes)
	total += ScaleCacheLimit(ClaudeThinkingReplayCacheMaxTotalBytes)
	total += ScaleCacheLimit(AntigravityReasoningReplayCacheMaxTotalBytes)
	total += ScaleCacheLimit(claudeWebSearchResultMaxTotalBytes)

	if total > budget {
		t.Fatalf("scaled cache limits = %d bytes, want at most the budget %d", total, budget)
	}
	if total == 0 {
		t.Fatal("scaled cache limits must not be empty")
	}
}

// TestCacheBudgetExplicitOverride verifies that an operator can set the
// budget and that it is capped at the maximum.
func TestCacheBudgetExplicitOverride(t *testing.T) {
	withCacheMemoryLimit(t, 512<<20)

	SetCacheBudget(64 << 20)
	if budget := CacheBudget(); budget != 64<<20 {
		t.Fatalf("explicit budget = %d, want %d", budget, int64(64<<20))
	}

	SetCacheBudget(8 << 30)
	if budget := CacheBudget(); budget != CacheBudgetMaxBytes {
		t.Fatalf("explicit budget above the maximum = %d, want %d", budget, int64(CacheBudgetMaxBytes))
	}

	SetCacheBudget(0)
	if budget := CacheBudget(); budget != int64(512<<20)/CacheBudgetLimitDivisor {
		t.Fatalf("budget after the reset = %d, want the automatic budget", budget)
	}
}
