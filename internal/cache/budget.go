package cache

import (
	"sync/atomic"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/memlimit"
)

// CacheBudgetMaxBytes is the sum of the per cache defaults. It is the largest
// budget the caches may use, so a host without a visible memory limit keeps
// the current limits.
const CacheBudgetMaxBytes = SignatureCacheMaxTotalBytes +
	CodexReasoningReplayCacheMaxTotalBytes +
	KimiThinkingReplayCacheMaxTotalBytes +
	XAIReasoningReplayCacheMaxTotalBytes +
	ClaudeThinkingReplayCacheMaxTotalBytes +
	AntigravityReasoningReplayCacheMaxTotalBytes +
	claudeWebSearchResultMaxTotalBytes

const (
	// CacheBudgetLimitDivisor divides the memory limit into the share the
	// caches may use. The rest of the limit covers the request handling, the
	// runtime metadata and the buffers that do not belong to a cache.
	CacheBudgetLimitDivisor = 8

	// CacheBudgetMinBytes is the smallest budget the caches get when a memory
	// limit is visible. Below it the replay caches stop working, so the
	// caches keep a workable floor.
	CacheBudgetMinBytes = 32 << 20
)

// cacheBudgetBytes holds the budget an operator configured. Zero means the
// budget is derived from the memory limit.
var cacheBudgetBytes atomic.Int64

// cacheMemoryLimitFunc reports the memory limit of the host. The tests replace
// it to drive the budget without a container.
var cacheMemoryLimitFunc = memlimit.Limit

// SetCacheBudget sets the byte budget the caches may use. A value of zero
// restores the automatic budget, which is derived from the memory limit.
func SetCacheBudget(bytes int64) {
	if bytes < 0 {
		bytes = 0
	}
	cacheBudgetBytes.Store(bytes)
}

// CacheBudget returns the byte budget the caches may use. It returns zero when
// no budget applies, which keeps the per cache defaults unchanged.
func CacheBudget() int64 {
	budget := cacheBudgetBytes.Load()
	if budget <= 0 {
		limit := cacheMemoryLimitFunc()
		if limit <= 0 {
			return 0
		}
		budget = limit / CacheBudgetLimitDivisor
	}
	if budget < CacheBudgetMinBytes {
		budget = CacheBudgetMinBytes
	}
	if budget > CacheBudgetMaxBytes {
		budget = CacheBudgetMaxBytes
	}
	return budget
}

// ScaleCacheLimit scales a per cache default to the share of the cache budget
// that the cache may use.
//
// The per cache defaults are the largest share of the budget, so the result
// never exceeds the default and the sum of the scaled defaults stays inside
// the budget.
func ScaleCacheLimit(defaultLimit int64) int64 {
	budget := CacheBudget()
	if budget <= 0 || budget >= CacheBudgetMaxBytes {
		return defaultLimit
	}
	scaled := defaultLimit * budget / CacheBudgetMaxBytes
	if scaled < 1 {
		scaled = 1
	}
	return scaled
}
