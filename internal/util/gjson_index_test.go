package util

import (
	"testing"

	"github.com/tidwall/gjson"
)

// TestJSONIndexMatchesGJSON proves the index resolves the same values as a
// direct gjson lookup for every path shape the callers use.
func TestJSONIndexMatchesGJSON(t *testing.T) {
	bodies := []string{
		`{}`,
		`{"model":"gpt-5"}`,
		`{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`,
		`{"reasoning":{"effort":"high","summary":"auto"}}`,
		`{"reasoning":{"effort":"high"},"include_reasoning":true,"reasoning_effort":"low"}`,
		`{"thinking":{"type":"adaptive","budget_tokens":2048,"display":"omitted"}}`,
		`{"generationConfig":{"thinkingConfig":{"includeThoughts":true}}}`,
		`{"request":{"generationConfig":{"thinkingConfig":{"include_thoughts":false}}}}`,
		`{"extra_body":{"google":{"thinking_config":{"include_thoughts":true}}}}`,
		`{"generation_config":{"thinking_summaries":"auto","thinking_config":{"include_thoughts":true}}}`,
		`{"a":{"b":1},"a":{"c":2}}`,
		`{"messages":[{"role":"assistant","content":[{"type":"text","text":"x"}]}],"model":"m"}`,
	}
	paths := []string{
		"",
		"model",
		"messages",
		"messages.0.role",
		"messages.0.content.0.text",
		"reasoning.effort",
		"reasoning.summary",
		"reasoning.exclude",
		"include_reasoning",
		"reasoning_effort",
		"thinking.type",
		"thinking.budget_tokens",
		"thinking.display",
		"output_config.effort",
		"generationConfig.thinkingConfig.includeThoughts",
		"generationConfig.thinkingConfig.include_thoughts",
		"request.generationConfig.thinkingConfig.include_thoughts",
		"generation_config.thinking_summaries",
		"generation_config.thinking_config.include_thoughts",
		"extra_body.google.thinking_config.include_thoughts",
		"extra_body.extra_body.google.thinking_config.include_thoughts",
		"google.thinking_config.include_thoughts",
		"missing",
		"missing.nested",
		"a.b",
		"a.c",
		"reasoning",
		"thinking",
		"input",
	}

	for _, body := range bodies {
		index := NewJSONIndex([]byte(body))
		for _, path := range paths {
			want := gjson.GetBytes([]byte(body), path)
			got := index.Get(path)
			if got.Type != want.Type {
				t.Fatalf("body=%s path=%q type = %v, want %v", body, path, got.Type, want.Type)
			}
			if got.Raw != want.Raw {
				t.Fatalf("body=%s path=%q raw = %q, want %q", body, path, got.Raw, want.Raw)
			}
			if got.Str != want.Str {
				t.Fatalf("body=%s path=%q str = %q, want %q", body, path, got.Str, want.Str)
			}
			if got.Exists() != want.Exists() {
				t.Fatalf("body=%s path=%q exists = %v, want %v", body, path, got.Exists(), want.Exists())
			}
			if got.Index != want.Index {
				t.Fatalf("body=%s path=%q index = %d, want %d", body, path, got.Index, want.Index)
			}
		}
	}
}

// TestJSONIndexHandlesNonObject guards the fallback for bodies that are not
// JSON objects.
func TestJSONIndexHandlesNonObject(t *testing.T) {
	for _, body := range []string{``, `[]`, `[1,2,3]`, `"text"`, `123`, `null`} {
		index := NewJSONIndex([]byte(body))
		if got := index.Get("model"); got.Exists() {
			t.Fatalf("body=%q model = %q, want missing", body, got.Raw)
		}
	}
}

// TestJSONIndexResolvesLateMember proves the index answers a lookup for a
// member that sits behind a large sibling. A plain gjson lookup of an absent
// path scans the whole document, so this is the case the index exists for.
func TestJSONIndexResolvesLateMember(t *testing.T) {
	body := []byte(`{"messages":["` + string(make([]byte, 1<<16)) + `"],"model":"m"}`)
	index := NewJSONIndex(body)
	if got := index.Get("model"); got.String() != "m" {
		t.Fatalf("model = %q, want m", got.String())
	}
	if got := index.Get("generationConfig.thinkingConfig.includeThoughts"); got.Exists() {
		t.Fatalf("unexpected match: %q", got.Raw)
	}
}
