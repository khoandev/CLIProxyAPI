package chat_completions

import (
	"context"
	"strings"
	"testing"
)

var benchmarkClaudeOpenAIStreamSink [][]byte

// benchmarkClaudeOpenAIStream drives a content_block_delta stream through
// ConvertClaudeResponseToOpenAI with a fixed original request size.
func benchmarkClaudeOpenAIStream(b *testing.B, chunks int) {
	b.Helper()
	message := `{"role":"user","content":"benchmark history turn for the stream translator"}`
	history := strings.TrimSuffix(strings.Repeat(message+",", 2000), ",") // ~200 KiB
	originalRequest := []byte(`{"model":"claude-sonnet-4-5-20250929","messages":[` + history + `]}`)

	start := []byte(`data: {"type":"message_start","message":{"id":"msg_bench","model":"claude-sonnet-4-5-20250929","usage":{"input_tokens":1024,"output_tokens":0}}}`)
	delta := []byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"streamed text chunk"}}`)
	stop := []byte(`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2048}}`)

	var param any
	if got := ConvertClaudeResponseToOpenAI(context.Background(), "claude-sonnet-4-5-20250929", originalRequest, originalRequest, start, &param); len(got) == 0 {
		b.Fatal("message_start produced no chunk")
	}

	b.ReportAllocs()
	b.SetBytes(int64(len(delta)))
	b.ResetTimer()
	for b.Loop() {
		for range chunks {
			benchmarkClaudeOpenAIStreamSink = ConvertClaudeResponseToOpenAI(context.Background(), "claude-sonnet-4-5-20250929", originalRequest, originalRequest, delta, &param)
		}
		benchmarkClaudeOpenAIStreamSink = ConvertClaudeResponseToOpenAI(context.Background(), "claude-sonnet-4-5-20250929", originalRequest, originalRequest, stop, &param)
	}
}

// BenchmarkClaudeOpenAIStreamChunk measures the per-chunk cost of the Claude to
// OpenAI streaming translator over a 256-chunk stream.
func BenchmarkClaudeOpenAIStreamChunk(b *testing.B) {
	benchmarkClaudeOpenAIStream(b, 256)
}

// TestClaudeOpenAIStreamChunkPerChunkAllocsStable proves the per-chunk work does
// not grow with the stream length: a translator that keeps re-rendering the
// whole envelope or accumulating per-chunk state shows up as a rising cost per
// chunk.
func TestClaudeOpenAIStreamChunkPerChunkAllocsStable(t *testing.T) {
	bytesPerChunk := func(chunks int) float64 {
		result := testing.Benchmark(func(b *testing.B) {
			benchmarkClaudeOpenAIStream(b, chunks)
		})
		return float64(result.AllocedBytesPerOp()) / float64(chunks)
	}
	short := bytesPerChunk(64)
	long := bytesPerChunk(256)
	if long > short*12/10 {
		t.Fatalf("per-chunk allocations grew with stream length: %.0f B/chunk at 64 chunks, %.0f B/chunk at 256 chunks", short, long)
	}
}
