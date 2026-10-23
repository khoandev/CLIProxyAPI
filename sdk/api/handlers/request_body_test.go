package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
)

// countingRequestBody records how often the request body was read from the
// network.
type countingRequestBody struct {
	reader *bytes.Reader
	reads  int
}

func (b *countingRequestBody) Read(p []byte) (int, error) {
	n, err := b.reader.Read(p)
	if n > 0 {
		b.reads++
	}
	return n, err
}

func (b *countingRequestBody) Close() error { return nil }

// TestReadRequestBodyReadsBodyOnce proves the handler reads the request body
// from the network once.
func TestReadRequestBodyReadsBodyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const payload = `{"model":"gpt-5"}`
	body := &countingRequestBody{reader: bytes.NewReader([]byte(payload))}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", body)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	raw, err := ReadRequestBody(c)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	if string(raw) != payload {
		t.Fatalf("raw body = %q, want %q", raw, payload)
	}
	if body.reads != 1 {
		t.Fatalf("request body reads = %d, want 1", body.reads)
	}
}

// TestReadRequestBodyDecodesZstd proves an encoded request body still decodes
// after the shared body read.
func TestReadRequestBodyDecodesZstd(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const payload = `{"model":"gpt-5"}`

	var encoded bytes.Buffer
	encoder, err := zstd.NewWriter(&encoded)
	if err != nil {
		t.Fatalf("create zstd writer: %v", err)
	}
	if _, err = encoder.Write([]byte(payload)); err != nil {
		t.Fatalf("write zstd payload: %v", err)
	}
	if err = encoder.Close(); err != nil {
		t.Fatalf("close zstd writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(encoded.Bytes()))
	req.Header.Set("Content-Encoding", "zstd")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	decoded, err := ReadRequestBody(c)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	if string(decoded) != payload {
		t.Fatalf("decoded body = %q, want %q", decoded, payload)
	}
}

// TestReadRequestBodyKeepsBodyReadable proves the request body stays readable
// after the handler read it.
func TestReadRequestBodyKeepsBodyReadable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const payload = `{"model":"gpt-5"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(payload)))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	if _, err := ReadRequestBody(c); err != nil {
		t.Fatalf("read request body: %v", err)
	}
	second, err := ReadRequestBody(c)
	if err != nil {
		t.Fatalf("read request body again: %v", err)
	}
	if string(second) != payload {
		t.Fatalf("second body read = %q, want %q", second, payload)
	}
}
