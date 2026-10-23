package auth

import (
	"net/http"
	"strings"
)

func isModelQuotaOrCapacityMessage(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "auth in short cooldown") {
		return false
	}
	patterns := [...]string{
		"quota",
		"capacity",
		"not available for your plan",
		"model is at capacity",
	}
	for _, pattern := range patterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// ModelQuotaOrCapacityError reports upstream model/plan quota or capacity exhaustion (429/503).
// Pure auth cooldown messages are excluded.
func ModelQuotaOrCapacityError(err error) bool {
	if err == nil {
		return false
	}
	status := statusCodeFromError(err)
	if status != http.StatusTooManyRequests && status != http.StatusServiceUnavailable {
		return false
	}
	return isModelQuotaOrCapacityMessage(err.Error())
}
