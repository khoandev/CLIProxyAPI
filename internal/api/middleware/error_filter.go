package middleware

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/errorfilter"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/interfaces"
	"github.com/tidwall/gjson"
)

const (
	sniffWindowBytes       = 4 << 10   // 2xx JSON prefix inspected before passing through
	maxErrorBodyBytes      = 64 << 10  // buffered 2xx body once an error envelope is detected
	maxStatusErrorBodySize = 256 << 10 // buffered body for status >= 400
)

// ErrorFilterMiddleware rewrites error payloads so no CJK text reaches a client,
// and promotes a 2xx response whose body is an error envelope to HTTP 502.
//
// Some upstream gateways answer 200 with {"error":{...}} in the body. Executors
// detect that before it reaches here, but plugin executors, the codex,
// antigravity, devin and meta executors, and the interactions paths do not, so
// those responses used to reach clients as successes. The promotion happens in
// the writer because that is the only place that sees every handler's output.
func ErrorFilterMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		w := &errorFilterWriter{ResponseWriter: c.Writer, ctx: c}
		c.Writer = w
		defer w.finish()
		c.Next()
	}
}

// errorFilterWriter delays the response decision until it knows whether the body
// is an error envelope, a plain error status, or an ordinary success.
type errorFilterWriter struct {
	gin.ResponseWriter
	ctx       *gin.Context
	status    int
	decided   bool
	streaming bool
	buffering bool
	errorSeen bool
	buf       []byte
	size      int
}

func (w *errorFilterWriter) WriteHeader(code int) {
	if w.decided {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	w.status = code
}

func (w *errorFilterWriter) WriteHeaderNow() {
	w.finish()
	w.ResponseWriter.WriteHeaderNow()
}

func (w *errorFilterWriter) Flush() {
	w.finish()
	w.ResponseWriter.Flush()
}

func (w *errorFilterWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.finish()
	return w.ResponseWriter.Hijack()
}

func (w *errorFilterWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *errorFilterWriter) Size() int {
	return w.size
}

func (w *errorFilterWriter) Written() bool {
	return w.decided
}

func (w *errorFilterWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *errorFilterWriter) Write(p []byte) (int, error) {
	if !w.decided && !w.buffering {
		w.begin()
	}
	if !w.buffering {
		return w.forward(p)
	}

	// p is already part of the buffer, so no branch below may write it again.
	w.buf = append(w.buf, p...)
	switch {
	case w.errorSeen:
		if len(w.buf) > maxErrorBodyBytes {
			w.abandon()
		}
	case w.status >= http.StatusBadRequest:
		if len(w.buf) > maxStatusErrorBodySize {
			w.abandon()
		}
	default:
		if w.detectErrorEnvelope() {
			w.errorSeen = true
			break
		}
		// Either the body is complete and carries no error envelope, or it grew
		// past the sniff window: let it through as an ordinary success.
		if json.Valid(w.buf) || len(w.buf) >= sniffWindowBytes {
			w.abandon()
		}
	}
	return len(p), nil
}

// begin picks the response strategy on the first write, when the status code and
// the content type are both final.
func (w *errorFilterWriter) begin() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	contentType := strings.ToLower(w.Header().Get("Content-Type"))
	switch {
	case strings.Contains(contentType, "text/event-stream") || strings.Contains(contentType, "application/x-ndjson"):
		// Status cannot change mid-stream; only the payload can be rewritten.
		w.streaming = true
		w.decided = true
		w.ResponseWriter.WriteHeader(w.status)
	case w.status >= http.StatusBadRequest:
		w.buffering = true
	case w.status < http.StatusMultipleChoices && isProxyAPIErrorPath(w.requestPath()) && (contentType == "" || strings.Contains(contentType, "json")):
		w.buffering = true
	default:
		w.decided = true
		w.ResponseWriter.WriteHeader(w.status)
	}
}

func (w *errorFilterWriter) requestPath() string {
	if w.ctx == nil || w.ctx.Request == nil || w.ctx.Request.URL == nil {
		return ""
	}
	return w.ctx.Request.URL.Path
}

func (w *errorFilterWriter) detectErrorEnvelope() bool {
	return errorfilter.IsJSONErrorBody(w.buf)
}

// abandon flushes the buffer as an ordinary response.
func (w *errorFilterWriter) abandon() {
	body := w.buf
	w.buf = nil
	w.buffering = false
	w.decided = true
	w.ResponseWriter.WriteHeader(w.status)
	if len(body) == 0 {
		return
	}
	if w.streaming {
		if sanitized, detected := sanitizeSSEErrorChunk(body); detected {
			recordAPIError(w.ctx, http.StatusBadGateway, string(sanitized))
			body = sanitized
		}
	}
	n, _ := w.ResponseWriter.Write(body)
	w.size += n
}

// finish resolves the response once the handler chain is done.
func (w *errorFilterWriter) finish() {
	if w.decided {
		return
	}
	if !w.buffering {
		// No body was written: honour a bare status code and otherwise leave the
		// underlying writer untouched so net/http emits its own default response.
		if w.status != 0 {
			w.decided = true
			w.ResponseWriter.WriteHeader(w.status)
		}
		return
	}
	if w.errorSeen || w.status >= http.StatusBadRequest {
		w.writeFinal()
		return
	}
	w.abandon()
}

// writeFinal emits a buffered error response, promoting a 2xx error envelope to 502.
func (w *errorFilterWriter) writeFinal() {
	body := w.buf
	finalStatus := w.status
	if w.errorSeen && w.status < http.StatusMultipleChoices {
		finalStatus = http.StatusBadGateway
		if sanitized, changed := errorfilter.SanitizeJSONErrorBody(body); changed {
			body = sanitized
		} else if !errorfilter.IsJSONErrorBody(body) {
			body = errorfilter.BuildGenericErrorBody(errorEnvelopeField(body, "error.type"), errorEnvelopeField(body, "error.code"))
		}
		recordAPIError(w.ctx, finalStatus, string(body))
	} else {
		sanitized := errorfilter.SanitizeErrorBody(body)
		if !bytes.Equal(sanitized, body) {
			recordAPIError(w.ctx, w.status, string(sanitized))
		}
		body = sanitized
	}

	w.Header().Del("Content-Length")
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.buf = nil
	w.buffering = false
	w.decided = true
	w.ResponseWriter.WriteHeader(finalStatus)
	if len(body) == 0 {
		return
	}
	n, _ := w.ResponseWriter.Write(body)
	w.size += n
}

func (w *errorFilterWriter) forward(p []byte) (int, error) {
	consumed := len(p)
	rewritten := false
	if w.streaming {
		if sanitized, detected := sanitizeSSEErrorChunk(p); detected {
			recordAPIError(w.ctx, http.StatusBadGateway, string(sanitized))
			rewritten = len(sanitized) != consumed
			p = sanitized
		}
	}
	n, errWrite := w.ResponseWriter.Write(p)
	w.size += n
	if errWrite == nil && rewritten {
		return consumed, nil
	}
	return n, errWrite
}

// isProxyAPIErrorPath limits the 200-to-502 promotion to proxy API routes.
// Management endpoints legitimately answer {"status":"error"} with 200.
func isProxyAPIErrorPath(path string) bool {
	if path == "/v1" || strings.HasPrefix(path, "/v1/") {
		return true
	}
	if path == "/v1beta" || strings.HasPrefix(path, "/v1beta/") {
		return true
	}
	return strings.HasPrefix(path, "/openai/v1/") || strings.HasPrefix(path, "/backend-api/")
}

// errorEnvelopeField reads a string field from an error envelope.
func errorEnvelopeField(body []byte, path string) string {
	value := gjson.GetBytes(body, path)
	if value.Type != gjson.String {
		return ""
	}
	return value.String()
}

// sanitizeSSEErrorChunk rewrites CJK text inside a server-sent event chunk and
// reports whether the chunk carried an error event.
func sanitizeSSEErrorChunk(chunk []byte) ([]byte, bool) {
	if !bytes.Contains(chunk, []byte(`"error"`)) && !bytes.Contains(chunk, []byte("event: error")) {
		return chunk, false
	}
	lines := bytes.Split(chunk, []byte("\n"))
	detected := false
	rewritten := false
	for i, line := range lines {
		trimmed := bytes.TrimSpace(line)
		if bytes.Equal(trimmed, []byte("event: error")) {
			detected = true
			continue
		}
		if !bytes.HasPrefix(trimmed, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(trimmed[len("data:"):])
		if !isSSEErrorPayload(payload) {
			continue
		}
		detected = true
		sanitized, changed := errorfilter.SanitizeJSONErrorBody(payload)
		if !changed {
			continue
		}
		lines[i] = append([]byte("data: "), sanitized...)
		rewritten = true
	}
	if !rewritten {
		return chunk, detected
	}
	return bytes.Join(lines, []byte("\n")), detected
}

func isSSEErrorPayload(payload []byte) bool {
	if !bytes.HasPrefix(payload, []byte(`{"error"`)) &&
		!bytes.HasPrefix(payload, []byte(`{"type":"error"`)) &&
		!bytes.HasPrefix(payload, []byte(`{"type": "error"`)) {
		return false
	}
	return errorfilter.IsJSONErrorBody(payload)
}

// recordAPIError marks the request as an error for request logging, using the
// same context key the handlers write.
func recordAPIError(c *gin.Context, status int, message string) {
	if c == nil {
		return
	}
	entry := &interfaces.ErrorMessage{StatusCode: status, Error: errors.New(message)}
	if existing, exists := c.Get("API_RESPONSE_ERROR"); exists {
		if apiErrors, ok := existing.([]*interfaces.ErrorMessage); ok {
			c.Set("API_RESPONSE_ERROR", append(apiErrors, entry))
			return
		}
	}
	c.Set("API_RESPONSE_ERROR", []*interfaces.ErrorMessage{entry})
}
