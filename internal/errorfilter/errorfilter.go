// Package errorfilter sanitizes error payloads before they reach API clients.
//
// Upstream gateways in front of this proxy answer in Chinese, and those strings
// used to reach clients verbatim. Every error path funnels through the helpers
// here so a CJK message is replaced by a fixed generic string while the envelope
// classification (type/code) survives when it is ASCII.
package errorfilter

import (
	"encoding/json"
	"unicode"
	"unicode/utf8"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// GenericErrorMessage is the only message the proxy returns for an error whose
// original text contains CJK characters. The branding suffix matches
// handlers.withErrorBranding, so that helper will not append a second suffix.
const GenericErrorMessage = "Upstream service error (llmgate.app)"

const (
	defaultErrorType = "server_error"
	defaultErrorCode = "upstream_error"
)

// genericFallbackBody is the last-resort payload used when json.Marshal of the
// generic envelope fails (it cannot for these inputs, so this is purely defensive).
const genericFallbackBody = `{"error":{"message":"Upstream service error (llmgate.app)","type":"server_error","code":"upstream_error"}}`

// ContainsCJK reports whether s contains any Han, Hiragana, Katakana, or Hangul rune.
func ContainsCJK(s string) bool {
	for _, r := range s {
		if isCJKRune(r) {
			return true
		}
	}
	return false
}

// containsCJKBytes is the allocation-free form of ContainsCJK used on response bodies.
func containsCJKBytes(b []byte) bool {
	for i := 0; i < len(b); {
		if b[i] < utf8.RuneSelf {
			i++
			continue
		}
		r, size := utf8.DecodeRune(b[i:])
		if size <= 0 || (r == utf8.RuneError && size == 1) {
			i++
			continue
		}
		if isCJKRune(r) {
			return true
		}
		i += size
	}
	return false
}

func isCJKRune(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// SanitizeMessage returns GenericErrorMessage when msg contains CJK, else msg unchanged.
func SanitizeMessage(msg string) string {
	if !ContainsCJK(msg) {
		return msg
	}
	return GenericErrorMessage
}

// IsJSONErrorBody reports whether body is a JSON object carrying a non-null
// top-level "error" member.
func IsJSONErrorBody(body []byte) bool {
	start := firstNonSpaceByte(body)
	if start < 0 || body[start] != '{' {
		return false
	}
	if !json.Valid(body) {
		return false
	}
	errValue := gjson.GetBytes(body, "error")
	return errValue.Exists() && errValue.Type != gjson.Null
}

func firstNonSpaceByte(body []byte) int {
	for i := range body {
		switch body[i] {
		case ' ', '\t', '\n', '\r':
			continue
		default:
			return i
		}
	}
	return -1
}

// SanitizeJSONErrorBody replaces CJK text inside a JSON error body, preserving the
// envelope type/code when they are ASCII. Returns the body to use and whether it changed.
//
// The sjson writes here must keep their default allocating behavior: callers pass
// buffers that other readers may still alias, so an in-place rewrite would corrupt
// every gjson.Result already derived from those bytes (see
// util.TestNoInPlaceSJSONWrites). This path only runs for bodies that carry CJK.
func SanitizeJSONErrorBody(body []byte) ([]byte, bool) {
	if !containsCJKBytes(body) {
		return body, false
	}

	asciiType := asciiEnvelopeField(body, "error.type")
	asciiCode := asciiEnvelopeField(body, "error.code")

	out := body
	errValue := gjson.GetBytes(body, "error")
	switch {
	case errValue.Type == gjson.String:
		if ContainsCJK(errValue.String()) {
			if updated, errSet := sjson.SetBytes(out, "error", GenericErrorMessage); errSet == nil {
				out = updated
			}
		}
	case errValue.IsObject():
		out, _ = sanitizeErrorObject(out, "error")
	}
	if nested := gjson.GetBytes(body, "response.error"); nested.IsObject() {
		out, _ = sanitizeErrorObject(out, "response.error")
	}
	if !errValue.Exists() {
		// Some gateways report the failure as a bare top-level message.
		if msg := gjson.GetBytes(out, "message"); msg.Type == gjson.String && ContainsCJK(msg.String()) {
			if updated, errSet := sjson.SetBytes(out, "message", GenericErrorMessage); errSet == nil {
				out = updated
			}
		}
	}

	if containsCJKBytes(out) {
		// CJK survived in a field this sanitizer does not model; the envelope shape
		// is no longer trustworthy, so fall back to a fully generic body.
		return BuildGenericErrorBody(asciiType, asciiCode), true
	}
	return out, true
}

// sanitizeErrorObject replaces CJK message/code/type inside the error object at path.
func sanitizeErrorObject(body []byte, path string) ([]byte, bool) {
	out := body
	changed := false
	for _, field := range [...]struct {
		name  string
		value string
	}{
		{name: "message", value: GenericErrorMessage},
		{name: "code", value: defaultErrorCode},
		{name: "type", value: defaultErrorType},
	} {
		key := path + "." + field.name
		value := gjson.GetBytes(out, key)
		if value.Type != gjson.String || !ContainsCJK(value.String()) {
			continue
		}
		if updated, errSet := sjson.SetBytes(out, key, field.value); errSet == nil {
			out = updated
		}
		changed = true
	}
	return out, changed
}

// asciiEnvelopeField returns the string value at path when it is non-empty and
// free of CJK, otherwise "".
func asciiEnvelopeField(body []byte, path string) string {
	value := gjson.GetBytes(body, path)
	if value.Type != gjson.String {
		return ""
	}
	text := value.String()
	if text == "" || ContainsCJK(text) {
		return ""
	}
	return text
}

// SanitizeErrorBody sanitizes any error body: JSON error envelopes are rewritten
// field-by-field, any other body containing CJK is replaced wholesale.
func SanitizeErrorBody(body []byte) []byte {
	if !containsCJKBytes(body) {
		return body
	}
	if IsJSONErrorBody(body) {
		sanitized, _ := SanitizeJSONErrorBody(body)
		return sanitized
	}
	return []byte(GenericErrorMessage)
}

// BuildGenericErrorBody returns {"error":{"message":GenericErrorMessage,"type":typ,"code":code}}.
// typ/code default to "server_error"/"upstream_error" when empty or containing CJK.
func BuildGenericErrorBody(typ, code string) []byte {
	if typ == "" || ContainsCJK(typ) {
		typ = defaultErrorType
	}
	if code == "" || ContainsCJK(code) {
		code = defaultErrorCode
	}
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	envelope.Error.Message = GenericErrorMessage
	envelope.Error.Type = typ
	envelope.Error.Code = code
	data, errMarshal := json.Marshal(envelope)
	if errMarshal != nil {
		return []byte(genericFallbackBody)
	}
	return data
}
