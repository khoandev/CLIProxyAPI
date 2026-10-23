package helps

import (
	"bufio"
	"context"
	"strings"
	"testing"
)

func TestAcquireScanBufferReturnsPoolSizedBuffer(t *testing.T) {
	buf := AcquireScanBuffer()
	defer ReleaseScanBuffer(buf)
	if len(buf) != 0 {
		t.Fatalf("len = %d, want 0", len(buf))
	}
	if cap(buf) != scanBufferSize {
		t.Fatalf("cap = %d, want %d", cap(buf), scanBufferSize)
	}
}

func TestReleaseScanBufferDropsForeignCapacity(t *testing.T) {
	foreign := make([]byte, 0, 16)
	ReleaseScanBuffer(foreign)
	// The foreign buffer must never surface from the pool.
	for range scanBufferSize + 1 {
		buf := AcquireScanBuffer()
		if cap(buf) != scanBufferSize {
			t.Fatalf("acquired cap = %d, want %d", cap(buf), scanBufferSize)
		}
		ReleaseScanBuffer(buf)
	}
}

func TestReleaseSSEEventBufferDropsOversized(t *testing.T) {
	small := AcquireSSEEventBuffer()
	ReleaseSSEEventBuffer(small)
	for range 8 {
		buf := AcquireSSEEventBuffer()
		if buf.Len() != 0 {
			t.Fatalf("len = %d, want 0 after reset", buf.Len())
		}
		if buf.Cap() > 256<<10 {
			t.Fatalf("cap = %d, oversized buffer must not be pooled", buf.Cap())
		}
		ReleaseSSEEventBuffer(buf)
	}

	oversized := AcquireSSEEventBuffer()
	oversized.Grow(300 << 10)
	ReleaseSSEEventBuffer(oversized)
	for range 8 {
		buf := AcquireSSEEventBuffer()
		if buf.Cap() > 256<<10 {
			t.Fatalf("acquired cap = %d, oversized buffer leaked into pool", buf.Cap())
		}
		ReleaseSSEEventBuffer(buf)
	}
}

// TestCodexSSEScannerSmallMaxBytesRejectsLongLine guards the pooled-buffer guard in
// NewCodexSSEScanner: bufio.Scanner's max token size is max(maxBytes, cap(buf)), so a 32KB
// pooled buffer must not silently raise a smaller caller limit.
func TestCodexSSEScannerSmallMaxBytesRejectsLongLine(t *testing.T) {
	stream := "data: " + strings.Repeat("x", 100) + "\n"
	scanner := NewCodexSSEScanner(context.Background(), strings.NewReader(stream), 64)
	for scanner.Scan() {
	}
	if scanner.Err() != bufio.ErrTooLong {
		t.Fatalf("err = %v, want bufio.ErrTooLong", scanner.Err())
	}
	scanner.Close()
}
