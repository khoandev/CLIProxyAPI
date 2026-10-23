package translator

import (
	"context"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/cachecost"
	"github.com/tidwall/gjson"
)

func TestApplyCacheHitCostRatioNonStream(t *testing.T) {
	cachecost.SetRatio(0.9)
	t.Cleanup(func() { cachecost.SetRatio(1) })

	cases := []struct {
		name      string
		format    Format
		body      string
		wantCache int64
	}{
		{
			name:      "openai chat completions",
			format:    FormatOpenAI,
			body:      `{"usage":{"prompt_tokens":100,"prompt_tokens_details":{"cached_tokens":90}}}`,
			wantCache: 81,
		},
		{
			name:      "openai responses",
			format:    FormatOpenAIResponse,
			body:      `{"usage":{"input_tokens":100,"input_tokens_details":{"cached_tokens":90}}}`,
			wantCache: 81,
		},
		{
			name:      "gemini",
			format:    FormatGemini,
			body:      `{"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":90}}`,
			wantCache: 81,
		},
		{
			name:      "claude",
			format:    FormatClaude,
			body:      `{"usage":{"input_tokens":100,"cache_read_input_tokens":90,"cache_creation_input_tokens":20}}`,
			wantCache: 81,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := gjson.ParseBytes(applyCacheHitCostRatio([]byte(tc.body), tc.format))
			paths := cacheUsagePathsByFormat[tc.format]
			if cached := got.Get(paths.cachePath).Int(); cached != tc.wantCache {
				t.Fatalf("cache tokens = %d, want %d (body %s)", cached, tc.wantCache, got.Raw)
			}
			if input := got.Get(paths.inputPath).Int(); input != 100 {
				t.Fatalf("input tokens = %d, want 100 (total must not change)", input)
			}
			if tc.format == FormatClaude {
				if write := got.Get("usage.cache_creation_input_tokens").Int(); write != 20 {
					t.Fatalf("cache write tokens = %d, want 20 (must not change)", write)
				}
			}
		})
	}
}

func TestApplyCacheHitCostRatioLeavesPayloadsAlone(t *testing.T) {
	cachecost.SetRatio(0.9)
	t.Cleanup(func() { cachecost.SetRatio(1) })

	cases := []struct {
		name   string
		format Format
		body   string
	}{
		{name: "zero cached tokens", format: FormatOpenAI, body: `{"usage":{"prompt_tokens":100,"prompt_tokens_details":{"cached_tokens":0}}}`},
		{name: "missing cache field", format: FormatOpenAI, body: `{"usage":{"prompt_tokens":100}}`},
		{name: "missing input field", format: FormatOpenAI, body: `{"usage":{"prompt_tokens_details":{"cached_tokens":90}}}`},
		{name: "no usage object", format: FormatOpenAI, body: `{"id":"x"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(applyCacheHitCostRatio([]byte(tc.body), tc.format)); got != tc.body {
				t.Fatalf("payload changed: %s", got)
			}
		})
	}
}

func TestApplyCacheHitCostRatioDisabled(t *testing.T) {
	cachecost.SetRatio(1)
	body := `{"usage":{"prompt_tokens":100,"prompt_tokens_details":{"cached_tokens":90}}}`
	if got := string(applyCacheHitCostRatio([]byte(body), FormatOpenAI)); got != body {
		t.Fatalf("payload changed while disabled: %s", got)
	}
}

func TestTranslateNonStreamAppliesCacheHitCostRatio(t *testing.T) {
	cachecost.SetRatio(0.5)
	t.Cleanup(func() { cachecost.SetRatio(1) })

	registry := NewRegistry()
	registry.Register(FormatOpenAI, FormatGemini, nil, ResponseTransform{
		NonStream: func(_ context.Context, _ string, _, _, rawJSON []byte, _ *any) []byte {
			return []byte(`{"usage":{"prompt_tokens":100,"prompt_tokens_details":{"cached_tokens":90}}}`)
		},
	})

	got := gjson.ParseBytes(registry.TranslateNonStream(context.Background(), FormatGemini, FormatOpenAI, "m", nil, nil, []byte(`{}`), nil))
	if cached := got.Get("usage.prompt_tokens_details.cached_tokens").Int(); cached != 45 {
		t.Fatalf("cached tokens = %d, want 45", cached)
	}
	if input := got.Get("usage.prompt_tokens").Int(); input != 100 {
		t.Fatalf("prompt tokens = %d, want 100", input)
	}
}

func TestTranslateStreamAppliesCacheHitCostRatio(t *testing.T) {
	cachecost.SetRatio(0.5)
	t.Cleanup(func() { cachecost.SetRatio(1) })

	registry := NewRegistry()
	registry.Register(FormatOpenAI, FormatGemini, nil, ResponseTransform{
		Stream: func(_ context.Context, _ string, _, _, _ []byte, _ *any) [][]byte {
			return [][]byte{
				[]byte(`{"type":"content","text":"hi"}`),
				[]byte(`{"usage":{"prompt_tokens":100,"prompt_tokens_details":{"cached_tokens":90}}}`),
			}
		},
	})

	chunks := registry.TranslateStream(context.Background(), FormatGemini, FormatOpenAI, "m", nil, nil, []byte(`{}`), nil)
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want 2", len(chunks))
	}
	if string(chunks[0]) != `{"type":"content","text":"hi"}` {
		t.Fatalf("non-usage chunk changed: %s", chunks[0])
	}
	if cached := gjson.GetBytes(chunks[1], "usage.prompt_tokens_details.cached_tokens").Int(); cached != 45 {
		t.Fatalf("cached tokens = %d, want 45 (chunk %s)", cached, chunks[1])
	}
}
