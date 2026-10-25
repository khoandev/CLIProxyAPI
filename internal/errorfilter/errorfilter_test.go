package errorfilter

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/tidwall/gjson"
)

func TestContainsCJK(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "simplified chinese", in: "错误", want: true},
		{name: "japanese katakana", in: "エラー", want: true},
		{name: "korean hangul", in: "오류", want: true},
		{name: "mixed sentence", in: "配额不足", want: true},
		{name: "english", in: "quota exceeded", want: false},
		{name: "vietnamese diacritics", in: "Lỗi hệ thống", want: false},
		{name: "emoji", in: "🚀", want: false},
		{name: "empty", in: "", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ContainsCJK(tc.in); got != tc.want {
				t.Fatalf("ContainsCJK(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitizeMessage(t *testing.T) {
	if got := SanitizeMessage("配额不足"); got != GenericErrorMessage {
		t.Fatalf("SanitizeMessage(CJK) = %q, want %q", got, GenericErrorMessage)
	}
	if got := SanitizeMessage("rate limited"); got != "rate limited" {
		t.Fatalf("SanitizeMessage(ASCII) = %q, want unchanged", got)
	}
}

func TestSanitizeJSONErrorBody(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		wantChanged bool
		wantMessage string
		wantType    string
		wantCode    string
	}{
		{
			name:        "openai envelope keeps ascii classification",
			body:        `{"error":{"message":"配额不足","type":"insufficient_quota","code":"insufficient_quota"}}`,
			wantChanged: true,
			wantMessage: GenericErrorMessage,
			wantType:    "insufficient_quota",
			wantCode:    "insufficient_quota",
		},
		{
			name:        "ascii message untouched",
			body:        `{"error":{"message":"rate limited"}}`,
			wantChanged: false,
			wantMessage: "rate limited",
		},
		{
			name:        "string error member",
			body:        `{"error":"请求失败"}`,
			wantChanged: true,
		},
		{
			name:        "nested response error",
			body:        `{"response":{"error":{"message":"失败","code":"x"}}}`,
			wantChanged: true,
			wantMessage: GenericErrorMessage,
			wantCode:    "x",
		},
		{
			name:        "unhandled field falls back to generic envelope",
			body:        `{"error":{"detail":"内部错误"}}`,
			wantChanged: true,
			wantMessage: GenericErrorMessage,
			wantType:    defaultErrorType,
			wantCode:    defaultErrorCode,
		},
		{
			name:        "cjk classification replaced",
			body:        `{"error":{"message":"quota exceeded","type":"错误","code":"错误码"}}`,
			wantChanged: true,
			wantMessage: "quota exceeded",
			wantType:    defaultErrorType,
			wantCode:    defaultErrorCode,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, changed := SanitizeJSONErrorBody([]byte(tc.body))
			if changed != tc.wantChanged {
				t.Fatalf("changed = %v, want %v (out=%s)", changed, tc.wantChanged, out)
			}
			if ContainsCJK(string(out)) {
				t.Fatalf("sanitized body still contains CJK: %s", out)
			}
			if !json.Valid(out) {
				t.Fatalf("sanitized body is not valid JSON: %s", out)
			}
			if !changed {
				if !bytes.Equal(out, []byte(tc.body)) {
					t.Fatalf("unchanged body was rewritten: %s", out)
				}
				return
			}
			if tc.body == `{"error":"请求失败"}` {
				if got := gjson.GetBytes(out, "error").String(); got != GenericErrorMessage {
					t.Fatalf("error = %q, want %q", got, GenericErrorMessage)
				}
				return
			}
			path := "error"
			if gjson.GetBytes(out, "response.error").IsObject() {
				path = "response.error"
			}
			if got := gjson.GetBytes(out, path+".message").String(); got != tc.wantMessage {
				t.Fatalf("message = %q, want %q", got, tc.wantMessage)
			}
			if tc.wantType != "" {
				if got := gjson.GetBytes(out, path+".type").String(); got != tc.wantType {
					t.Fatalf("type = %q, want %q", got, tc.wantType)
				}
			}
			if tc.wantCode != "" {
				if got := gjson.GetBytes(out, path+".code").String(); got != tc.wantCode {
					t.Fatalf("code = %q, want %q", got, tc.wantCode)
				}
			}
		})
	}
}

func TestIsJSONErrorBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "error envelope", body: `{"error":{"message":"配额不足"}}`, want: true},
		{name: "leading whitespace", body: "\n\t {\"error\":{\"message\":\"x\"}}", want: true},
		{name: "null error", body: `{"error":null}`, want: false},
		{name: "success body", body: `{"ok":true}`, want: false},
		{name: "json null", body: `null`, want: false},
		{name: "not json", body: `error: boom`, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsJSONErrorBody([]byte(tc.body)); got != tc.want {
				t.Fatalf("IsJSONErrorBody(%q) = %v, want %v", tc.body, got, tc.want)
			}
		})
	}
}

func TestSanitizeErrorBody(t *testing.T) {
	if got := string(SanitizeErrorBody([]byte("错误: 配额不足"))); got != GenericErrorMessage {
		t.Fatalf("plain text CJK body = %q, want %q", got, GenericErrorMessage)
	}
	envelope := `{"error":{"message":"配额不足","type":"insufficient_quota"}}`
	out := SanitizeErrorBody([]byte(envelope))
	if got := gjson.GetBytes(out, "error.message").String(); got != GenericErrorMessage {
		t.Fatalf("envelope message = %q, want %q", got, GenericErrorMessage)
	}
	if got := gjson.GetBytes(out, "error.type").String(); got != "insufficient_quota" {
		t.Fatalf("envelope type = %q, want preserved", got)
	}
	ascii := []byte(`{"error":{"message":"rate limited"}}`)
	if got := SanitizeErrorBody(ascii); !bytes.Equal(got, ascii) {
		t.Fatalf("ascii body rewritten: %s", got)
	}
}

func TestBuildGenericErrorBody(t *testing.T) {
	tests := []struct {
		name     string
		typ      string
		code     string
		wantType string
		wantCode string
	}{
		{name: "defaults", wantType: defaultErrorType, wantCode: defaultErrorCode},
		{name: "keeps ascii", typ: "insufficient_quota", code: "insufficient_quota", wantType: "insufficient_quota", wantCode: "insufficient_quota"},
		{name: "replaces cjk", typ: "错误", code: "错误码", wantType: defaultErrorType, wantCode: defaultErrorCode},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := BuildGenericErrorBody(tc.typ, tc.code)
			if got := gjson.GetBytes(out, "error.message").String(); got != GenericErrorMessage {
				t.Fatalf("message = %q, want %q", got, GenericErrorMessage)
			}
			if got := gjson.GetBytes(out, "error.type").String(); got != tc.wantType {
				t.Fatalf("type = %q, want %q", got, tc.wantType)
			}
			if got := gjson.GetBytes(out, "error.code").String(); got != tc.wantCode {
				t.Fatalf("code = %q, want %q", got, tc.wantCode)
			}
		})
	}
}
