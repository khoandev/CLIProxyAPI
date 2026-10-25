package handlers

import (
	"bytes"
	"fmt"
	"io"
	"testing"
)

func TestSSEFrameWriterWritesDataFrames(t *testing.T) {
	var sink bytes.Buffer
	var writer SSEFrameWriter

	const frames = 100
	var want bytes.Buffer
	for i := 0; i < frames; i++ {
		chunk := []byte(fmt.Sprintf(`{"i":%d}`, i))
		if errWrite := writer.Write(&sink, chunk); errWrite != nil {
			t.Fatalf("Write: %v", errWrite)
		}
		want.WriteString("data: ")
		want.Write(chunk)
		want.WriteString("\n\n")
	}
	if sink.String() != want.String() {
		t.Fatalf("frames = %q, want %q", sink.String(), want.String())
	}
}

// TestSSEFrameWriterReusesBuffer proves the per-chunk frame allocation is gone:
// once the buffer has grown, writing a chunk of the same size allocates nothing.
func TestSSEFrameWriterReusesBuffer(t *testing.T) {
	var writer SSEFrameWriter
	chunk := []byte(`{"id":"chatcmpl-1","choices":[{"delta":{"content":"hi"}}]}`)
	if errWrite := writer.Write(io.Discard, chunk); errWrite != nil {
		t.Fatalf("Write: %v", errWrite)
	}

	if allocs := testing.AllocsPerRun(100, func() {
		if errWrite := writer.Write(io.Discard, chunk); errWrite != nil {
			t.Fatalf("Write: %v", errWrite)
		}
	}); allocs != 0 {
		t.Fatalf("allocs/run = %v, want 0 after the buffer has grown", allocs)
	}
}
