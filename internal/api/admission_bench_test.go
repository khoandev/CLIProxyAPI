package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// benchmarkAdmissionEngine drives one POST through engine and reports the
// per-request cost of the whole chain.
func benchmarkAdmissionEngine(b *testing.B, engine *gin.Engine) {
	b.Helper()
	body := bytes.Repeat([]byte(`{"model":"gpt-5.1"}`), 56) // ~1 KiB
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		req := httptest.NewRequest(http.MethodPost, "/admission-bench", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusOK {
			b.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
		}
	}
}

// BenchmarkInFlightAdmission measures the admission middleware on an accepted
// request. Compare it with BenchmarkInFlightAdmissionBare: the difference is
// the middleware's own cost, which must be zero allocations.
func BenchmarkInFlightAdmission(b *testing.B) {
	// The server logs one info line per request; silence it so the benchmark
	// measures the middleware and not the logger.
	previousLevel := log.GetLevel()
	log.SetLevel(log.PanicLevel)
	b.Cleanup(func() { log.SetLevel(previousLevel) })

	server := newTestServerWithConfig(b, parseTestServerConfigYAML(b, "max-in-flight-requests: 64\n"))
	engine := gin.New()
	engine.Use(server.inFlightAdmissionMiddleware())
	engine.POST("/admission-bench", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	benchmarkAdmissionEngine(b, engine)
}

// BenchmarkInFlightAdmissionBare is the same chain without the admission
// middleware, so the two runs bracket the middleware's own allocation cost.
func BenchmarkInFlightAdmissionBare(b *testing.B) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/admission-bench", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	benchmarkAdmissionEngine(b, engine)
}
