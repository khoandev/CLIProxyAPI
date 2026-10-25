package thinking

import (
	"strings"
	"testing"
)

var benchmarkApplyThinkingSink []byte

// BenchmarkApplyThinkingOpenAI measures the thinking pipeline on a request that
// carries no reasoning_effort suffix, which is the common case: the pipeline
// still parses the payload and writes the canonical representation back.
func BenchmarkApplyThinkingOpenAI(b *testing.B) {
	message := `{"role":"user","content":"benchmark payload for the thinking pipeline"}`
	messages := strings.TrimSuffix(strings.Repeat(message+",", 1050), ",") // ~64 KiB
	body := []byte(`{"model":"gpt-5.1","messages":[` + messages + `]}`)

	out, err := ApplyThinking(body, "gpt-5.1", "openai", "openai", "")
	if err != nil {
		b.Fatalf("ApplyThinking: %v", err)
	}
	benchmarkApplyThinkingSink = out

	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for b.Loop() {
		result, errApply := ApplyThinking(body, "gpt-5.1", "openai", "openai", "")
		if errApply != nil {
			b.Fatalf("ApplyThinking: %v", errApply)
		}
		benchmarkApplyThinkingSink = result
	}
}
