package openai

import (
	"strings"
	"testing"
)

func TestShouldTreatAsResponsesFormat(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "chat completions with messages", body: `{"model":"m","messages":[{"role":"user","content":"hi"}]}`, want: false},
		{name: "responses with input", body: `{"model":"m","input":[{"role":"user","content":"hi"}]}`, want: true},
		{name: "responses with instructions", body: `{"model":"m","instructions":"be brief"}`, want: true},
		{name: "neither", body: `{"model":"m"}`, want: false},
		{name: "messages wins over input", body: `{"messages":[],"input":[]}`, want: false},
		{name: "explicit null messages counts as present", body: `{"messages":null,"instructions":"x"}`, want: false},
		{name: "empty body", body: ``, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldTreatAsResponsesFormat([]byte(test.body)); got != test.want {
				t.Fatalf("shouldTreatAsResponsesFormat(%s) = %v, want %v", test.body, got, test.want)
			}
		})
	}
}

// largeChatCompletionsBody builds a chat-completions payload whose "messages"
// array is roughly size bytes, mirroring a large multi-turn conversation.
func largeChatCompletionsBody(size int) []byte {
	var builder strings.Builder
	builder.Grow(size + 256)
	builder.WriteString(`{"model":"m","messages":[`)
	for builder.Len() < size {
		if builder.Len() > len(`{"model":"m","messages":[`) {
			builder.WriteString(",")
		}
		builder.WriteString(`{"role":"user","content":"`)
		builder.WriteString(strings.Repeat("a", 512))
		builder.WriteString(`"}`)
	}
	builder.WriteString(`],"stream":true}`)
	return []byte(builder.String())
}

// TestShouldTreatAsResponsesFormatNoCopy proves the existence probe does not
// copy the payload: gjson.GetBytes would allocate one string per probe.
func TestShouldTreatAsResponsesFormatNoCopy(t *testing.T) {
	body := largeChatCompletionsBody(256 << 10)
	if allocs := testing.AllocsPerRun(100, func() {
		if shouldTreatAsResponsesFormat(body) {
			t.Fatal("chat completions payload must not be treated as responses format")
		}
	}); allocs != 0 {
		t.Fatalf("allocs/run = %v, want 0 (payload must not be copied)", allocs)
	}
}

func BenchmarkShouldTreatAsResponsesFormat(b *testing.B) {
	body := largeChatCompletionsBody(256 << 10)
	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if shouldTreatAsResponsesFormat(body) {
			b.Fatal("chat completions payload must not be treated as responses format")
		}
	}
}
