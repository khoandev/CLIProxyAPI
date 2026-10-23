package middleware

import (
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
)

// benchmarkStreamingChunkPath drives 2000 SSE chunks per iteration through a
// freshly built logging wrapper, so the per-chunk cost of the request-log path
// (copies, channel hops, temp-file writes, final log assembly) is measurable.
// The sink is discardResponseWriter: httptest.ResponseRecorder grows a
// bytes.Buffer per chunk and would swamp the logging delta.
func benchmarkStreamingChunkPath(b *testing.B, newLogger func() logging.RequestLogger) {
	chunk := benchChunk()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		b.StopTimer()
		c := newDiscardGinWriter()
		wrapper := NewResponseWriterWrapper(c.Writer, newLogger(), &RequestInfo{
			URL:     "/v1/chat/completions",
			Method:  http.MethodPost,
			Headers: map[string][]string{"Content-Type": {"application/json"}},
		})
		c.Writer = wrapper
		wrapper.WriteHeader(http.StatusOK)
		b.StartTimer()

		for j := range 2000 {
			if _, errWrite := wrapper.Write(chunk); errWrite != nil {
				b.Fatalf("write chunk %d: %v", j, errWrite)
			}
		}
		if errFinalize := wrapper.Finalize(c); errFinalize != nil {
			b.Fatalf("finalize: %v", errFinalize)
		}
	}
}

// BenchmarkStreamingChunkPathLoggerEnabled measures the streaming path with a
// real file request logger.
func BenchmarkStreamingChunkPathLoggerEnabled(b *testing.B) {
	logsDir := b.TempDir()
	benchmarkStreamingChunkPath(b, func() logging.RequestLogger {
		return logging.NewFileRequestLogger(true, logsDir, "", 0)
	})
}

// BenchmarkStreamingChunkPathLoggerDisabled measures the streaming path when
// request logging is off; no stream writer is created.
func BenchmarkStreamingChunkPathLoggerDisabled(b *testing.B) {
	benchmarkStreamingChunkPath(b, func() logging.RequestLogger {
		return &testRequestLogger{enabled: false}
	})
}
