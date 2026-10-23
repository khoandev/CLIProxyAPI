package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileBodySource_MemoryPartsNeedNoFiles(t *testing.T) {
	logsDir := t.TempDir()
	source, errSource := newFileBodySourceInDir(logsDir, "mem", 1<<16, 1<<20)
	if errSource != nil {
		t.Fatalf("newFileBodySourceInDir: %v", errSource)
	}
	if errAppend := source.AppendString("=== API REQUEST 1 ==="); errAppend != nil {
		t.Fatalf("AppendString: %v", errAppend)
	}
	if errAppend := source.AppendBytes([]byte("{}")); errAppend != nil {
		t.Fatalf("AppendBytes: %v", errAppend)
	}
	if errAppend := source.AppendPart([]byte("Event: a")); errAppend != nil {
		t.Fatalf("AppendPart: %v", errAppend)
	}
	if !source.HasPayload() {
		t.Fatal("HasPayload = false, want true")
	}
	if paths := source.Paths(); len(paths) != 0 {
		t.Fatalf("Paths = %v, want empty for in-memory parts", paths)
	}
	entries, errRead := os.ReadDir(logsDir)
	if errRead != nil {
		t.Fatalf("read logs dir: %v", errRead)
	}
	if len(entries) != 0 {
		t.Fatalf("logs dir entries = %d, want 0", len(entries))
	}

	raw, errBytes := source.Bytes()
	if errBytes != nil {
		t.Fatalf("Bytes: %v", errBytes)
	}
	// AppendBytes appends to the last part, so the header and body share one part;
	// AppendPart starts a new part and WriteTo inserts the single separator.
	want := "=== API REQUEST 1 ==={}\nEvent: a\n"
	if string(raw) != want {
		t.Fatalf("Bytes = %q, want %q", string(raw), want)
	}
	if errCleanup := source.Cleanup(); errCleanup != nil {
		t.Fatalf("Cleanup: %v", errCleanup)
	}
	if errCleanup := source.Cleanup(); errCleanup != nil {
		t.Fatalf("second Cleanup: %v", errCleanup)
	}
}

func TestFileBodySource_SpillsPartOverLimit(t *testing.T) {
	logsDir := t.TempDir()
	source, errSource := newFileBodySourceInDir(logsDir, "spill", 64<<10, 1<<20)
	if errSource != nil {
		t.Fatalf("newFileBodySourceInDir: %v", errSource)
	}
	chunk := bytes.Repeat([]byte("a"), 4096)
	for i := 0; i < 48; i++ {
		if errAppend := source.AppendBytes(chunk); errAppend != nil {
			t.Fatalf("AppendBytes %d: %v", i, errAppend)
		}
	}

	entries, errRead := os.ReadDir(logsDir)
	if errRead != nil {
		t.Fatalf("read logs dir: %v", errRead)
	}
	if len(entries) != 1 || !entries[0].IsDir() || !strings.HasPrefix(entries[0].Name(), "request-log-parts-spill-") {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("logs dir entries = %v, want one request-log-parts-spill-* dir", names)
	}
	partEntries, errReadDir := os.ReadDir(filepath.Join(logsDir, entries[0].Name()))
	if errReadDir != nil {
		t.Fatalf("read part dir: %v", errReadDir)
	}
	if len(partEntries) != 1 {
		t.Fatalf("part dir entries = %d, want 1", len(partEntries))
	}

	paths := source.Paths()
	if len(paths) != 1 {
		t.Fatalf("Paths = %v, want 1 spilled path", paths)
	}
	raw, errBytes := source.Bytes()
	if errBytes != nil {
		t.Fatalf("Bytes: %v", errBytes)
	}
	want := bytes.Repeat(chunk, 48)
	if !bytes.Equal(raw, want) {
		t.Fatalf("Bytes len = %d, want %d", len(raw), len(want))
	}
	if errCleanup := source.Cleanup(); errCleanup != nil {
		t.Fatalf("Cleanup: %v", errCleanup)
	}
	assertFileBodySourceCleaned(t, paths)
	if errCleanup := source.Cleanup(); errCleanup != nil {
		t.Fatalf("second Cleanup: %v", errCleanup)
	}
}

func TestFileBodySource_TotalLimitSpillsOldestParts(t *testing.T) {
	logsDir := t.TempDir()
	source, errSource := newFileBodySourceInDir(logsDir, "total", 4<<10, 8<<10)
	if errSource != nil {
		t.Fatalf("newFileBodySourceInDir: %v", errSource)
	}
	payload := bytes.Repeat([]byte("b"), 2048)
	// Each part holds one payload plus the trailing newline AppendPart adds, and
	// WriteTo joins parts with a single separator.
	var want bytes.Buffer
	for i := 0; i < 6; i++ {
		if errAppend := source.AppendPart(payload); errAppend != nil {
			t.Fatalf("AppendPart %d: %v", i, errAppend)
		}
		if i > 0 {
			want.WriteByte('\n')
		}
		want.Write(payload)
		want.WriteByte('\n')
	}
	if paths := source.Paths(); len(paths) == 0 {
		t.Fatal("expected at least one spilled part after exceeding the total limit")
	}
	raw, errBytes := source.Bytes()
	if errBytes != nil {
		t.Fatalf("Bytes: %v", errBytes)
	}
	if !bytes.Equal(raw, want.Bytes()) {
		t.Fatalf("Bytes = %q, want %q", string(raw), string(want.Bytes()))
	}
	if errCleanup := source.Cleanup(); errCleanup != nil {
		t.Fatalf("Cleanup: %v", errCleanup)
	}
}

func TestFileBodySource_RejectsUseAfterCleanup(t *testing.T) {
	source, errSource := newFileBodySourceInDir(t.TempDir(), "cleaned", 1<<16, 1<<20)
	if errSource != nil {
		t.Fatalf("newFileBodySourceInDir: %v", errSource)
	}
	if errCleanup := source.Cleanup(); errCleanup != nil {
		t.Fatalf("Cleanup: %v", errCleanup)
	}
	if errAppend := source.AppendBytes([]byte("x")); errAppend == nil || !strings.Contains(errAppend.Error(), "cleaned") {
		t.Fatalf("AppendBytes after cleanup err = %v, want cleaned error", errAppend)
	}
	if errAppend := source.AppendString("x"); errAppend == nil || !strings.Contains(errAppend.Error(), "cleaned") {
		t.Fatalf("AppendString after cleanup err = %v, want cleaned error", errAppend)
	}
	if errAppend := source.AppendPart([]byte("x")); errAppend == nil || !strings.Contains(errAppend.Error(), "cleaned") {
		t.Fatalf("AppendPart after cleanup err = %v, want cleaned error", errAppend)
	}
	if _, errPart := source.CreatePart("x"); errPart == nil || !strings.Contains(errPart.Error(), "cleaned") {
		t.Fatalf("CreatePart after cleanup err = %v, want cleaned error", errPart)
	}
}

func TestFileBodySource_AppendPartTrimsAndTerminates(t *testing.T) {
	source, errSource := newFileBodySourceInDir(t.TempDir(), "trim", 1<<16, 1<<20)
	if errSource != nil {
		t.Fatalf("newFileBodySourceInDir: %v", errSource)
	}
	defer func() {
		if errCleanup := source.Cleanup(); errCleanup != nil {
			t.Fatalf("Cleanup: %v", errCleanup)
		}
	}()

	if errAppend := source.AppendPart([]byte("  x  ")); errAppend != nil {
		t.Fatalf("AppendPart: %v", errAppend)
	}
	raw, errBytes := source.Bytes()
	if errBytes != nil {
		t.Fatalf("Bytes: %v", errBytes)
	}
	if string(raw) != "x\n" {
		t.Fatalf("Bytes = %q, want %q", string(raw), "x\n")
	}

	if errAppend := source.AppendPart([]byte("y\n")); errAppend != nil {
		t.Fatalf("AppendPart: %v", errAppend)
	}
	raw, errBytes = source.Bytes()
	if errBytes != nil {
		t.Fatalf("Bytes: %v", errBytes)
	}
	// The second part is not double-terminated: TrimSpace drops the trailing
	// newline, AppendPart adds exactly one back.
	if string(raw) != "x\n\ny\n" {
		t.Fatalf("Bytes = %q, want %q", string(raw), "x\n\ny\n")
	}
}

func TestFileBodySource_PartWriterRejectsWritesAfterClose(t *testing.T) {
	source, errSource := newFileBodySourceInDir(t.TempDir(), "writer", 1<<16, 1<<20)
	if errSource != nil {
		t.Fatalf("newFileBodySourceInDir: %v", errSource)
	}
	defer func() {
		if errCleanup := source.Cleanup(); errCleanup != nil {
			t.Fatalf("Cleanup: %v", errCleanup)
		}
	}()

	writer, errPart := source.CreatePart("body")
	if errPart != nil {
		t.Fatalf("CreatePart: %v", errPart)
	}
	if _, errWrite := writer.Write([]byte("payload")); errWrite != nil {
		t.Fatalf("Write: %v", errWrite)
	}
	if errClose := writer.Close(); errClose != nil {
		t.Fatalf("Close: %v", errClose)
	}
	if errClose := writer.Close(); errClose != nil {
		t.Fatalf("second Close: %v", errClose)
	}
	if _, errWrite := writer.Write([]byte("late")); errWrite == nil || !strings.Contains(errWrite.Error(), "closed") {
		t.Fatalf("Write after close err = %v, want closed error", errWrite)
	}
	raw, errBytes := source.Bytes()
	if errBytes != nil {
		t.Fatalf("Bytes: %v", errBytes)
	}
	if string(raw) != "payload" {
		t.Fatalf("Bytes = %q, want %q", string(raw), "payload")
	}
}
