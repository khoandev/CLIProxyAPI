package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gin "github.com/gin-gonic/gin"
	proxyconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
)

// withHostMemoryLimit replaces the memory limit the strict mode derives its
// limits from and restores it afterwards.
func withHostMemoryLimit(t *testing.T, limit int64) {
	t.Helper()
	previous := hostMemoryLimit
	hostMemoryLimit = func() int64 { return limit }
	t.Cleanup(func() { hostMemoryLimit = previous })
}

// TestStrictMemoryLimitsDisabledKeepsRequestBodyUnlimited verifies that the
// documented meaning of max-request-body-bytes: 0 stays untouched when the
// strict mode is off.
func TestStrictMemoryLimitsDisabledKeepsRequestBodyUnlimited(t *testing.T) {
	withHostMemoryLimit(t, 256<<20)

	server := newTestServerWithConfig(t, parseTestServerConfigYAML(t, "max-request-body-bytes: 0\n"))
	if limit := server.maxRequestBodyBytes(); limit != 0 {
		t.Fatalf("request body limit = %d, want 0 (no limit)", limit)
	}

	server.engine.POST("/strict-off", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	body := strings.Repeat("x", 4<<20)
	req := httptest.NewRequest(http.MethodPost, "/strict-off", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; the request body must not be limited", rr.Code)
	}
}

// TestStrictMemoryLimitsDeriveRequestBodyLimit verifies that the strict mode
// derives the request body limit from the memory limit of the host and that
// the request is rejected with 413 when it exceeds the limit.
func TestStrictMemoryLimitsDeriveRequestBodyLimit(t *testing.T) {
	withHostMemoryLimit(t, 256<<20)

	server := newTestServerWithConfig(t, parseTestServerConfigYAML(t, "max-request-body-bytes: 0\nstrict-memory-limits: true\n"))
	want := int64(256<<20) / strictMemoryLimitDivisor
	if limit := server.maxRequestBodyBytes(); limit != want {
		t.Fatalf("request body limit = %d, want %d", limit, want)
	}

	server.engine.POST("/strict-on", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	body := strings.Repeat("x", int(want)+1024)
	req := httptest.NewRequest(http.MethodPost, "/strict-on", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	server.engine.ServeHTTP(rr, req)
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rr.Code)
	}
}

// TestStrictMemoryLimitsRespectConfiguredLimit verifies that an explicit
// max-request-body-bytes wins over the derived limit.
func TestStrictMemoryLimitsRespectConfiguredLimit(t *testing.T) {
	withHostMemoryLimit(t, 256<<20)

	server := newTestServerWithConfig(t, parseTestServerConfigYAML(t, "max-request-body-bytes: 4096\nstrict-memory-limits: true\n"))
	if limit := server.maxRequestBodyBytes(); limit != 4096 {
		t.Fatalf("request body limit = %d, want 4096", limit)
	}
}

// TestStrictMemoryLimitsWithoutHostLimit verifies that the strict mode does
// not limit the request body when no memory limit of the host is visible.
func TestStrictMemoryLimitsWithoutHostLimit(t *testing.T) {
	withHostMemoryLimit(t, 0)

	server := newTestServerWithConfig(t, parseTestServerConfigYAML(t, "max-request-body-bytes: 0\nstrict-memory-limits: true\n"))
	if limit := server.maxRequestBodyBytes(); limit != 0 {
		t.Fatalf("request body limit = %d, want 0 (no host limit)", limit)
	}
}

// TestStrictMemoryLimitsLowerDeferredRequestBodyLimit verifies that the strict
// mode lowers the deferred upstream request body limit and that the default is
// restored when the mode is off.
func TestStrictMemoryLimitsLowerDeferredRequestBodyLimit(t *testing.T) {
	withHostMemoryLimit(t, 64<<20)

	applyStrictMemoryLimitsConfig(&proxyconfig.Config{StrictMemoryLimits: true})
	want := int64(64<<20) / strictMemoryLimitDivisor
	if limit := logging.DeferredAPIRequestBodyMaxBytes(); limit != want {
		t.Fatalf("deferred request body limit = %d, want %d", limit, want)
	}

	applyStrictMemoryLimitsConfig(&proxyconfig.Config{})
	if limit := logging.DeferredAPIRequestBodyMaxBytes(); limit != logging.MaxDeferredAPIRequestBodyBytes {
		t.Fatalf("deferred request body limit = %d, want the default %d", limit, logging.MaxDeferredAPIRequestBodyBytes)
	}
}
