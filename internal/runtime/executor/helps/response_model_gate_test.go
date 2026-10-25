package helps

import (
	"testing"
)

func TestObserveResponseModelSkipsDeltasAfterModelSeen(t *testing.T) {
	reporter := &UsageReporter{}
	reporter.ObserveResponseModel([]byte(`{"model":"gpt-5","choices":[{"delta":{}}]}`))
	if got := reporter.ResponseModel(); got != "gpt-5" {
		t.Fatalf("ResponseModel = %q, want gpt-5", got)
	}
	if reporter.IsResponseModelFinal() {
		t.Fatalf("model must not be final after a non-terminal frame")
	}

	// A delta frame carries neither a model nor a terminal marker, so it must be
	// answered without parsing it (parsing copies the payload).
	delta := []byte(`{"choices":[{"delta":{"content":"x"}}]}`)
	if allocs := testing.AllocsPerRun(100, func() {
		reporter.ObserveResponseModel(delta)
	}); allocs != 0 {
		t.Fatalf("allocs/run = %v, want 0 (delta frames must not be parsed)", allocs)
	}
	if got := reporter.ResponseModel(); got != "gpt-5" {
		t.Fatalf("ResponseModel = %q, want gpt-5 after delta", got)
	}
	if reporter.IsResponseModelFinal() {
		t.Fatalf("delta frame must not finalize the model")
	}

	// A terminal frame still has to finalize the model.
	reporter.ObserveResponseModel([]byte(`{"type":"message_stop"}`))
	if !reporter.IsResponseModelFinal() {
		t.Fatalf("terminal frame must finalize the response model")
	}
}

// TestObserveResponseModelFinalizesOnTerminalMarkersWithoutType proves a terminal
// frame that carries no type field still finalizes the model, so the stream
// observer can stop early instead of scanning to the end of the stream.
func TestObserveResponseModelFinalizesOnTerminalMarkersWithoutType(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		payload  string
	}{
		{name: "gemini finishReason", provider: "gemini", payload: `{"candidates":[{"finishReason":"STOP"}]}`},
		{name: "interactions event_type", payload: `{"event_type":"interaction.completed"}`},
		{name: "interactions status", payload: `{"interaction":{"status":"completed"}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reporter := &UsageReporter{provider: test.provider}
			reporter.ObserveResponseModel([]byte(`{"model":"gemini-2.5-pro"}`))
			if reporter.IsResponseModelFinal() {
				t.Fatalf("model must not be final yet")
			}
			reporter.ObserveResponseModel([]byte(test.payload))
			if !reporter.IsResponseModelFinal() {
				t.Fatalf("terminal frame %s must finalize the response model", test.payload)
			}
		})
	}
}

func TestObserveResponseModelSeenFromSetResponseModel(t *testing.T) {
	reporter := &UsageReporter{}
	reporter.SetResponseModel("claude-sonnet-4-5")

	delta := []byte(`{"choices":[{"delta":{"content":"x"}}]}`)
	if allocs := testing.AllocsPerRun(100, func() {
		reporter.ObserveResponseModel(delta)
	}); allocs != 0 {
		t.Fatalf("allocs/run = %v, want 0 after SetResponseModel", allocs)
	}
	if got := reporter.ResponseModel(); got != "claude-sonnet-4-5" {
		t.Fatalf("ResponseModel = %q, want claude-sonnet-4-5", got)
	}
}

func TestExtractResponseModelEventMarkerGates(t *testing.T) {
	tests := []struct {
		name         string
		provider     string
		payload      string
		wantModel    string
		wantTerminal bool
	}{
		{
			name:     "gemini delta without markers reports nothing",
			provider: "gemini",
			payload:  `{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`,
		},
		{
			name:      "gemini modelVersion is reported",
			provider:  "gemini",
			payload:   `{"response":{"modelVersion":"gemini-2.5-pro"}}`,
			wantModel: "gemini-2.5-pro",
		},
		{
			name:         "gemini finishReason is terminal",
			provider:     "gemini",
			payload:      `{"candidates":[{"finishReason":"STOP"}]}`,
			wantTerminal: true,
		},
		{
			name:         "gemini interactions event_type is terminal",
			provider:     "gemini-interactions",
			payload:      `{"event_type":"interaction.completed"}`,
			wantTerminal: true,
		},
		{
			name:         "generic message_stop is terminal",
			provider:     "",
			payload:      `{"type":"message_stop"}`,
			wantTerminal: true,
		},
		{
			name:         "generic interaction status is terminal",
			provider:     "",
			payload:      `{"event_type":"interaction.step","interaction":{"status":"completed"}}`,
			wantTerminal: true,
		},
		{
			name:     "generic delta without markers reports nothing",
			provider: "",
			payload:  `{"choices":[{"delta":{"content":"hi"}}]}`,
		},
		{
			name:      "generic response.model is reported",
			provider:  "",
			payload:   `{"response":{"model":"gpt-5"},"type":"response.output_item.added"}`,
			wantModel: "gpt-5",
		},
		{
			name:      "claude message_start reports the model",
			provider:  "claude",
			payload:   `{"type":"message_start","message":{"model":"claude-sonnet-4-5"}}`,
			wantModel: "claude-sonnet-4-5",
		},
		{
			name:     "claude delta without model reports nothing",
			provider: "claude",
			payload:  `{"type":"content_block_delta","delta":{"text":"hi"}}`,
		},
		{
			name:      "claude default branch still reads message.model",
			provider:  "claude",
			payload:   `{"message":{"model":"claude-haiku-4-5"}}`,
			wantModel: "claude-haiku-4-5",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model, terminal := extractResponseModelEvent([]byte(test.payload), test.provider)
			if model != test.wantModel {
				t.Fatalf("model = %q, want %q", model, test.wantModel)
			}
			if terminal != test.wantTerminal {
				t.Fatalf("terminal = %v, want %v", terminal, test.wantTerminal)
			}
		})
	}
}
