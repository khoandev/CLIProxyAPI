package executor

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func zstdCompressForTest(t *testing.T, payload []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	encoder, errWriter := zstd.NewWriter(&compressed)
	if errWriter != nil {
		t.Fatalf("zstd.NewWriter: %v", errWriter)
	}
	if _, errWrite := encoder.Write(payload); errWrite != nil {
		t.Fatalf("zstd write: %v", errWrite)
	}
	if errClose := encoder.Close(); errClose != nil {
		t.Fatalf("zstd close: %v", errClose)
	}
	return compressed.Bytes()
}

// TestDecodeResponseBodyZstdRoundTrip proves both zstd paths (declared header and
// magic-byte sniffing) still decode a streamed SSE payload byte for byte with the
// lean decoder options.
func TestDecodeResponseBodyZstdRoundTrip(t *testing.T) {
	payload := []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"text\":\"hello\"}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	compressed := zstdCompressForTest(t, payload)

	t.Run("content-encoding header", func(t *testing.T) {
		header := http.Header{}
		header.Set("Content-Encoding", "zstd")
		body, errDecode := decodeResponseBody(io.NopCloser(bytes.NewReader(compressed)), claudeResponseContentEncoding(header))
		if errDecode != nil {
			t.Fatalf("decodeResponseBody: %v", errDecode)
		}
		defer func() { _ = body.Close() }()
		decoded, errRead := io.ReadAll(body)
		if errRead != nil {
			t.Fatalf("read decoded body: %v", errRead)
		}
		if !bytes.Equal(decoded, payload) {
			t.Fatalf("decoded = %q, want %q", decoded, payload)
		}
	})

	t.Run("magic bytes without header", func(t *testing.T) {
		body, errDecode := decodeResponseBody(io.NopCloser(bytes.NewReader(compressed)), "")
		if errDecode != nil {
			t.Fatalf("decodeResponseBody: %v", errDecode)
		}
		defer func() { _ = body.Close() }()
		decoded, errRead := io.ReadAll(body)
		if errRead != nil {
			t.Fatalf("read decoded body: %v", errRead)
		}
		if !bytes.Equal(decoded, payload) {
			t.Fatalf("decoded = %q, want %q", decoded, payload)
		}
	})
}
