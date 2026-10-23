package helps

import (
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// UpstreamBodyError is a StatusError surfaced when an upstream provider returns
// an HTTP 2xx response whose JSON body nevertheless carries an error object
// (e.g. {"error":{"message":"Insufficient quota.","type":"insufficient_quota"}}).
// Some OpenAI-compatible gateways report quota/billing failures this way instead
// of using a proper non-2xx status code, which would otherwise cause the proxy
// to forward the error payload to the client as a successful response and skip
// any retry across alternative credentials.
type UpstreamBodyError struct {
	Code       int
	Message    string
	RetryAfter *time.Duration
}

func (e UpstreamBodyError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return http.StatusText(e.Code)
}

// StatusCode implements cliproxyexecutor.StatusError so the auth conductor can
// classify the failure and trigger credential rotation / cooldown.
func (e UpstreamBodyError) StatusCode() int { return e.Code }

// RetryAfterDuration exposes an optional cooldown hint, mirroring the statusErr
// contract used by provider executors.
func (e UpstreamBodyError) RetryAfterDuration() *time.Duration { return e.RetryAfter }

// DetectUpstreamErrorBody inspects an upstream JSON response body that was
// received with an HTTP 2xx status and returns an UpstreamBodyError when the
// payload carries a top-level "error" object or is an HTML document. The HTTP
// status passed in is the one actually received from upstream (typically 200);
// the returned error's StatusCode is derived from the error type/code so the
// retry classifier can treat quota/billing failures as retryable across
// credentials.
//
// Returns nil when the body is not valid JSON or does not contain an error
// object, in which case the caller should treat the response as successful.
func DetectUpstreamErrorBody(httpStatus int, body []byte) *UpstreamBodyError {
	if len(body) == 0 {
		return nil
	}
	var firstByte byte
	for _, b := range body {
		if b != ' ' && b != '\t' && b != '\n' && b != '\r' {
			firstByte = b
			break
		}
	}
	// HTML response on a 2xx status means the upstream returned a web page
	// (e.g. a login wall, gateway error page, or CDN block page) instead of
	// a valid API response. Surface this as a 502 Bad Gateway so the auth
	// conductor can rotate credentials and retry rather than forwarding the
	// HTML to the client as a successful API response.
	if firstByte == '<' {
		lower := strings.ToLower(strings.TrimSpace(string(body[:min(len(body), 512)])))
		if strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html") {
			if httpStatus >= 200 && httpStatus < 300 {
				msg := "[upstream returned HTML page instead of API response]"
				if title := extractHTMLTitle(body); title != "" {
					msg = "[upstream returned HTML page: " + title + "]"
				}
				return &UpstreamBodyError{Code: http.StatusBadGateway, Message: msg}
			}
		}
	}
	if firstByte != '{' && firstByte != '[' {
		// Non-JSON body on 401/403 — treat as auth failure so credential
		// rotation kicks in (e.g. upstream returns a plain-text "Unauthorized").
		if httpStatus == http.StatusUnauthorized {
			return &UpstreamBodyError{Code: http.StatusUnauthorized, Message: string(body)}
		}
		if httpStatus == http.StatusForbidden {
			return &UpstreamBodyError{Code: http.StatusForbidden, Message: string(body)}
		}
		// Plain-text body on 2xx containing key-abuse / ToS-violation phrases
		// — upstream confirmed the key is dead even though HTTP status is 200.
		if httpStatus >= 200 && httpStatus < 300 {
			if code := detectPlainTextKeyAbuse(body); code != 0 {
				return &UpstreamBodyError{Code: code, Message: string(body)}
			}
		}
		return nil
	}
	errField := gjson.GetBytes(body, "error")
	if !errField.Exists() {
		return nil
	}
	if errField.Type == gjson.String {
		msg := strings.TrimSpace(errField.String())
		if msg == "" {
			return nil
		}
		return &UpstreamBodyError{
			Code:    inferUpstreamErrorStatus(httpStatus, "", msg),
			Message: string(body),
		}
	}
	if errField.Type != gjson.JSON {
		return nil
	}

	errType := strings.ToLower(strings.TrimSpace(errField.Get("type").String()))
	errCode := strings.ToLower(strings.TrimSpace(errField.Get("code").String()))
	message := strings.TrimSpace(errField.Get("message").String())
	if errType == "" && errCode == "" && message == "" {
		return nil
	}

	status := inferUpstreamErrorStatus(httpStatus, errType+" "+errCode, message)
	return &UpstreamBodyError{
		Code:    status,
		Message: string(body),
	}
}

// inferUpstreamErrorStatus maps known upstream error type/code strings to an
// HTTP-like status code that the auth conductor understands for cooldown and
// retry decisions. Unknown error shapes fall back to the received HTTP status,
// or 502 Bad Gateway when the upstream masked an error behind HTTP 200.
func inferUpstreamErrorStatus(httpStatus int, typeOrCode string, message string) int {
	lower := strings.ToLower(typeOrCode + " " + message)
	switch {
	case strings.Contains(lower, "insufficient_quota") ||
		strings.Contains(lower, "quota") ||
		strings.Contains(lower, "billing") ||
		strings.Contains(lower, "payment") ||
		strings.Contains(lower, "credit") ||
		strings.Contains(lower, "insufficient"):
		return http.StatusPaymentRequired
	case strings.Contains(lower, "rate_limit") ||
		strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "too many requests") ||
		strings.Contains(lower, "usage_limit") ||
		strings.Contains(lower, "capacity"):
		return http.StatusTooManyRequests
	case strings.Contains(lower, "api key has been disabled") ||
		strings.Contains(lower, "key has been disabled") ||
		strings.Contains(lower, "unauthorized resale") ||
		strings.Contains(lower, "violating the terms of service") ||
		strings.Contains(lower, "violates our terms") ||
		strings.Contains(lower, "terms of service violation") ||
		strings.Contains(lower, "account has been suspended") ||
		strings.Contains(lower, "account suspended") ||
		strings.Contains(lower, "key is disabled") ||
		strings.Contains(lower, "key disabled"):
		return http.StatusForbidden
	case strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "invalid_api_key") ||
		strings.Contains(lower, "authentication"):
		return http.StatusUnauthorized
	case strings.Contains(lower, "forbidden") ||
		strings.Contains(lower, "permission"):
		return http.StatusForbidden
	case strings.Contains(lower, "not_found") ||
		strings.Contains(lower, "model_not_found"):
		return http.StatusNotFound
	}
	if httpStatus >= 200 && httpStatus < 300 {
		return http.StatusBadGateway
	}
	return httpStatus
}

// detectPlainTextKeyAbuse scans a non-JSON response body for well-known
// phrases that upstream providers embed in plain-text or minimal-HTML
// error responses to indicate that an API key has been revoked, disabled,
// or is being used in violation of their terms of service. Returns the
// appropriate HTTP status code (401 or 403), or 0 if no phrase matched.
func detectPlainTextKeyAbuse(body []byte) int {
	lower := strings.ToLower(string(body[:min(len(body), 1024)]))
	switch {
	case strings.Contains(lower, "api key has been disabled") ||
		strings.Contains(lower, "key has been disabled") ||
		strings.Contains(lower, "key is disabled") ||
		strings.Contains(lower, "key disabled") ||
		strings.Contains(lower, "unauthorized resale") ||
		strings.Contains(lower, "violating the terms of service") ||
		strings.Contains(lower, "violates our terms") ||
		strings.Contains(lower, "terms of service violation") ||
		strings.Contains(lower, "account has been suspended") ||
		strings.Contains(lower, "account suspended"):
		return http.StatusForbidden
	case strings.Contains(lower, "invalid api key") ||
		strings.Contains(lower, "invalid_api_key") ||
		strings.Contains(lower, "api key is invalid") ||
		strings.Contains(lower, "incorrect api key"):
		return http.StatusForbidden
	}
	return 0
}
