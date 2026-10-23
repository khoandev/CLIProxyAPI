// Package cachecost redistributes cache-read tokens so that reported cache
// savings can be scaled independently from the price table applied downstream.
//
// It intentionally depends on the standard library only, so both the internal
// usage pipeline and the translator layer can share it without import cycles.
package cachecost

import (
	"math"
	"sync/atomic"
)

// Default is the ratio used when configuration omits a valid value.
const Default = 0.9

// ratio holds the configured ratio as float64 bits. ratioConfigured separates an
// explicit zero from an unset value, which share the same bit pattern.
var (
	ratio           atomic.Uint64
	ratioConfigured atomic.Bool
)

// SetRatio configures how much of the cache price cache-read tokens keep when
// reported. Values outside [0, 1] fall back to Default.
func SetRatio(value float64) {
	ratioConfigured.Store(true)
	ratio.Store(math.Float64bits(Normalize(value)))
}

// Ratio reports the active ratio. It returns 1 when no ratio was ever set,
// which makes every redistribution a no-op.
func Ratio() float64 {
	if !ratioConfigured.Load() {
		return 1
	}
	return math.Float64frombits(ratio.Load())
}

// Normalize keeps only ratios in [0, 1]. A ratio of 1 is a no-op and 0 removes
// cache-read tokens entirely; anything invalid falls back to Default.
func Normalize(value float64) float64 {
	if math.IsNaN(value) || value < 0 || value > 1 {
		return Default
	}
	return value
}

// ScaledCacheTokens floors the scaled cache-read bucket. The rounding remainder
// stays in the uncached bucket, so the input and total token counts never change.
func ScaledCacheTokens(tokens int64, value float64) int64 {
	if tokens <= 0 {
		return 0
	}
	scaled := int64(math.Floor(float64(tokens) * Normalize(value)))
	if scaled < 0 {
		return 0
	}
	if scaled > tokens {
		return tokens
	}
	return scaled
}
