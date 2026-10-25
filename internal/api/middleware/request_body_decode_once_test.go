package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
)

func zstdRequestBody(t *testing.T, payload []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	encoder, errNewWriter := zstd.NewWriter(&compressed)
	if errNewWriter != nil {
		t.Fatalf("zstd.NewWriter: %v", errNewWriter)
	}
	if _, errWrite := encoder.Write(payload); errWrite != nil {
		t.Fatalf("zstd write: %v", errWrite)
	}
	if errClose := encoder.Close(); errClose != nil {
		t.Fatalf("zstd close: %v", errClose)
	}
	return compressed.Bytes()
}

// TestRequestLoggingDecodesZstdBodyOnce proves the logging middleware and the
// handler share one decode: the handler returns the very slice the middleware
// produced instead of decompressing the body a second time.
func TestRequestLoggingDecodesZstdBodyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)

	payload := []byte(`{"model":"m","messages":[]}`)
	compressed := zstdRequestBody(t, payload)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(compressed))
	req.Header.Set("Content-Encoding", "zstd")
	c.Request = req

	// The middleware runs before the handler.
	info, errCapture := captureRequestInfo(c, true)
	if errCapture != nil {
		t.Fatalf("captureRequestInfo: %v", errCapture)
	}
	if !bytes.Equal(info.Body, payload) {
		t.Fatalf("logged request body = %q, want %q", string(info.Body), string(payload))
	}

	handlerBody, errRead := handlers.ReadRequestBody(c)
	if errRead != nil {
		t.Fatalf("ReadRequestBody: %v", errRead)
	}
	if !bytes.Equal(handlerBody, payload) {
		t.Fatalf("handler request body = %q, want %q", string(handlerBody), string(payload))
	}
	if len(handlerBody) == 0 || &handlerBody[0] != &info.Body[0] {
		t.Fatalf("handler decoded the body again instead of reusing the cached decode")
	}
}

// TestRequestLoggingDoesNotCacheIdentityBody keeps the uncompressed path
// unchanged: nothing is cached and the handler still sees the raw body.
func TestRequestLoggingDoesNotCacheIdentityBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	payload := []byte(`{"model":"m","messages":[]}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(payload))

	if _, errCapture := captureRequestInfo(c, true); errCapture != nil {
		t.Fatalf("captureRequestInfo: %v", errCapture)
	}
	if _, ok := util.CachedDecodedRequestBody(c); ok {
		t.Fatalf("identity body must not be cached as a decoded body")
	}

	handlerBody, errRead := handlers.ReadRequestBody(c)
	if errRead != nil {
		t.Fatalf("ReadRequestBody: %v", errRead)
	}
	if !bytes.Equal(handlerBody, payload) {
		t.Fatalf("handler request body = %q, want %q", string(handlerBody), string(payload))
	}
}

// TestRequestLoggingDoesNotCacheTruncatedDecode proves a truncated log body is
// never shared with the handler, which must still see the complete payload.
func TestRequestLoggingDoesNotCacheTruncatedDecode(t *testing.T) {
	gin.SetMode(gin.TestMode)

	payload := []byte(`{"model":"m","messages":[{"role":"user","content":"` + string(bytes.Repeat([]byte("a"), 4096)) + `"}]}`)
	compressed := zstdRequestBody(t, payload)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(compressed))
	req.Header.Set("Content-Encoding", "zstd")
	c.Request = req
	c.Set(handlers.MaxRequestBodyBytesContextKey, int64(1024))

	info, errCapture := captureRequestInfo(c, true)
	if errCapture != nil {
		t.Fatalf("captureRequestInfo: %v", errCapture)
	}
	if !bytes.Contains(info.Body, []byte("DECOMPRESSED REQUEST BODY TRUNCATED")) {
		t.Fatalf("logged body must be truncated, got %d bytes", len(info.Body))
	}
	if _, ok := util.CachedDecodedRequestBody(c); ok {
		t.Fatalf("truncated log body must not be cached as the decoded request body")
	}
}
