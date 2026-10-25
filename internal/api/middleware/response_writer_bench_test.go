package middleware

import (
	"net/http"
	"testing"
)

// benchmarkResponseWriterWrite measures the logging wrapper's per-chunk cost
// when the logger is disabled and nothing needs to be captured.
func benchmarkResponseWriterWrite(b *testing.B, write func(w *ResponseWriterWrapper) error) {
	b.Helper()
	c := newDiscardGinWriter()
	wrapper := NewResponseWriterWrapper(c.Writer, &testRequestLogger{enabled: false}, &RequestInfo{
		URL:     "/v1/chat/completions",
		Method:  http.MethodPost,
		Headers: map[string][]string{"Content-Type": {"application/json"}},
	})
	wrapper.WriteHeader(http.StatusOK)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for range 2000 {
			if err := write(wrapper); err != nil {
				b.Fatalf("write: %v", err)
			}
		}
	}
}

// BenchmarkResponseWriterPassthrough covers the io.Writer path of the wrapper
// with request logging disabled.
func BenchmarkResponseWriterPassthrough(b *testing.B) {
	chunk := benchChunk()
	benchmarkResponseWriterWrite(b, func(w *ResponseWriterWrapper) error {
		_, err := w.Write(chunk)
		return err
	})
}

// BenchmarkResponseWriterWriteStringPassthrough covers the io.StringWriter
// path of the wrapper with request logging disabled.
func BenchmarkResponseWriterWriteStringPassthrough(b *testing.B) {
	chunk := string(benchChunk())
	benchmarkResponseWriterWrite(b, func(w *ResponseWriterWrapper) error {
		_, err := w.WriteString(chunk)
		return err
	})
}

// benchmarkResponseWriterPerResponse measures one complete wrapped response:
// the wrapper is constructed, the status is written and one chunk is sent.
//
// The wrapper snapshots the response headers once per response, so the cost of
// that snapshot only shows up when the wrapper is built per response rather
// than reused across chunks.
func benchmarkResponseWriterPerResponse(b *testing.B, loggerEnabled bool) {
	b.Helper()
	c := newDiscardGinWriter()
	chunk := benchChunk()
	logger := &testRequestLogger{enabled: loggerEnabled}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		wrapper := NewResponseWriterWrapper(c.Writer, logger, &RequestInfo{
			URL:     "/v1/chat/completions",
			Method:  http.MethodPost,
			Headers: map[string][]string{"Content-Type": {"application/json"}},
		})
		wrapper.WriteHeader(http.StatusOK)
		if _, err := wrapper.Write(chunk); err != nil {
			b.Fatalf("Write: %v", err)
		}
	}
}

// BenchmarkResponseWriterWrapperHeaderCapture measures the header snapshot the
// logging wrapper takes once per response while request logging is enabled.
func BenchmarkResponseWriterWrapperHeaderCapture(b *testing.B) {
	benchmarkResponseWriterPerResponse(b, true)
}

// BenchmarkResponseWriterResponseOverhead is the same wrapped response with
// request logging disabled, where the header snapshot has no consumer.
func BenchmarkResponseWriterResponseOverhead(b *testing.B) {
	benchmarkResponseWriterPerResponse(b, false)
}
