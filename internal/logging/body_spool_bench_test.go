package logging

import (
	"bytes"
	"testing"
)

func benchmarkBodySpool(b *testing.B, limit int) {
	dir := b.TempDir()
	chunk := bytes.Repeat([]byte("s"), 30)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		spool := newBodySpool(dir, limit)
		for j := 0; j < 2000; j++ {
			if _, errWrite := spool.Write(chunk); errWrite != nil {
				b.Fatalf("Write error: %v", errWrite)
			}
		}
		spool.Cleanup()
	}
}

func BenchmarkBodySpoolMemory(b *testing.B) {
	benchmarkBodySpool(b, defaultSpoolMemoryLimit)
}

func BenchmarkBodySpoolSpilled(b *testing.B) {
	benchmarkBodySpool(b, 0)
}
