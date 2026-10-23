package usage

import (
	"github.com/router-for-me/CLIProxyAPI/v8/internal/cachecost"
)

// RedistributeCacheRead rescales cache-read tokens so that they keep only the
// configured share of their cache price. The remainder is added back to the
// uncached input bucket, which raises the effective cost. Cache-write tokens and
// every total are left unchanged, so the breakdown stays within the v2 accounting
// invariants. A ratio of 1 leaves the detail untouched.
func RedistributeCacheRead(detail Detail, ratio float64) Detail {
	cacheRead := detail.CacheReadTokens
	if cachecost.Normalize(ratio) >= 1 || cacheRead <= 0 {
		return detail
	}
	if detail.TokenBreakdown.Valid() {
		detail.CacheReadTokens = cachecost.ScaledCacheTokens(cacheRead, ratio)
		detail.TokenBreakdown.Input.CacheReadTokens = detail.CacheReadTokens
		detail.TokenBreakdown.Input.UncachedTokens = detail.TokenBreakdown.Input.TotalTokens -
			detail.TokenBreakdown.Input.CacheReadTokens - detail.TokenBreakdown.Input.CacheWriteTokens
		return detail
	}
	detail.CachedTokens = cachecost.ScaledCacheTokens(detail.CachedTokens, ratio)
	return detail
}
