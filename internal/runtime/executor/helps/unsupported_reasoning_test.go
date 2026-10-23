package helps

import (
	"net/http"
	"testing"
)

func TestIsUnsupportedReasoningParamError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       bool
	}{
		{"reasoning_effort", http.StatusBadRequest, `{"detail":"Unsupported parameter: reasoning_effort"}`, true},
		{"reasoning.effort", http.StatusBadRequest, `{"detail":"Unsupported parameter: reasoning.effort"}`, true},
		{"reasoning generic", http.StatusBadRequest, `{"detail":"Unsupported parameter: reasoning"}`, true},
		{"case insensitive", http.StatusBadRequest, `{"detail":"unsupported parameter: REASONING_EFFORT"}`, true},
		{"wrong status", http.StatusUnprocessableEntity, `{"detail":"Unsupported parameter: reasoning_effort"}`, false},
		{"unrelated 400", http.StatusBadRequest, `{"detail":"Unsupported parameter: temperature"}`, false},
		{"empty body", http.StatusBadRequest, ``, false},
		{"non-json", http.StatusBadRequest, `Unsupported parameter: reasoning_effort`, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsUnsupportedReasoningParamError(tc.statusCode, []byte(tc.body))
			if got != tc.want {
				t.Fatalf("IsUnsupportedReasoningParamError(%d,%q)=%v want %v", tc.statusCode, tc.body, got, tc.want)
			}
		})
	}
}

func TestStripReasoningEffortParameters(t *testing.T) {
	tests := []struct {
		name string
		body string
		// expect absence checks
		wantNoReasoningEffort bool
		wantNoReasoningDot    bool
	}{
		{"flat", `{"model":"x","reasoning_effort":"high","prompt":"hi"}`, true, false},
		{"nested", `{"model":"x","reasoning":{"effort":"high"}}`, false, true},
		{"both", `{"model":"x","reasoning_effort":"high","reasoning":{"effort":"high"}}`, true, true},
		{"nested with other fields keeps reasoning", `{"reasoning":{"effort":"high","summary":"detailed"}}`, false, true},
		{"empty input", ``, false, false},
		{"invalid json", `not json`, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := StripReasoningEffortParameters([]byte(tc.body))
			if tc.wantNoReasoningEffort && containsKey(out, "reasoning_effort") {
				t.Fatalf("expected reasoning_effort stripped, got %s", string(out))
			}
			if tc.wantNoReasoningDot && containsKey(out, "reasoning.effort") {
				t.Fatalf("expected reasoning.effort stripped, got %s", string(out))
			}
			// If reasoning became empty, it should be gone
			if tc.name == "nested" || tc.name == "both" || tc.name == "flat" {
				// For flat-only, reasoning should not exist; for nested empty, reasoning should be gone
				if tc.name == "nested" || tc.name == "both" {
					if containsKey(out, "reasoning") && tc.name != "nested with other fields keeps reasoning" {
						// Should be gone if it was only effort
						// Check raw contains "reasoning"
						// Use helper: if original had only effort, reasoning must be deleted
						if string(out) != "" && containsSubstring(string(out), `"reasoning"`) {
							t.Fatalf("expected empty reasoning object stripped, got %s", string(out))
						}
					}
				}
			}
		})
	}
}

func containsKey(body []byte, key string) bool {
	// simple check: look for gjson existence
	if len(body) == 0 {
		return false
	}
	if key == "reasoning.effort" {
		return containsSubstring(string(body), `"effort"`)
	}
	return containsSubstring(string(body), `"`+key+`"`)
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i <= len(s)-len(substr); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}
