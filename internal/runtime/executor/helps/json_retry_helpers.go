package helps

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// DeleteJSONField removes a top-level or nested JSON field from a payload.
func DeleteJSONField(body []byte, key string) []byte {
	if key == "" || len(body) == 0 {
		return body
	}
	updated, err := sjson.DeleteBytes(body, key)
	if err != nil {
		return body
	}
	return updated
}

// ParseRetryDelay extracts the retry delay from a Google API 429 error response.
func ParseRetryDelay(errorBody []byte) (*time.Duration, error) {
	rateLimitedUntil := regexp.MustCompile(`(?i)rate limited until\s+([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]+)?(?:Z|[+-][0-9]{2}:[0-9]{2}))`)
	if matches := rateLimitedUntil.FindSubmatch(errorBody); len(matches) > 1 {
		if until, err := time.Parse(time.RFC3339, string(matches[1])); err == nil {
			duration := time.Until(until)
			return &duration, nil
		}
	}
	details := gjson.GetBytes(errorBody, "error.details")
	if details.Exists() && details.IsArray() {
		for _, detail := range details.Array() {
			if detail.Get("@type").String() != "type.googleapis.com/google.rpc.RetryInfo" {
				continue
			}
			retryDelay := detail.Get("retryDelay").String()
			if retryDelay == "" {
				continue
			}
			duration, err := time.ParseDuration(retryDelay)
			if err != nil {
				return nil, fmt.Errorf("failed to parse duration")
			}
			return &duration, nil
		}

		for _, detail := range details.Array() {
			if detail.Get("@type").String() != "type.googleapis.com/google.rpc.ErrorInfo" {
				continue
			}
			quotaResetDelay := detail.Get("metadata.quotaResetDelay").String()
			if quotaResetDelay == "" {
				continue
			}
			duration, err := time.ParseDuration(quotaResetDelay)
			if err == nil {
				return &duration, nil
			}
		}
	}

	message := gjson.GetBytes(errorBody, "error.message").String()
	if message != "" {
		re := regexp.MustCompile(`after\s+(\d+)s\.?`)
		if matches := re.FindStringSubmatch(message); len(matches) > 1 {
			seconds, err := strconv.Atoi(matches[1])
			if err == nil {
				duration := time.Duration(seconds) * time.Second
				return &duration, nil
			}
		}
		reHuman := regexp.MustCompile(`after\s+((?:\d+h)?(?:\d+m)?(?:\d+s)?)\.?`)
		if matches := reHuman.FindStringSubmatch(strings.ToLower(message)); len(matches) > 1 {
			duration, err := time.ParseDuration(matches[1])
			if err == nil && duration > 0 {
				return &duration, nil
			}
		}
	}

	return nil, fmt.Errorf("no RetryInfo found")
}
