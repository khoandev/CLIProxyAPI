package util

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

var benchmarkSharedRequestBodySink []byte

// BenchmarkSharedRequestBodyCached measures the cached path of
// SharedRequestBody: the body is read once (during setup) and every later call
// must return the cached slice without touching the reader again, so the loop
// must report 0 B/op and 0 allocs/op.
func BenchmarkSharedRequestBodyCached(b *testing.B) {
	gin.SetMode(gin.TestMode)
	body := bytes.Repeat([]byte(`{"messages":[{"role":"user","content":"bench"}]}`), 4608) // ~256 KiB
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

	warmed, err := SharedRequestBody(c)
	if err != nil {
		b.Fatalf("warm SharedRequestBody: %v", err)
	}
	if len(warmed) != len(body) {
		b.Fatalf("body length = %d, want %d", len(warmed), len(body))
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for b.Loop() {
		first, errFirst := SharedRequestBody(c)
		if errFirst != nil {
			b.Fatalf("first SharedRequestBody: %v", errFirst)
		}
		second, errSecond := SharedRequestBody(c)
		if errSecond != nil {
			b.Fatalf("second SharedRequestBody: %v", errSecond)
		}
		if len(second) != len(first) {
			b.Fatalf("cached body length = %d, want %d", len(second), len(first))
		}
		benchmarkSharedRequestBodySink = second
	}
}
