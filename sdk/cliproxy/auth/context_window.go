package auth

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// ErrContextWindowExceeded is the normalized sentinel for context-limit
// failures across Codex terminal/stream/compact and conductor paths.
// Callers use errors.Is(err, ErrContextWindowExceeded) to detect it.
var ErrContextWindowExceeded = errors.New("context window exceeded")

// ContextWindowExceededError carries parsed context-limit metadata.
// MaxContext/RequestTokens are 0 when the upstream message omits them.
type ContextWindowExceededError struct {
	MaxContext    int
	RequestTokens int
	Provider      string
	Model         string
	Raw           string
}

func (e *ContextWindowExceededError) Error() string {
	if e == nil {
		return ErrContextWindowExceeded.Error()
	}
	if e.Raw != "" {
		return e.Raw
	}
	return ErrContextWindowExceeded.Error()
}

// Unwrap reports the normalized sentinel so errors.Is finds it.
func (e *ContextWindowExceededError) Unwrap() error { return ErrContextWindowExceeded }

var (
	contextLimitMaxRe    = regexp.MustCompile(`(?i)maximum context length is\s+([\d,]+)\s+tokens`)
	contextLimitActualRe = regexp.MustCompile(`(?i)resulted in\s+([\d,]+)\s+tokens`)
)

func parseContextTokenNumber(s string) (int, bool) {
	n, err := strconv.Atoi(strings.ReplaceAll(s, ",", ""))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// ParseContextLimit extracts (max, actual) token counts from messages like
// "This model's maximum context length is 272000 tokens, however your
// messages resulted in 272444 tokens". ok is true when the max is found;
// actual stays 0 when the message omits it.
func ParseContextLimit(msg string) (max, actual int, ok bool) {
	m := contextLimitMaxRe.FindStringSubmatch(msg)
	if m == nil {
		return 0, 0, false
	}
	max, ok = parseContextTokenNumber(m[1])
	if !ok {
		return 0, 0, false
	}
	if a := contextLimitActualRe.FindStringSubmatch(msg); a != nil {
		actual, _ = parseContextTokenNumber(a[1])
	}
	return max, actual, true
}

// IsContextWindowExceeded strictly matches context-limit failures.
// True only for context-specific codes (context_length_exceeded,
// model_context_window_exceeded, context_too_large) or messages pairing a
// context phrase (context window/length, too many tokens) with an exceeded
// indication (exceed, too long/many, maximum). Status-agnostic: a bare
// 400/429 with no such signal is never a match.
func IsContextWindowExceeded(_ int, code, typ, msg, reason string) bool {
	for _, candidate := range [...]string{
		strings.ToLower(strings.TrimSpace(code)),
		strings.ToLower(strings.TrimSpace(typ)),
	} {
		switch candidate {
		case "context_length_exceeded", "model_context_window_exceeded", "context_too_large":
			return true
		}
	}
	text := strings.ToLower(strings.TrimSpace(msg) + "\n" + strings.TrimSpace(reason))
	if strings.TrimSpace(text) == "" {
		return false
	}
	// Structured code embedded in a raw body string.
	for _, token := range [...]string{"context_length_exceeded", "model_context_window_exceeded", "context_too_large"} {
		if strings.Contains(text, token) {
			return true
		}
	}
	hasContext := strings.Contains(text, "context window") ||
		strings.Contains(text, "context length") ||
		strings.Contains(text, "context_length") ||
		strings.Contains(text, "too many tokens")
	if !hasContext {
		return false
	}
	for _, indication := range [...]string{"exceed", "too long", "too many", "maximum", "too-large", "too_large"} {
		if strings.Contains(text, indication) {
			return true
		}
	}
	return false
}
