package helps

import (
	"bytes"
	"testing"
)

// TestFilterSSEUsageMetadataReturnsInputWithoutMarkers proves the cheap gate
// returns the very same slice, so a plain delta frame is never scanned or copied.
func TestFilterSSEUsageMetadataReturnsInputWithoutMarkers(t *testing.T) {
	in := []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"hi\"}]}}]}\n\n")
	out := FilterSSEUsageMetadata(in)
	if len(out) != len(in) || &out[0] != &in[0] {
		t.Fatalf("output = %q, want the input slice unchanged", out)
	}
}

// TestFilterSSEUsageMetadataProcessesRawJSONWithoutDataPrefix covers the branch
// that runs after the loop when the payload is bare JSON instead of SSE frames.
func TestFilterSSEUsageMetadataProcessesRawJSONWithoutDataPrefix(t *testing.T) {
	in := []byte(`{"candidates":[{"content":{"parts":[]}}],"usageMetadata":{"promptTokenCount":3}}`)
	out := FilterSSEUsageMetadata(in)
	if bytes.Contains(out, []byte("usageMetadata")) {
		t.Fatalf("output = %q, want usageMetadata stripped from bare JSON", out)
	}
}

// TestFilterSSEUsageMetadataKeepsBareJSONWithoutMarkers proves the gate returns a
// bare JSON payload that carries no usage or finishReason untouched.
func TestFilterSSEUsageMetadataKeepsBareJSONWithoutMarkers(t *testing.T) {
	in := []byte(`{"candidates":[{"content":{"parts":[{"text":"hi"}]}}]}`)
	out := FilterSSEUsageMetadata(in)
	if len(out) != len(in) || &out[0] != &in[0] {
		t.Fatalf("output = %q, want the input slice unchanged", out)
	}
}

func TestFilterSSEUsageMetadataStillProcessesUsageFrames(t *testing.T) {
	in := []byte("data: {\"candidates\":[{\"content\":{\"parts\":[]}}],\"usageMetadata\":{\"promptTokenCount\":3}}\n\n")
	out := FilterSSEUsageMetadata(in)
	if bytes.Contains(out, []byte("usageMetadata")) {
		t.Fatalf("output = %q, want usageMetadata stripped from a non-stop frame", out)
	}
}

// TestEnsureResponsesUsageDetailsReturnsInputWithoutUsage proves a frame without
// the usage key is returned untouched.
func TestEnsureResponsesUsageDetailsReturnsInputWithoutUsage(t *testing.T) {
	in := []byte(`{"type":"response.output_item.added","item":{"type":"message"}}`)
	out := EnsureResponsesUsageDetails(in)
	if len(out) != len(in) || &out[0] != &in[0] {
		t.Fatalf("output = %q, want the input slice unchanged", out)
	}
}

func TestEnsureResponsesUsageDetailsStillFillsUsage(t *testing.T) {
	in := []byte(`{"type":"response.completed","response":{"usage":{"input_tokens":5,"output_tokens":2}}}`)
	out := EnsureResponsesUsageDetails(in)
	if !bytes.Contains(out, []byte(`"output_tokens_details"`)) {
		t.Fatalf("output = %q, want output_tokens_details added", out)
	}
	if !bytes.Contains(out, []byte(`"input_tokens_details"`)) {
		t.Fatalf("output = %q, want input_tokens_details added", out)
	}
}
