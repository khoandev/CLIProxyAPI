package helps

import (
	"net/http"
	"testing"
)

func TestDetectUpstreamErrorBody_InsufficientQuota(t *testing.T) {
	body := []byte(`{"error":{"message":"Insufficient quota.","type":"insufficient_quota"}}`)
	err := DetectUpstreamErrorBody(http.StatusOK, body)
	if err == nil {
		t.Fatal("expected error for insufficient_quota body on HTTP 200")
	}
	if err.Code != http.StatusPaymentRequired {
		t.Fatalf("expected status 402, got %d", err.Code)
	}
	if err.StatusCode() != http.StatusPaymentRequired {
		t.Fatalf("expected StatusCode()=402, got %d", err.StatusCode())
	}
	if err.Error() == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestDetectUpstreamErrorBody_RateLimit(t *testing.T) {
	body := []byte(`{"error":{"message":"Too many requests.","type":"rate_limit_exceeded"}}`)
	err := DetectUpstreamErrorBody(http.StatusOK, body)
	if err == nil {
		t.Fatal("expected error for rate_limit body")
	}
	if err.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %d", err.Code)
	}
}

func TestDetectUpstreamErrorBody_StringError(t *testing.T) {
	body := []byte(`{"error":"Insufficient quota."}`)
	err := DetectUpstreamErrorBody(http.StatusOK, body)
	if err == nil {
		t.Fatal("expected error for string error body")
	}
	if err.Code != http.StatusPaymentRequired {
		t.Fatalf("expected status 402, got %d", err.Code)
	}
}

func TestDetectUpstreamErrorBody_NoError(t *testing.T) {
	body := []byte(`{"id":"resp_1","choices":[{"message":{"content":"ok"}}]}`)
	err := DetectUpstreamErrorBody(http.StatusOK, body)
	if err != nil {
		t.Fatalf("expected nil error for success body, got %v", err)
	}
}

func TestDetectUpstreamErrorBody_NonJSON(t *testing.T) {
	body := []byte("data: {\"type\":\"response.completed\"}\n\n")
	err := DetectUpstreamErrorBody(http.StatusOK, body)
	if err != nil {
		t.Fatalf("expected nil error for SSE body, got %v", err)
	}
}

func TestDetectUpstreamErrorBody_EmptyBody(t *testing.T) {
	if err := DetectUpstreamErrorBody(http.StatusOK, nil); err != nil {
		t.Fatalf("expected nil for empty body, got %v", err)
	}
}

func TestDetectUpstreamErrorBody_EmptyErrorObject(t *testing.T) {
	body := []byte(`{"error":{}}`)
	if err := DetectUpstreamErrorBody(http.StatusOK, body); err != nil {
		t.Fatalf("expected nil for empty error object, got %v", err)
	}
}

func TestDetectUpstreamErrorBody_UnknownFallsBackToBadGateway(t *testing.T) {
	body := []byte(`{"error":{"message":"something broke","type":"weird_error"}}`)
	err := DetectUpstreamErrorBody(http.StatusOK, body)
	if err == nil {
		t.Fatal("expected error for unknown error type on HTTP 200")
	}
	if err.Code != http.StatusBadGateway {
		t.Fatalf("expected fallback status 502, got %d", err.Code)
	}
}

func TestDetectUpstreamErrorBody_PreservesNon2xxStatus(t *testing.T) {
	body := []byte(`{"error":{"message":"something broke","type":"weird_error"}}`)
	err := DetectUpstreamErrorBody(http.StatusInternalServerError, body)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Code != http.StatusInternalServerError {
		t.Fatalf("expected preserved status 500, got %d", err.Code)
	}
}

func TestDetectUpstreamErrorBody_HTMLDoctype(t *testing.T) {
	body := []byte("<!doctype html><html lang=\"zh-CN\"><head><title>Mze - API - AI API Gateway</title></head></html>")
	err := DetectUpstreamErrorBody(http.StatusOK, body)
	if err == nil {
		t.Fatal("expected error for HTML body on HTTP 200")
	}
	if err.Code != http.StatusBadGateway {
		t.Fatalf("expected status 502, got %d", err.Code)
	}
	if err.Message == "" {
		t.Fatal("expected non-empty error message")
	}
}

func TestDetectUpstreamErrorBody_HTMLDoctypeUpperCase(t *testing.T) {
	body := []byte("<!DOCTYPE HTML><html><head><title>Gateway Error</title></head><body></body></html>")
	err := DetectUpstreamErrorBody(http.StatusOK, body)
	if err == nil {
		t.Fatal("expected error for uppercase DOCTYPE HTML body")
	}
	if err.Code != http.StatusBadGateway {
		t.Fatalf("expected status 502, got %d", err.Code)
	}
}

func TestDetectUpstreamErrorBody_HTMLTagOnly(t *testing.T) {
	body := []byte("<html lang=\"en\"><head><title>Login</title></head></html>")
	err := DetectUpstreamErrorBody(http.StatusOK, body)
	if err == nil {
		t.Fatal("expected error for <html> body")
	}
	if err.Code != http.StatusBadGateway {
		t.Fatalf("expected status 502, got %d", err.Code)
	}
}

func TestDetectUpstreamErrorBody_HTMLWithTitle(t *testing.T) {
	body := []byte("<!doctype html><html><head><title>Mze - API - AI API Gateway</title></head></html>")
	err := DetectUpstreamErrorBody(http.StatusOK, body)
	if err == nil {
		t.Fatal("expected error for HTML body with title")
	}
	if err.Message != "[upstream returned HTML page: Mze - API - AI API Gateway]" {
		t.Fatalf("unexpected message: %s", err.Message)
	}
}

func TestDetectUpstreamErrorBody_HTMLNoTitle(t *testing.T) {
	body := []byte("<!doctype html><html><head></head><body><p>Forbidden</p></body></html>")
	err := DetectUpstreamErrorBody(http.StatusOK, body)
	if err == nil {
		t.Fatal("expected error for HTML body without title")
	}
	if err.Message != "[upstream returned HTML page instead of API response]" {
		t.Fatalf("unexpected message: %s", err.Message)
	}
}

func TestDetectUpstreamErrorBody_HTMLNon2xx(t *testing.T) {
	// Non-2xx HTML should not be caught here (already handled by status check in executor)
	body := []byte("<!doctype html><html><head><title>Not Found</title></head></html>")
	err := DetectUpstreamErrorBody(http.StatusNotFound, body)
	if err != nil {
		t.Fatalf("expected nil for non-2xx HTML body, got %v", err)
	}
}

func TestDetectUpstreamErrorBody_HTMLWhitespacePrefix(t *testing.T) {
	body := []byte("\n\n  <!doctype html><html><head><title>Page</title></head></html>")
	err := DetectUpstreamErrorBody(http.StatusOK, body)
	if err == nil {
		t.Fatal("expected error for HTML body with leading whitespace")
	}
	if err.Code != http.StatusBadGateway {
		t.Fatalf("expected status 502, got %d", err.Code)
	}
}

func TestDetectUpstreamErrorBody_401NonJSON(t *testing.T) {
	body := []byte("Unauthorized")
	err := DetectUpstreamErrorBody(http.StatusUnauthorized, body)
	if err == nil {
		t.Fatal("expected error for non-JSON 401 body")
	}
	if err.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", err.Code)
	}
}

func TestDetectUpstreamErrorBody_403NonJSON(t *testing.T) {
	body := []byte("Forbidden")
	err := DetectUpstreamErrorBody(http.StatusForbidden, body)
	if err == nil {
		t.Fatal("expected error for non-JSON 403 body")
	}
	if err.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", err.Code)
	}
}

func TestDetectUpstreamErrorBody_200KeyDisabledPlaintext(t *testing.T) {
	body := []byte("Error: API key has been disabled")
	err := DetectUpstreamErrorBody(http.StatusOK, body)
	if err == nil {
		t.Fatal("expected error for 200 body with key-disabled phrase")
	}
	if err.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", err.Code)
	}
}

func TestDetectUpstreamErrorBody_200UnauthorizedResalePlaintext(t *testing.T) {
	body := []byte("This key has been suspended due to unauthorized resale.")
	err := DetectUpstreamErrorBody(http.StatusOK, body)
	if err == nil {
		t.Fatal("expected error for 200 body with unauthorized resale phrase")
	}
	if err.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", err.Code)
	}
}

func TestDetectUpstreamErrorBody_200ToSViolationPlaintext(t *testing.T) {
	body := []byte("Your account has been disabled for violating the terms of service.")
	err := DetectUpstreamErrorBody(http.StatusOK, body)
	if err == nil {
		t.Fatal("expected error for 200 body with ToS violation phrase")
	}
	if err.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", err.Code)
	}
}

func TestDetectUpstreamErrorBody_200AccountSuspendedPlaintext(t *testing.T) {
	body := []byte("account has been suspended")
	err := DetectUpstreamErrorBody(http.StatusOK, body)
	if err == nil {
		t.Fatal("expected error for 200 body with account suspended phrase")
	}
	if err.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", err.Code)
	}
}

func TestDetectUpstreamErrorBody_200InvalidAPIKeyPlaintext(t *testing.T) {
	body := []byte("Invalid API key provided.")
	err := DetectUpstreamErrorBody(http.StatusOK, body)
	if err == nil {
		t.Fatal("expected error for 200 body with invalid api key phrase")
	}
	if err.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", err.Code)
	}
}

func TestDetectUpstreamErrorBody_200NormalPlaintextNoMatch(t *testing.T) {
	body := []byte("Hello world, this is a normal plain-text response.")
	err := DetectUpstreamErrorBody(http.StatusOK, body)
	if err != nil {
		t.Fatalf("expected nil for benign plain-text 200 body, got %v", err)
	}
}

func TestDetectUpstreamErrorBody_200KeyDisabledInJSONMessage(t *testing.T) {
	body := []byte(`{"error":{"message":"API key has been disabled","type":"invalid_request_error"}}`)
	err := DetectUpstreamErrorBody(http.StatusOK, body)
	if err == nil {
		t.Fatal("expected error for JSON body with key-disabled message")
	}
	if err.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", err.Code)
	}
}

func TestDetectUpstreamErrorBody_200ToSViolationInJSONMessage(t *testing.T) {
	body := []byte(`{"error":{"message":"Your key was disabled for violating the terms of service.","type":"policy_violation"}}`)
	err := DetectUpstreamErrorBody(http.StatusOK, body)
	if err == nil {
		t.Fatal("expected error for JSON body with ToS violation message")
	}
	if err.Code != http.StatusForbidden {
		t.Fatalf("expected status 403, got %d", err.Code)
	}
}
