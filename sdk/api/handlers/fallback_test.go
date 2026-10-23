package handlers

import (
	"testing"
)

func TestWithFallbackModelInPayload(t *testing.T) {
	tests := []struct {
		name          string
		rawJSON       []byte
		fallbackModel string
		want          string
	}{
		{
			name:          "updates model field",
			rawJSON:       []byte(`{"model":"claude-opus-4-8","messages":[]}`),
			fallbackModel: "claude-opus-4-7",
			want:          `{"model":"claude-opus-4-7","messages":[]}`,
		},
		{
			name:          "empty payload unchanged",
			rawJSON:       nil,
			fallbackModel: "claude-opus-4-7",
			want:          "",
		},
		{
			name:          "no model field unchanged",
			rawJSON:       []byte(`{"messages":[]}`),
			fallbackModel: "claude-opus-4-7",
			want:          `{"messages":[]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(withFallbackModelInPayload(tt.rawJSON, tt.fallbackModel))
			if got != tt.want {
				t.Fatalf("withFallbackModelInPayload() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRestoreOriginalModelInBody(t *testing.T) {
	tests := []struct {
		name          string
		body          []byte
		originalModel string
		want          string
	}{
		{
			name:          "overwrites model",
			body:          []byte(`{"model":"claude-opus-4-7","id":"chatcmpl-1"}`),
			originalModel: "claude-opus-4-8",
			want:          `{"model":"claude-opus-4-8","id":"chatcmpl-1"}`,
		},
		{
			name:          "empty body unchanged",
			body:          nil,
			originalModel: "claude-opus-4-8",
			want:          "",
		},
		{
			name:          "no model field unchanged",
			body:          []byte(`{"id":"chatcmpl-1"}`),
			originalModel: "claude-opus-4-8",
			want:          `{"id":"chatcmpl-1"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(restoreOriginalModelInBody(tt.body, tt.originalModel))
			if got != tt.want {
				t.Fatalf("restoreOriginalModelInBody() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRestoreOriginalModelInChunk(t *testing.T) {
	tests := []struct {
		name          string
		chunk         []byte
		originalModel string
		want          string
	}{
		{
			name:          "sse data json",
			chunk:         []byte("data: {\"model\":\"claude-opus-4-7\",\"choices\":[]}\n\n"),
			originalModel: "claude-opus-4-8",
			want:          "data: {\"model\":\"claude-opus-4-8\",\"choices\":[]}\n\n",
		},
		{
			name:          "raw json chunk",
			chunk:         []byte(`{"model":"kimi-k2.5"}`),
			originalModel: "kimi-k2.6",
			want:          `{"model":"kimi-k2.6"}`,
		},
		{
			name:          "empty chunk unchanged",
			chunk:         nil,
			originalModel: "claude-opus-4-8",
			want:          "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(restoreOriginalModelInChunk(tt.chunk, tt.originalModel))
			if got != tt.want {
				t.Fatalf("restoreOriginalModelInChunk() = %q, want %q", got, tt.want)
			}
		})
	}
}
