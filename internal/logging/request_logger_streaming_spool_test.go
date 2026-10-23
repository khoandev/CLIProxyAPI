package logging

import (
	"bytes"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deterministicPayload builds size bytes that are not compressible into a
// short repeating pattern, so a truncated or reordered log body cannot
// accidentally compare equal.
func deterministicPayload(size int) []byte {
	source := rand.New(rand.NewPCG(42, 43))
	payload := make([]byte, size)
	for i := 0; i < size; i++ {
		payload[i] = byte(source.Uint32())
	}
	return payload
}

func readOnlyLogFile(t *testing.T, logsDir string) []byte {
	t.Helper()
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		t.Fatalf("ReadDir(%s) error: %v", logsDir, err)
	}
	var logFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".log") {
			logFiles = append(logFiles, filepath.Join(logsDir, entry.Name()))
		}
	}
	if len(logFiles) != 1 {
		t.Fatalf("want exactly 1 log file in %s, got %v", logsDir, logFiles)
	}
	content, err := os.ReadFile(logFiles[0])
	if err != nil {
		t.Fatalf("read log file error: %v", err)
	}
	return content
}

func assertNoSpoolTempFiles(t *testing.T, logsDir string) {
	t.Helper()
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		t.Fatalf("ReadDir(%s) error: %v", logsDir, err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "spool-") {
			t.Fatalf("spool temp file leaked: %s", entry.Name())
		}
	}
}

func runStreamingLogRequest(t *testing.T, logsDir string, payload []byte) []byte {
	t.Helper()
	logger := NewFileRequestLogger(true, logsDir, "", 0)
	writer, errLog := logger.LogStreamingRequest(
		"/v1/messages",
		http.MethodPost,
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"stream":true}`),
		"spool-test",
	)
	if errLog != nil {
		t.Fatalf("LogStreamingRequest error: %v", errLog)
	}
	if errStatus := writer.WriteStatus(http.StatusOK, map[string][]string{"Content-Type": {"text/event-stream"}}); errStatus != nil {
		t.Fatalf("WriteStatus error: %v", errStatus)
	}
	writer.WriteChunkAsync(payload)
	if errClose := writer.Close(); errClose != nil {
		t.Fatalf("Close error: %v", errClose)
	}
	return readOnlyLogFile(t, logsDir)
}

func TestFileRequestLogger_StreamingLogKeepsSmallResponseBody(t *testing.T) {
	logsDir := t.TempDir()
	payload := deterministicPayload(64 * 1024)

	content := runStreamingLogRequest(t, logsDir, payload)
	assertNoSpoolTempFiles(t, logsDir)

	if !bytes.HasSuffix(content, payload) {
		t.Fatalf("log does not end with the streamed payload: log=%d bytes payload=%d bytes", len(content), len(payload))
	}
}

func TestFileRequestLogger_StreamingLogKeepsSpilledResponseBody(t *testing.T) {
	logsDir := t.TempDir()
	// 1.5 MiB of chunked writes crosses defaultSpoolMemoryLimit, exercising the
	// spill path end to end through Close and log assembly.
	payload := deterministicPayload(1536 * 1024)
	const chunkSize = 8192

	logger := NewFileRequestLogger(true, logsDir, "", 0)
	writer, errLog := logger.LogStreamingRequest(
		"/v1/messages",
		http.MethodPost,
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"stream":true}`),
		"spool-test-spill",
	)
	if errLog != nil {
		t.Fatalf("LogStreamingRequest error: %v", errLog)
	}
	if errStatus := writer.WriteStatus(http.StatusOK, map[string][]string{"Content-Type": {"text/event-stream"}}); errStatus != nil {
		t.Fatalf("WriteStatus error: %v", errStatus)
	}
	for offset := 0; offset < len(payload); offset += chunkSize {
		end := offset + chunkSize
		if end > len(payload) {
			end = len(payload)
		}
		writer.WriteChunkAsync(payload[offset:end])
	}
	if errClose := writer.Close(); errClose != nil {
		t.Fatalf("Close error: %v", errClose)
	}

	content := readOnlyLogFile(t, logsDir)
	assertNoSpoolTempFiles(t, logsDir)

	if !bytes.HasSuffix(content, payload) {
		t.Fatalf("spilled log body mismatch: log=%d bytes payload=%d bytes", len(content), len(payload))
	}
}

func TestFileRequestLogger_StreamingLogKeepsSpilledRequestBody(t *testing.T) {
	logsDir := t.TempDir()
	// A request body past the spool limit spills, so the log must read it back
	// through the temporary file path.
	requestBody := deterministicPayload(1536 * 1024)

	logger := NewFileRequestLogger(true, logsDir, "", 0)
	writer, errLog := logger.LogStreamingRequest(
		"/v1/messages",
		http.MethodPost,
		map[string][]string{"Content-Type": {"application/json"}},
		requestBody,
		"spool-test-request-spill",
	)
	if errLog != nil {
		t.Fatalf("LogStreamingRequest error: %v", errLog)
	}
	if errStatus := writer.WriteStatus(http.StatusOK, nil); errStatus != nil {
		t.Fatalf("WriteStatus error: %v", errStatus)
	}
	writer.WriteChunkAsync([]byte("data: tail\n\n"))
	if errClose := writer.Close(); errClose != nil {
		t.Fatalf("Close error: %v", errClose)
	}

	content := readOnlyLogFile(t, logsDir)
	assertNoSpoolTempFiles(t, logsDir)

	if !bytes.Contains(content, requestBody) {
		t.Fatalf("log is missing the spilled request body: log=%d bytes body=%d bytes", len(content), len(requestBody))
	}
	if !bytes.Contains(content, []byte("data: tail")) {
		t.Fatal("log is missing the streamed response body")
	}
}
