package openai

import (
	"strings"
	"testing"
)

func TestInspectChatStreamChunk(t *testing.T) {
	tests := []struct {
		name         string
		chunk        string
		wantErr      string
		wantFinished bool
	}{
		{
			name:  "plain delta carries neither marker",
			chunk: `{"choices":[{"delta":{"content":"x"}}]}`,
		},
		{
			name:         "finish_reason closes the response",
			chunk:        `{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
			wantFinished: true,
		},
		{
			name:         "error object is terminal",
			chunk:        `{"error":{"message":"boom"}}`,
			wantErr:      "boom",
			wantFinished: true,
		},
		{
			name:  "null error is not terminal",
			chunk: `{"choices":[{"delta":{"content":"x"}}],"error":null}`,
		},
		{
			name:         "data prefix and trailing newlines are stripped",
			chunk:        "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\n",
			wantFinished: true,
		},
		{
			name:         "error with data prefix is terminal",
			chunk:        "data: {\"error\":{\"message\":\"bad key\"}}\n\n",
			wantErr:      "bad key",
			wantFinished: true,
		},
		{
			name:         "error without message falls back to the raw chunk",
			chunk:        `{"error":{"type":"x"}}`,
			wantErr:      `{"error":{"type":"x"}}`,
			wantFinished: true,
		},
		{
			name:  "empty finish_reason is not terminal",
			chunk: `{"choices":[{"delta":{},"finish_reason":""}]}`,
		},
		{
			name:  "null finish_reason is not terminal",
			chunk: `{"choices":[{"delta":{},"finish_reason":null}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			termErr, finished := inspectChatStreamChunk([]byte(test.chunk))
			if test.wantErr == "" {
				if termErr != nil {
					t.Fatalf("termErr = %v, want nil", termErr.Error)
				}
			} else {
				if termErr == nil {
					t.Fatalf("termErr = nil, want %q", test.wantErr)
				}
				if termErr.Error.Error() != test.wantErr {
					t.Fatalf("termErr = %q, want %q", termErr.Error.Error(), test.wantErr)
				}
			}
			if finished != test.wantFinished {
				t.Fatalf("finished = %v, want %v", finished, test.wantFinished)
			}
		})
	}
}

// TestInspectChatStreamChunkSkipsParseForDeltaChunks proves a chunk without the
// error or finish_reason markers is answered without copying or parsing it.
func TestInspectChatStreamChunkSkipsParseForDeltaChunks(t *testing.T) {
	chunk := []byte(`{"id":"chatcmpl-1","choices":[{"index":0,"delta":{"content":"` + strings.Repeat("a", 32<<10) + `"}}]}`)
	if allocs := testing.AllocsPerRun(100, func() {
		if termErr, finished := inspectChatStreamChunk(chunk); termErr != nil || finished {
			t.Fatalf("inspectChatStreamChunk = (%v, %v), want (nil, false)", termErr, finished)
		}
	}); allocs != 0 {
		t.Fatalf("allocs/run = %v, want 0 (delta chunks must not be parsed)", allocs)
	}
}
