// Package errorfilter sanitizes error payloads before they reach API clients.
//
// Upstream gateways in front of this proxy answer in Chinese, and those strings
// used to reach clients verbatim. Some also expose internal routing state such
// as per-model cooldowns or account availability. Every error path funnels
// through the helpers here so such a message is replaced by a fixed generic
// string while the envelope classification (type/code) survives when it is ASCII.
package errorfilter

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/MoeForge/moego/ojson"
)

// GenericErrorMessage is the only message the proxy returns for an error whose
// original text contains CJK characters or exposes internal routing state. The
// branding suffix matches handlers.withErrorBranding, so that helper will not
// append a second suffix.
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

// internalDetailMarkers are lowercase fragments that mark an error as leaking
// internal routing state (model/credential cooldown, account availability)
// instead of describing the client's request. Those payloads are replaced by
// GenericErrorMessage while the envelope type/code still classify the failure.
var internalDetailMarkers = [...]string{
	"unavailable on every account",
	"per-model cooldown",
	"try another model",
	"model is unavailable",
}

// internalDetailCodes mark the same class of failure when the accompanying
// message is already generic.
var internalDetailCodes = [...]string{
	"model_unavailable",
}

// IsInternalDetail reports whether s describes internal routing state that must
// not reach clients.
func IsInternalDetail(s string) bool {
	for _, marker := range internalDetailMarkers {
		if containsFold(s, marker) {
			return true
		}
	}
	return false
}

func isInternalDetailCode(code string) bool {
	trimmed := strings.TrimSpace(code)
	for _, candidate := range internalDetailCodes {
		if strings.EqualFold(trimmed, candidate) {
			return true
		}
	}
	return false
}

// containsFold reports whether s contains sub, folding ASCII case without
// allocating.
func containsFold[S ~string | ~[]byte](s S, sub string) bool {
	if len(sub) == 0 || len(s) < len(sub) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		matched := true
		for j := range len(sub) {
			c := s[i+j]
			if 'A' <= c && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != sub[j] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// SanitizeMessage returns GenericErrorMessage when msg contains CJK or exposes
// internal routing state, else msg unchanged.
func SanitizeMessage(msg string) string {
	if !ContainsCJK(msg) && !IsInternalDetail(msg) {
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
	errValue := ojson.GetBytesNoCopy(body, "error")
	return errValue.Exists() && errValue.Type != ojson.Null
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

// SanitizeJSONErrorBody replaces CJK text and internal routing detail inside a
// JSON error body, preserving the envelope type/code when they are ASCII.
// Returns the body to use and whether it changed.
//
// The sjson writes here must keep their default allocating behavior: callers pass
// buffers that other readers may still alias, so an in-place rewrite would corrupt
// every ojson.Result already derived from those bytes (see
// util.TestNoInPlaceSJSONWrites). This path only runs for bodies that carry CJK
// or internal detail.
func SanitizeJSONErrorBody(body []byte) ([]byte, bool) {
	internalDetail := hasInternalDetailEnvelope(body)
	if !internalDetail && !containsCJKBytes(body) {
		return body, false
	}

	asciiType := asciiEnvelopeField(body, "error.type")
	asciiCode := asciiEnvelopeField(body, "error.code")

	if internalDetail {
		// Rebuild instead of rewriting fields: the original payload can also
		// carry cooldown/provider fields that must not reach clients.
		return BuildGenericErrorBody(asciiType, asciiCode), true
	}

	out := body
	errValue := ojson.GetBytesNoCopy(body, "error")
	switch {
	case errValue.Type == ojson.String:
		if ContainsCJK(errValue.String()) {
			if updated, errSet := ojson.SetBytes(out, "error", GenericErrorMessage); errSet == nil {
				out = updated
			}
		}
	case errValue.IsObject():
		out, _ = sanitizeErrorObject(out, "error")
	}
	if nested := ojson.GetBytesNoCopy(body, "response.error"); nested.IsObject() {
		out, _ = sanitizeErrorObject(out, "response.error")
	}
	if !errValue.Exists() {
		// Some gateways report the failure as a bare top-level message.
		if msg := ojson.GetBytesNoCopy(out, "message"); msg.Type == ojson.String && ContainsCJK(msg.String()) {
			if updated, errSet := ojson.SetBytes(out, "message", GenericErrorMessage); errSet == nil {
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

// hasInternalDetailEnvelope reports whether a JSON error envelope carries
// internal routing detail in its message or code.
func hasInternalDetailEnvelope(body []byte) bool {
	for _, path := range [...]string{"error.message", "error", "response.error.message", "message"} {
		value := ojson.GetBytesNoCopy(body, path)
		if value.Type == ojson.String && IsInternalDetail(value.String()) {
			return true
		}
	}
	for _, path := range [...]string{"error.code", "response.error.code"} {
		value := ojson.GetBytesNoCopy(body, path)
		if value.Type == ojson.String && isInternalDetailCode(value.String()) {
			return true
		}
	}
	return false
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
		value := ojson.GetBytesNoCopy(out, key)
		if value.Type != ojson.String || !ContainsCJK(value.String()) {
			continue
		}
		if updated, errSet := ojson.SetBytes(out, key, field.value); errSet == nil {
			out = updated
		}
		changed = true
	}
	return out, changed
}

// asciiEnvelopeField returns the string value at path when it is non-empty and
// free of CJK, otherwise "".
func asciiEnvelopeField(body []byte, path string) string {
	value := ojson.GetBytesNoCopy(body, path)
	if value.Type != ojson.String {
		return ""
	}
	text := value.String()
	if text == "" || ContainsCJK(text) {
		return ""
	}
	return text
}

// SanitizeErrorBody sanitizes any error body: JSON error envelopes are rewritten
// field-by-field, any other body containing CJK or internal routing detail is
// replaced wholesale.
func SanitizeErrorBody(body []byte) []byte {
	if !containsCJKBytes(body) && !containsInternalDetailBytes(body) {
		return body
	}
	if IsJSONErrorBody(body) {
		sanitized, _ := SanitizeJSONErrorBody(body)
		return sanitized
	}
	return []byte(GenericErrorMessage)
}

func containsInternalDetailBytes(body []byte) bool {
	for _, marker := range internalDetailMarkers {
		if containsFold(body, marker) {
			return true
		}
	}
	return false
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
