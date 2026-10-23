package auth

import (
	"errors"
	"testing"
)

func TestContextWindowNormalized(t *testing.T) {
	const overflow = "This model's maximum context length is 272000 tokens, however your messages resulted in 272444 tokens."
	if !IsContextWindowExceeded(400, "", "", overflow, "") {
		t.Fatalf("overflow message not detected")
	}
	max, actual, ok := ParseContextLimit(overflow)
	if !ok || max != 272000 || actual != 272444 {
		t.Fatalf("ParseContextLimit = %d,%d,%v", max, actual, ok)
	}
	for _, code := range []string{"context_length_exceeded", "model_context_window_exceeded"} {
		if !IsContextWindowExceeded(400, code, "", "prompt is too long", "") {
			t.Fatalf("code %q not detected", code)
		}
	}
	for _, msg := range []string{"bad request", "rate limit exceeded, retry later"} {
		for _, status := range []int{400, 429} {
			if IsContextWindowExceeded(status, "", "", msg, "") {
				t.Fatalf("generic %d %q misdetected", status, msg)
			}
		}
	}
	wrapped := &ContextWindowExceededError{MaxContext: max, RequestTokens: actual, Provider: "codex", Raw: overflow}
	if !errors.Is(wrapped, ErrContextWindowExceeded) {
		t.Fatalf("errors.Is(wrapped) = false")
	}
	if !isContextWindowExceededError(wrapped) {
		t.Fatalf("conductor helper misses wrapped error")
	}
	if isContextWindowExceededError(errors.New("payload too large")) {
		t.Fatalf("conductor helper misdetects generic error")
	}
}
