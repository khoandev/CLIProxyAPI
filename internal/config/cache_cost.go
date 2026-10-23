package config

import (
	"github.com/router-for-me/CLIProxyAPI/v8/internal/cachecost"
)

// normalizeCacheHitCostRatio resolves the configured cache-read pricing ratio.
// An absent value falls back to the default; an out-of-range value is clamped
// so a typo cannot silently disable redistribution. Zero is valid and means
// cache-read tokens are not discounted at all.
func normalizeCacheHitCostRatio(value *float64) *float64 {
	if value == nil {
		defaultRatio := cachecost.Default
		return &defaultRatio
	}
	resolved := cachecost.Normalize(*value)
	return &resolved
}

// CacheHitCostRatioValue returns the effective cache-read pricing ratio.
// It falls back to the default when the value was never normalized.
func (c *Config) CacheHitCostRatioValue() float64 {
	if c == nil || c.CacheHitCostRatio == nil {
		return cachecost.Default
	}
	return *c.CacheHitCostRatio
}
