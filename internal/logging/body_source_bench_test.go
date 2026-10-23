package logging

import (
	"bytes"
	"testing"
)

// benchmarkFileBodySourceAppend appends one request section (header text, body,
// empty marker, terminator) and then chunks response payloads, mirroring the
// per-request logging path. The logs directory is created once outside the timed
// region; Cleanup runs untimed because it is request teardown.
//
// Only exported entry points are used, so the same file runs against the
// pre-change baseline worktree.
func benchmarkFileBodySourceAppend(b *testing.B, chunks int, chunkSize int) {
	b.Helper()
	b.ReportAllocs()
	logsDir := b.TempDir()
	header := []byte("=== API REQUEST 1 ===\nTimestamp: 2026-05-25T12:00:00Z\nHeaders: 4\n")
	body := bytes.Repeat([]byte("r"), 1024)
	empty := []byte("<empty>")
	terminator := []byte("\n\n")
	payload := bytes.Repeat([]byte("x"), chunkSize)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		source, errSource := NewFileBodySourceInDir(logsDir, "bench")
		if errSource != nil {
			b.Fatalf("NewFileBodySourceInDir: %v", errSource)
		}
		if errAppend := source.AppendBytes(header); errAppend != nil {
			b.Fatalf("AppendBytes header: %v", errAppend)
		}
		if errAppend := source.AppendBytes(body); errAppend != nil {
			b.Fatalf("AppendBytes body: %v", errAppend)
		}
		if errAppend := source.AppendBytes(empty); errAppend != nil {
			b.Fatalf("AppendBytes empty: %v", errAppend)
		}
		if errAppend := source.AppendBytes(terminator); errAppend != nil {
			b.Fatalf("AppendBytes terminator: %v", errAppend)
		}
		for c := 0; c < chunks; c++ {
			if errAppend := source.AppendBytes(payload); errAppend != nil {
				b.Fatalf("AppendBytes chunk %d: %v", c, errAppend)
			}
		}
		b.StopTimer()
		if errCleanup := source.Cleanup(); errCleanup != nil {
			b.Fatalf("Cleanup: %v", errCleanup)
		}
		b.StartTimer()
	}
}

func BenchmarkFileBodySourceAppend20Chunks(b *testing.B) {
	benchmarkFileBodySourceAppend(b, 20, 120)
}

func BenchmarkFileBodySourceAppend200Chunks(b *testing.B) {
	benchmarkFileBodySourceAppend(b, 200, 120)
}

func BenchmarkFileBodySourceAppendLargeSpill(b *testing.B) {
	benchmarkFileBodySourceAppend(b, 200, 8<<10)
}
