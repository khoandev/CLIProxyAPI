package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// benchChunk is a clean SSE-sized chunk: it carries no masking token, so the
// masking writer must forward it without rewriting or allocating.
func benchChunk() []byte {
	chunk := make([]byte, 1024)
	for i := range chunk {
		chunk[i] = byte('a' + i%26)
	}
	return chunk
}

// benchWriteChunks writes 2000 chunks through write and reports allocations.
func benchWriteChunks(b *testing.B, write func([]byte) (int, error)) {
	b.Helper()
	chunk := benchChunk()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		for j := range 2000 {
			if _, err := write(chunk); err != nil {
				b.Fatalf("write chunk %d: %v", j, err)
			}
		}
	}
}

func newBenchGinWriter() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	return c, rec
}

func benchmarkMasking(b *testing.B, set maskRuleSet) {
	c, _ := newBenchGinWriter()
	w := &maskingResponseWriter{ResponseWriter: c.Writer, set: set}
	c.Writer = w
	benchWriteChunks(b, w.Write)
}

// discardResponseWriter drops the body so a benchmark measures the masking
// writer alone. httptest.ResponseRecorder grows a bytes.Buffer per write, which
// swamps the masking delta with buffer-doubling allocations.
type discardResponseWriter struct {
	gin.ResponseWriter
}

func (w discardResponseWriter) Write(b []byte) (int, error)       { return len(b), nil }
func (w discardResponseWriter) WriteString(s string) (int, error) { return len(s), nil }

func newDiscardGinWriter() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := newBenchGinWriter()
	c.Writer = discardResponseWriter{ResponseWriter: c.Writer}
	return c
}

func benchmarkMaskingDiscard(b *testing.B, set maskRuleSet) {
	b.Helper()
	c := newDiscardGinWriter()
	w := &maskingResponseWriter{ResponseWriter: c.Writer, set: set}
	c.Writer = w
	benchWriteChunks(b, w.Write)
}

// BenchmarkMaskingDiscardBrandOnly measures the brand-only path with no
// recorder buffer in the way.
func BenchmarkMaskingDiscardBrandOnly(b *testing.B) {
	benchmarkMaskingDiscard(b, brandOnlySet)
}

// BenchmarkMaskingDiscardClaude measures brand + kiro without recorder noise.
func BenchmarkMaskingDiscardClaude(b *testing.B) {
	benchmarkMaskingDiscard(b, brandClaudeSet)
}

// BenchmarkMaskingDiscardGemini measures brand + antigravity without recorder noise.
func BenchmarkMaskingDiscardGemini(b *testing.B) {
	benchmarkMaskingDiscard(b, brandAntigravitySet)
}

// BenchmarkMaskingResponseWriterBrandOnly covers a request with no profile writer.
func BenchmarkMaskingResponseWriterBrandOnly(b *testing.B) {
	benchmarkMasking(b, brandOnlySet)
}

// BenchmarkMaskingResponseWriterClaude covers a claude-* request: brand + kiro.
func BenchmarkMaskingResponseWriterClaude(b *testing.B) {
	benchmarkMasking(b, brandClaudeSet)
}

// BenchmarkMaskingResponseWriterGemini covers a gemini-* request: brand + antigravity.
func BenchmarkMaskingResponseWriterGemini(b *testing.B) {
	benchmarkMasking(b, brandAntigravitySet)
}

// BenchmarkResponseWriterWrapperHeaderCapture measures the per-chunk header
// capture cost of the logging wrapper on a streaming response.
func BenchmarkResponseWriterWrapperHeaderCapture(b *testing.B) {
	c, _ := newBenchGinWriter()
	wrapper := NewResponseWriterWrapper(c.Writer, &testRequestLogger{enabled: true}, &RequestInfo{
		URL:     "/v1/chat/completions",
		Method:  http.MethodPost,
		Headers: map[string][]string{"Content-Type": {"application/json"}},
	})
	c.Writer = wrapper
	wrapper.WriteHeader(http.StatusOK)
	benchWriteChunks(b, wrapper.Write)
}
