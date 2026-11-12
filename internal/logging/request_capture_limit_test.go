package logging

import (
	"bytes"
	"testing"
	"time"
)

// TestDeferredAPIRequestBodyLimitIsConfigurable verifies that the deferred
// upstream request body limit can be lowered and that the default is restored.
func TestDeferredAPIRequestBodyLimitIsConfigurable(t *testing.T) {
	t.Cleanup(func() { SetDeferredAPIRequestBodyMaxBytes(0) })

	if limit := DeferredAPIRequestBodyMaxBytes(); limit != MaxDeferredAPIRequestBodyBytes {
		t.Fatalf("deferred request body limit = %d, want the default %d", limit, MaxDeferredAPIRequestBodyBytes)
	}

	SetDeferredAPIRequestBodyMaxBytes(4096)
	if limit := DeferredAPIRequestBodyMaxBytes(); limit != 4096 {
		t.Fatalf("deferred request body limit = %d, want 4096", limit)
	}

	SetDeferredAPIRequestBodyMaxBytes(0)
	if limit := DeferredAPIRequestBodyMaxBytes(); limit != MaxDeferredAPIRequestBodyBytes {
		t.Fatalf("deferred request body limit after the reset = %d, want the default %d", limit, MaxDeferredAPIRequestBodyBytes)
	}
}

// TestDeferredAPIRequestBodyKeepsBoundedSubslice verifies that a lowered limit
// keeps a bounded subslice of the body and never copies it.
func TestDeferredAPIRequestBodyKeepsBoundedSubslice(t *testing.T) {
	t.Cleanup(func() { SetDeferredAPIRequestBodyMaxBytes(0) })

	body := bytes.Repeat([]byte("a"), 8192)
	capture := NewDeferredAPICapture()
	defer capture.Close()

	SetDeferredAPIRequestBodyMaxBytes(1024)
	recorded, bytesUsed, ok := capture.RecordRequest(body, func(index int, capturedAt time.Time, capturedBody []byte, bodyEmpty, bodyTruncated bool) DeferredAPIRequest {
		return func() []byte { return capturedBody }
	})
	if !ok {
		t.Fatal("RecordRequest() did not record the request")
	}
	if bytesUsed != 1024 {
		t.Fatalf("bytes used = %d, want 1024", bytesUsed)
	}
	captured := recorded()
	if len(captured) != 1024 {
		t.Fatalf("recorded body = %d bytes, want 1024", len(captured))
	}
	// The captured body must alias the original buffer, not a copy of it.
	if &captured[0] != &body[0] {
		t.Fatal("the captured body must be a subslice of the original body")
	}
}
