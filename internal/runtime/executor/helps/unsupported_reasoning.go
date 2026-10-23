package helps

import (
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// IsUnsupportedReasoningParamError reports whether the upstream response
// indicates an unsupported reasoning parameter. It checks for HTTP 400 and
// a body containing "Unsupported parameter: reasoning_effort" or
// "Unsupported parameter: reasoning.effort" or "Unsupported parameter: reasoning"
// case-insensitively.
func IsUnsupportedReasoningParamError(statusCode int, body []byte) bool {
	if statusCode != http.StatusBadRequest {
		return false
	}
	if len(body) == 0 {
		return false
	}
	lower := strings.ToLower(string(body))
	if strings.Contains(lower, "unsupported parameter: reasoning_effort") {
		return true
	}
	if strings.Contains(lower, "unsupported parameter: reasoning.effort") {
		return true
	}
	if strings.Contains(lower, "unsupported parameter: reasoning") {
		return true
	}
	return false
}

// StripReasoningEffortParameters removes reasoning effort fields from a JSON
// payload. It deletes "reasoning_effort", "reasoning.effort", and if the
// "reasoning" object becomes empty after removing the nested effort, it also
// deletes the "reasoning" object itself.
func StripReasoningEffortParameters(body []byte) []byte {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return body
	}
	result := body
	// Strip flat field.
	if gjson.GetBytes(result, "reasoning_effort").Exists() {
		if updated, err := sjson.DeleteBytes(result, "reasoning_effort"); err == nil {
			result = updated
		}
	}
	// Strip nested field.
	if gjson.GetBytes(result, "reasoning.effort").Exists() {
		if updated, err := sjson.DeleteBytes(result, "reasoning.effort"); err == nil {
			result = updated
		}
	}
	// If reasoning object is now empty, remove it.
	if r := gjson.GetBytes(result, "reasoning"); r.Exists() && r.IsObject() && len(r.Map()) == 0 {
		if updated, err := sjson.DeleteBytes(result, "reasoning"); err == nil {
			result = updated
		}
	}
	return result
}
