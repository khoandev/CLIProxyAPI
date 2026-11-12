package api

import (
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/memlimit"
)

const (
	// strictMemoryLimitDivisor divides the memory limit of the host into the
	// share one request body may use. The rest of the limit covers the
	// concurrent requests, the runtime metadata and the buffers of the
	// upstream connections.
	strictMemoryLimitDivisor = 4

	// strictMemoryLimitFloor is the smallest request body limit the strict
	// mode derives. Below it a multimodal request cannot be served.
	strictMemoryLimitFloor = 8 << 20

	// strictMemoryLimitCeiling is the largest request body limit the strict
	// mode derives. Above it the limit would not protect a small host
	// anymore.
	strictMemoryLimitCeiling = 256 << 20
)

// hostMemoryLimit reports the memory limit of the host. The tests replace it
// to drive the derived limits.
var hostMemoryLimit = memlimit.Limit

// strictMemoryLimitsEnabled reports whether the strict memory limits are on.
func strictMemoryLimitsEnabled(cfg *config.Config) bool {
	return cfg != nil && cfg.StrictMemoryLimits
}

// derivedRequestBodyBytes returns the request body limit derived from the
// memory limit of the host. It returns zero when no memory limit is visible,
// so the request body stays unlimited.
func derivedRequestBodyBytes() int64 {
	limit := hostMemoryLimit()
	if limit <= 0 {
		return 0
	}
	derived := limit / strictMemoryLimitDivisor
	if derived < strictMemoryLimitFloor {
		derived = strictMemoryLimitFloor
	}
	if derived > strictMemoryLimitCeiling {
		derived = strictMemoryLimitCeiling
	}
	return derived
}

// applyStrictMemoryLimitsConfig applies the limits that the strict mode
// derives from the memory limit of the host.
//
// The mode is opt in: without it the request body stays unlimited and the
// deferred upstream request bodies keep their default limit.
func applyStrictMemoryLimitsConfig(cfg *config.Config) {
	if !strictMemoryLimitsEnabled(cfg) {
		logging.SetDeferredAPIRequestBodyMaxBytes(0)
		return
	}
	derived := derivedRequestBodyBytes()
	if derived <= 0 {
		logging.SetDeferredAPIRequestBodyMaxBytes(0)
		return
	}
	deferred := derived
	if deferred > logging.MaxDeferredAPIRequestBodyBytes {
		deferred = logging.MaxDeferredAPIRequestBodyBytes
	}
	logging.SetDeferredAPIRequestBodyMaxBytes(deferred)
}
