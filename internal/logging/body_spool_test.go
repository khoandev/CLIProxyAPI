package logging

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func readAllSpool(t *testing.T, s *bodySpool) []byte {
	t.Helper()
	reader, err := s.Reader()
	if err != nil {
		t.Fatalf("Reader() error: %v", err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read error: %v", err)
	}
	if errClose := reader.Close(); errClose != nil {
		t.Fatalf("reader close error: %v", errClose)
	}
	return data
}

func spoolTempFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s) error: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestBodySpoolStaysInMemoryUnderLimit(t *testing.T) {
	dir := t.TempDir()
	spool := newBodySpool(dir, defaultSpoolMemoryLimit)
	defer spool.Cleanup()

	var want bytes.Buffer
	for i := 0; i < 10; i++ {
		chunk := bytes.Repeat([]byte{byte('a' + i)}, 1024)
		n, err := spool.Write(chunk)
		if err != nil {
			t.Fatalf("Write error: %v", err)
		}
		if n != len(chunk) {
			t.Fatalf("Write returned n=%d, want %d", n, len(chunk))
		}
		want.Write(chunk)
	}

	if spool.Path() != "" {
		t.Fatalf("spool spilled unexpectedly, path=%q", spool.Path())
	}
	if files := spoolTempFiles(t, dir); len(files) != 0 {
		t.Fatalf("temp dir not empty under limit: %v", files)
	}
	if got := readAllSpool(t, spool); !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("payload mismatch: got %d bytes, want %d bytes", len(got), want.Len())
	}
	if !bytes.Equal(spool.Bytes(), want.Bytes()) {
		t.Fatalf("Bytes() mismatch: got %d bytes, want %d bytes", len(spool.Bytes()), want.Len())
	}
}

func TestBodySpoolSpillsAndPreservesOrder(t *testing.T) {
	dir := t.TempDir()
	spool := newBodySpool(dir, 64*1024)

	var want bytes.Buffer
	chunk := bytes.Repeat([]byte("x"), 4096)
	for i := 0; i < 48; i++ {
		if _, err := spool.Write(chunk); err != nil {
			t.Fatalf("Write error: %v", err)
		}
		want.Write(chunk)
	}

	path := spool.Path()
	if path == "" {
		t.Fatal("spool did not spill past the limit")
	}
	if files := spoolTempFiles(t, dir); len(files) != 1 {
		t.Fatalf("want exactly 1 temp file, got %v", files)
	}
	if spool.Bytes() != nil {
		t.Fatalf("Bytes() must be nil after spill, got %d bytes", len(spool.Bytes()))
	}
	if got := readAllSpool(t, spool); !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("payload mismatch: got %d bytes, want %d bytes", len(got), want.Len())
	}

	spool.Cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("temp file still present after Cleanup: %v", err)
	}
	if files := spoolTempFiles(t, dir); len(files) != 0 {
		t.Fatalf("temp dir not empty after Cleanup: %v", files)
	}

	spool.Cleanup()
}

func TestBodySpoolSpillFailureLeavesBufferIntact(t *testing.T) {
	spool := newBodySpool(filepath.Join(t.TempDir(), "missing"), 8)
	defer spool.Cleanup()

	if _, err := spool.Write([]byte("abcd")); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	if _, err := spool.Write([]byte("efghij")); err == nil {
		t.Fatal("want error spilling into a missing directory")
	}
	if got := string(spool.Bytes()); got != "abcd" {
		t.Fatalf("buffer after failed spill = %q, want %q", got, "abcd")
	}
}
