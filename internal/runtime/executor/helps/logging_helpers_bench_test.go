package helps

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

var benchmarkDeferAPIRequestSink []byte

// BenchmarkDeferAPIRequestLargeBody measures the default (request-log: false)
// path of RecordAPIRequest with a multi-megabyte outbound body. The deferred
// path must not copy the payload.
func BenchmarkDeferAPIRequestLargeBody(b *testing.B) {
	gin.SetMode(gin.TestMode)
	body := bytes.Repeat([]byte(`{"model":"gpt-5.1","messages":[{"role":"user","content":"bench"}]}`), 14564) // ~1 MiB
	info := UpstreamRequestLog{
		URL:      "https://upstream.example/v1/responses",
		Method:   http.MethodPost,
		Headers:  http.Header{"Content-Type": {"application/json"}},
		Body:     body,
		Provider: "openai",
	}
	cfg := &config.Config{}
	cfg.RequestLog = false
	benchmarkDeferAPIRequestSink = body

	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for b.Loop() {
		ginCtx, _ := gin.CreateTestContext(nil)
		ctx := context.WithValue(context.Background(), "gin", ginCtx)
		RecordAPIRequest(ctx, cfg, info)
	}
}
