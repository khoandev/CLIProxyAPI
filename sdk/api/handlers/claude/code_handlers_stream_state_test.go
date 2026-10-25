package claude

import (
	"testing"
)

func TestClaudeSSEStreamStateObserveChunk(t *testing.T) {
	tests := []struct {
		name               string
		chunks             []string
		wantMessageStop    bool
		wantErrorEvent     bool
		wantBareMessage    bool
		wantTrailingNewlns int
	}{
		{
			name:               "message_stop event in one chunk",
			chunks:             []string{"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
			wantMessageStop:    true,
			wantTrailingNewlns: 2,
		},
		{
			name:               "message_stop split across chunks",
			chunks:             []string{"event: message_stop\ndata: {\"type\":\"mess", "age_stop\"}\n\n"},
			wantMessageStop:    true,
			wantTrailingNewlns: 2,
		},
		{
			name:               "line split mid marker keeps the event name",
			chunks:             []string{"event: err", "or\ndata: {\"type\":\"weird\",\"error\":{\"message\":\"x\"}}\n\n"},
			wantErrorEvent:     true,
			wantTrailingNewlns: 2,
		},
		{
			name:               "bare message json with stop_reason",
			chunks:             []string{"{\"type\":\"message\",\"stop_reason\":\"end_turn\"}\n\n"},
			wantMessageStop:    true,
			wantBareMessage:    true,
			wantTrailingNewlns: 2,
		},
		{
			name:               "delta chunk changes nothing",
			chunks:             []string{"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"hi\"}}\n\n"},
			wantTrailingNewlns: 2,
		},
		{
			name:               "single trailing newline",
			chunks:             []string{"event: ping\ndata: {\"type\":\"ping\"}\n"},
			wantTrailingNewlns: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var state claudeSSEStreamState
			for _, chunk := range test.chunks {
				state.ObserveChunk([]byte(chunk))
			}
			state.FlushPendingLine()

			if state.sawMessageStop != test.wantMessageStop {
				t.Fatalf("sawMessageStop = %v, want %v", state.sawMessageStop, test.wantMessageStop)
			}
			if state.sawErrorEvent != test.wantErrorEvent {
				t.Fatalf("sawErrorEvent = %v, want %v", state.sawErrorEvent, test.wantErrorEvent)
			}
			if state.sawBareMessageJSON != test.wantBareMessage {
				t.Fatalf("sawBareMessageJSON = %v, want %v", state.sawBareMessageJSON, test.wantBareMessage)
			}
			if state.trailingNewlines != test.wantTrailingNewlns {
				t.Fatalf("trailingNewlines = %d, want %d", state.trailingNewlines, test.wantTrailingNewlns)
			}
		})
	}
}

// TestClaudeSSEStreamStateSkipsParseForDeltaChunks proves the content deltas
// that dominate a stream are inspected without copying or parsing them.
func TestClaudeSSEStreamStateSkipsParseForDeltaChunks(t *testing.T) {
	chunk := []byte(`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}` + "\n\n")
	var state claudeSSEStreamState
	state.ObserveChunk(chunk) // warm up the reused buffers

	if allocs := testing.AllocsPerRun(100, func() {
		state.ObserveChunk(chunk)
		state.sawMessageStop = false
		state.sawErrorEvent = false
	}); allocs != 0 {
		t.Fatalf("allocs/run = %v, want 0 (delta chunks must not be parsed)", allocs)
	}
	if state.sawMessageStop || state.sawErrorEvent {
		t.Fatalf("delta chunk must not change state")
	}
}

func TestTrailingClaudeSSENewlines(t *testing.T) {
	// reference reproduces the previous forward scan so the backwards scan is
	// held to identical semantics.
	reference := func(prev int, chunk []byte) int {
		count := prev
		for _, b := range chunk {
			switch b {
			case '\r':
				continue
			case '\n':
				if count < 2 {
					count++
				}
			default:
				count = 0
			}
		}
		return count
	}

	chunks := []string{"", "\n", "\n\n", "\n\n\n", "\r\n", "\r", "x", "\nx", "x\n", "a\r\n", "\r\r\n", "a\n\n\n", "\n\r", "\r\n\r\n"}
	for _, prev := range []int{0, 1, 2} {
		for _, chunk := range chunks {
			want := reference(prev, []byte(chunk))
			if got := trailingClaudeSSENewlines(prev, []byte(chunk)); got != want {
				t.Fatalf("trailingClaudeSSENewlines(%d, %q) = %d, want %d", prev, chunk, got, want)
			}
		}
	}
}
