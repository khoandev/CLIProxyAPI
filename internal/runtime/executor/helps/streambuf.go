package helps

import (
	"bytes"
	"sync"
)

// scanBufferSize is the initial bufio.Scanner line buffer size installed from
// the pool. bufio.Scanner only uses the provided buffer while a token fits in
// it; when a token outgrows it the scanner allocates its own larger buffer and
// the pooled slice is never touched again, so releasing after the scan loop is
// always safe.
const scanBufferSize = 32 << 10

// scanBufferPool recycles the initial bufio.Scanner line buffers used by the
// streaming executors.
var scanBufferPool = sync.Pool{New: func() any { buf := make([]byte, 0, scanBufferSize); return &buf }}

// AcquireScanBuffer returns a zero-length scanner buffer from the pool.
func AcquireScanBuffer() []byte {
	return (*scanBufferPool.Get().(*[]byte))[:0]
}

// ReleaseScanBuffer returns buf to the pool. Buffers whose capacity no longer
// matches the pool size (grown by a caller or foreign) are dropped.
func ReleaseScanBuffer(buf []byte) {
	if cap(buf) != scanBufferSize {
		return
	}
	b := buf[:0]
	scanBufferPool.Put(&b)
}

// sseEventPool recycles the bytes.Buffer used to accumulate one SSE event.
var sseEventPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

// AcquireSSEEventBuffer returns a reset event buffer from the pool.
func AcquireSSEEventBuffer() *bytes.Buffer {
	buf := sseEventPool.Get().(*bytes.Buffer)
	buf.Reset()
	return buf
}

// ReleaseSSEEventBuffer returns buf to the pool unless it grew oversized;
// oversized buffers are dropped so the pool never retains large allocations.
func ReleaseSSEEventBuffer(buf *bytes.Buffer) {
	if buf == nil || buf.Cap() > 256<<10 {
		return
	}
	sseEventPool.Put(buf)
}
