package middleware

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
)

// TestRequestBodyIsSharedAcrossMiddlewareAndHandler proves the request body is
// read from the network once, even when the middleware and the handler both
// need it.
func TestRequestBodyIsSharedAcrossMiddlewareAndHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const payload = `{"model":"gpt-5.6-sol"}`
	body := &countingBody{reader: strings.NewReader(payload)}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	if set := maskRuleSetForRequest(c); len(set.rules) == 0 {
		t.Fatal("expected masking rules for a gpt-5.6 model")
	}
	shared, err := util.SharedRequestBody(c)
	if err != nil {
		t.Fatalf("shared request body: %v", err)
	}
	if string(shared) != payload {
		t.Fatalf("shared body = %q, want %q", shared, payload)
	}
	if body.reads != 1 {
		t.Fatalf("request body reads = %d, want 1", body.reads)
	}
}

// TestRequestLoggingAndMaskingShareOneBodyRead proves the request logging
// middleware and the masking middleware read the body once between them.
func TestRequestLoggingAndMaskingShareOneBodyRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const payload = `{"model":"gemini-2.5-pro"}`
	body := &countingBody{reader: strings.NewReader(payload)}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	info, err := captureRequestInfo(c, true)
	if err != nil {
		t.Fatalf("capture request info: %v", err)
	}
	if string(info.Body) != payload {
		t.Fatalf("captured body = %q, want %q", info.Body, payload)
	}
	if model := requestBodyModel(c); model != "gemini-2.5-pro" {
		t.Fatalf("request body model = %q, want gemini-2.5-pro", model)
	}
	if body.reads != 1 {
		t.Fatalf("request body reads = %d, want 1", body.reads)
	}
}

// TestMaskingKeepsMultipartBodyReadable proves the masking middleware leaves a
// multipart upload parsable.
func TestMaskingKeepsMultipartBodyReadable(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreateFormFile("file", "upload.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err = part.Write([]byte("hello")); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err = writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/files", &buffer)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	_ = maskRuleSetForRequest(c)

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		t.Fatalf("read multipart file: %v", err)
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read multipart content: %v", err)
	}
	if string(content) != "hello" {
		t.Fatalf("multipart content = %q, want hello", content)
	}
	if header.Filename != "upload.txt" {
		t.Fatalf("multipart filename = %q, want upload.txt", header.Filename)
	}
}
