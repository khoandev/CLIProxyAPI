package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// countingBody records how often the request body handed out content.
//
// io.ReadAll issues an extra read that only reports EOF, so the counter tracks
// the reads that actually returned payload bytes. A body consumed twice
// therefore counts two, because the restored body is read from its start
// again.
type countingBody struct {
	reader io.Reader
	reads  int
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	if n > 0 {
		b.reads++
	}
	return n, err
}

func (b *countingBody) Close() error { return nil }

// TestMaskingReadsRequestBodyOnce proves the masking middleware reads the
// request body a single time per request. The body can be several megabytes,
// and reading it once per rule check multiplied the copy cost of every
// request.
func TestMaskingReadsRequestBodyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := &countingBody{reader: strings.NewReader(`{"model":"gpt-5.6-sol"}`)}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	if set := maskRuleSetForRequest(c); len(set.rules) == 0 {
		t.Fatal("expected masking rules for a gpt-5.6 model")
	}
	if body.reads != 1 {
		t.Fatalf("request body reads = %d, want 1", body.reads)
	}
}

// TestMaskingKeepsRequestBodyReadable proves the body stays available for the
// downstream handler after the masking middleware inspected it.
func TestMaskingKeepsRequestBodyReadable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	payload := `{"model":"gemini-2.5-pro","messages":[]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	if !shouldMaskAntigravityRequest(c, maskHints(c), true) {
		t.Fatal("expected antigravity masking for a gemini model")
	}
	rest, err := io.ReadAll(c.Request.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if string(rest) != payload {
		t.Fatalf("restored body = %q, want %q", rest, payload)
	}
}
