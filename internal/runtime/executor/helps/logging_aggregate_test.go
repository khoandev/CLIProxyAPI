package helps

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func TestAggregateAPIResponseRunsOnceAndSkipsHandlerValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ginCtx, _ := gin.CreateTestContext(nil)
	ctx := context.WithValue(context.Background(), "gin", ginCtx)
	cfg := &config.Config{SDKConfig: config.SDKConfig{RequestLog: true}}

	AppendAPIResponseChunk(ctx, cfg, []byte("data: first\n\n"))

	// Chunks are not published while streaming.
	if _, exists := ginCtx.Get(apiResponseKey); exists {
		t.Fatalf("API_RESPONSE must not be published per chunk")
	}

	AggregateAPIResponse(ginCtx)
	value, exists := ginCtx.Get(apiResponseKey)
	if !exists {
		t.Fatalf("API_RESPONSE was not published at the end of the response")
	}
	body, _ := value.([]byte)
	if !bytes.Contains(body, []byte("data: first")) {
		t.Fatalf("API_RESPONSE = %q, want the captured chunk", body)
	}

	// A handler tail the aggregate does not contain is kept after it, exactly as
	// the per-chunk writes plus the handler append used to produce.
	ginCtx.Set(apiResponseKey, []byte("handler tail"))
	AppendAPIResponseChunk(ctx, cfg, []byte("data: second\n\n"))
	AggregateAPIResponse(ginCtx)
	value, _ = ginCtx.Get(apiResponseKey)
	merged, _ := value.([]byte)
	if !bytes.Contains(merged, []byte("data: first")) || !bytes.Contains(merged, []byte("data: second")) {
		t.Fatalf("API_RESPONSE = %q, want both chunks", merged)
	}
	if !bytes.HasSuffix(merged, []byte("handler tail")) {
		t.Fatalf("API_RESPONSE = %q, want the handler tail kept at the end", merged)
	}
}

// chunkBenchmarkAppend measures appending n chunks through the request-log path.
func chunkBenchmarkAppend(b *testing.B, chunks int) {
	gin.SetMode(gin.TestMode)
	ginCtx, _ := gin.CreateTestContext(nil)
	ctx := context.WithValue(context.Background(), "gin", ginCtx)
	cfg := &config.Config{SDKConfig: config.SDKConfig{RequestLog: true}}
	chunk := []byte(`data: {"choices":[{"delta":{"content":"` + string(bytes.Repeat([]byte("a"), 64)) + `"}}]}` + "\n\n")

	b.ReportAllocs()
	for b.Loop() {
		for i := 0; i < chunks; i++ {
			AppendAPIResponseChunk(ctx, cfg, chunk)
		}
	}
}

func BenchmarkAppendAPIResponseChunk500(b *testing.B) { chunkBenchmarkAppend(b, 500) }
func BenchmarkAppendAPIResponseChunk1000(b *testing.B) {
	chunkBenchmarkAppend(b, 1000)
}

// appendChunksOnce appends chunks through the request-log path once and
// returns how long the pass took. Every pass starts from a fresh gin context so
// the passes stay independent.
func appendChunksOnce(chunks int) time.Duration {
	gin.SetMode(gin.TestMode)
	ginCtx, _ := gin.CreateTestContext(nil)
	ctx := context.WithValue(context.Background(), "gin", ginCtx)
	cfg := &config.Config{SDKConfig: config.SDKConfig{RequestLog: true}}
	chunk := []byte(`data: {"choices":[{"delta":{"content":"` + string(bytes.Repeat([]byte("a"), 64)) + `"}}]}` + "\n\n")

	start := time.Now()
	for range chunks {
		AppendAPIResponseChunk(ctx, cfg, chunk)
	}
	return time.Since(start)
}

// TestAppendAPIResponseChunkScalesLinearly guards against reintroducing the
// per-chunk aggregate rebuild, which made appending O(n^2).
func TestAppendAPIResponseChunkScalesLinearly(t *testing.T) {
	if testing.Short() {
		t.Skip("timing based")
	}
	// Interleaved samples with a per-size minimum: a single benchmark run
	// reports a mean, so any load spike while the package runs in parallel
	// with the rest of the suite shows up as superlinear scaling.
	const samples = 12
	var smallBest, largeBest time.Duration
	for i := range samples {
		small := appendChunksOnce(500)
		large := appendChunksOnce(1000)
		if i == 0 || small < smallBest {
			smallBest = small
		}
		if i == 0 || large < largeBest {
			largeBest = large
		}
	}
	if smallBest <= 0 {
		t.Fatal("benchmark produced no samples")
	}
	// Linear growth is 2x for twice the chunks; the per-chunk rebuild this
	// guards against is 4x. The limit sits between them so allocation noise on
	// a loaded machine cannot flip it.
	const maxRatio = 3.0
	ratio := float64(largeBest) / float64(smallBest)
	if ratio >= maxRatio {
		t.Fatalf("1000 chunks / 500 chunks = %.2fx, want < %.1fx (quadratic behaviour is back)", ratio, maxRatio)
	}
	t.Logf("500 chunks = %s/pass, 1000 chunks = %s/pass, ratio %.2f", smallBest, largeBest, ratio)
}
