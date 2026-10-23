package logging

import (
	"bytes"
	"fmt"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
)

// FileStreamingLogWriter implements StreamingLogWriter for file-based streaming logs.
// Response chunks are copied into a body spool that stays in memory up to the
// spool limit and spills to a temporary file beyond it. The final log file is
// assembled when Close is called.
type FileStreamingLogWriter struct {
	// logsDir is the target directory for log files.
	logsDir string

	// logFilename is the target log file name.
	logFilename string

	// url is the request URL (masked upstream in middleware).
	url string

	// method is the HTTP method.
	method string

	// timestamp is captured when the streaming log is initialized.
	timestamp time.Time

	// requestHeaders stores the request headers.
	requestHeaders map[string][]string

	// requestSpool holds the request body for later assembly.
	requestSpool *bodySpool

	// responseSpool accumulates the streamed response body.
	responseSpool *bodySpool

	// writeErr records the first spool write failure.
	writeErr error

	// responseStatus stores the HTTP status code.
	responseStatus int

	// statusWritten indicates whether a non-zero status was recorded.
	statusWritten bool

	// responseHeaders stores the response headers.
	responseHeaders map[string][]string

	// apiRequest stores the upstream API request data.
	apiRequest []byte

	// apiRequestSource stores file-backed upstream API request data.
	apiRequestSource *FileBodySource

	// apiResponse stores the upstream API response data.
	apiResponse []byte

	// apiResponseSource stores file-backed upstream API response data.
	apiResponseSource *FileBodySource

	// apiWebsocketTimeline stores the upstream websocket event timeline.
	apiWebsocketTimeline []byte

	// apiResponseTimestamp captures when the API response was received.
	apiResponseTimestamp time.Time
}

// WriteChunkAsync spools a response chunk for later assembly. The spool copies
// chunk before returning, so the caller may reuse its buffer.
//
// Parameters:
//   - chunk: The response chunk to write
func (w *FileStreamingLogWriter) WriteChunkAsync(chunk []byte) {
	if w.responseSpool == nil || len(chunk) == 0 {
		return
	}
	if _, errWrite := w.responseSpool.Write(chunk); errWrite != nil && w.writeErr == nil {
		w.writeErr = errWrite
	}
}

// WriteStatus buffers the response status and headers for later writing.
//
// Parameters:
//   - status: The response status code
//   - headers: The response headers
//
// Returns:
//   - error: Always returns nil (buffering cannot fail)
func (w *FileStreamingLogWriter) WriteStatus(status int, headers map[string][]string) error {
	if status == 0 {
		return nil
	}

	w.responseStatus = status
	if headers != nil {
		w.responseHeaders = make(map[string][]string, len(headers))
		for key, values := range headers {
			headerValues := make([]string, len(values))
			copy(headerValues, values)
			w.responseHeaders[key] = headerValues
		}
	}
	w.statusWritten = true
	return nil
}

// WriteAPIRequest buffers the upstream API request details for later writing.
//
// Parameters:
//   - apiRequest: The API request data (typically includes URL, headers, body sent upstream)
//
// Returns:
//   - error: Always returns nil (buffering cannot fail)
func (w *FileStreamingLogWriter) WriteAPIRequest(apiRequest []byte) error {
	if len(apiRequest) == 0 {
		return nil
	}
	w.apiRequest = bytes.Clone(apiRequest)
	return nil
}

// WriteAPIRequestSource buffers a file-backed upstream API request for final writing.
func (w *FileStreamingLogWriter) WriteAPIRequestSource(apiRequestSource *FileBodySource) error {
	if apiRequestSource == nil || !apiRequestSource.HasPayload() {
		return nil
	}
	w.apiRequestSource = apiRequestSource
	return nil
}

// WriteAPIResponse buffers the upstream API response details for later writing.
//
// Parameters:
//   - apiResponse: The API response data
//
// Returns:
//   - error: Always returns nil (buffering cannot fail)
func (w *FileStreamingLogWriter) WriteAPIResponse(apiResponse []byte) error {
	if len(apiResponse) == 0 {
		return nil
	}
	w.apiResponse = bytes.Clone(apiResponse)
	return nil
}

// WriteAPIResponseSource buffers a file-backed upstream API response for final writing.
func (w *FileStreamingLogWriter) WriteAPIResponseSource(apiResponseSource *FileBodySource) error {
	if apiResponseSource == nil || !apiResponseSource.HasPayload() {
		return nil
	}
	w.apiResponseSource = apiResponseSource
	return nil
}

// WriteAPIWebsocketTimeline buffers the upstream websocket timeline for later writing.
//
// Parameters:
//   - apiWebsocketTimeline: The upstream websocket event timeline
//
// Returns:
//   - error: Always returns nil (buffering cannot fail)
func (w *FileStreamingLogWriter) WriteAPIWebsocketTimeline(apiWebsocketTimeline []byte) error {
	if len(apiWebsocketTimeline) == 0 {
		return nil
	}
	w.apiWebsocketTimeline = bytes.Clone(apiWebsocketTimeline)
	return nil
}

func (w *FileStreamingLogWriter) SetFirstChunkTimestamp(timestamp time.Time) {
	if !timestamp.IsZero() {
		w.apiResponseTimestamp = timestamp
	}
}

// Close finalizes the log file and cleans up resources.
// It writes all buffered data to the file in the correct order:
// API WEBSOCKET TIMELINE -> API REQUEST -> API RESPONSE -> RESPONSE (status, headers, body chunks)
//
// Returns:
//   - error: An error if closing fails, nil otherwise
func (w *FileStreamingLogWriter) Close() error {
	defer func() {
		w.requestSpool.Cleanup()
		w.responseSpool.Cleanup()
	}()
	if w.writeErr != nil {
		return w.writeErr
	}
	if w.logFilename == "" {
		return nil
	}
	logFile, _, errOpen := createUniqueLogFile(w.logsDir, w.logFilename)
	if errOpen != nil {
		return fmt.Errorf("failed to create log file: %w", errOpen)
	}
	writeErr := w.writeFinalLog(logFile)
	if errClose := logFile.Close(); errClose != nil {
		log.WithError(errClose).Warn("failed to close request log file")
		if writeErr == nil {
			writeErr = errClose
		}
	}
	return writeErr
}

func (w *FileStreamingLogWriter) writeFinalLog(logFile *os.File) error {
	if errWrite := writeRequestInfoWithBody(logFile, w.url, w.method, w.requestHeaders, w.requestSpool.Bytes(), w.requestSpool.Path(), w.timestamp, "http", inferUpstreamTransport(w.apiRequest, w.apiRequestSource, w.apiResponse, w.apiResponseSource, w.apiWebsocketTimeline, nil, nil), true); errWrite != nil {
		return errWrite
	}
	if errWrite := writeAPISection(logFile, "=== API WEBSOCKET TIMELINE ===\n", "=== API WEBSOCKET TIMELINE", w.apiWebsocketTimeline, time.Time{}); errWrite != nil {
		return errWrite
	}
	if errWrite := writePreformattedAPISectionWithSource(logFile, "=== API REQUEST ===\n", "=== API REQUEST", w.apiRequest, w.apiRequestSource, time.Time{}); errWrite != nil {
		return errWrite
	}
	if errWrite := writePreformattedAPISectionWithSource(logFile, "=== API RESPONSE ===\n", "=== API RESPONSE", w.apiResponse, w.apiResponseSource, w.apiResponseTimestamp); errWrite != nil {
		return errWrite
	}

	responseReader, errReader := w.responseSpool.Reader()
	if errReader != nil {
		return errReader
	}
	defer func() {
		if errClose := responseReader.Close(); errClose != nil {
			log.WithError(errClose).Warn("failed to close response body spool")
		}
	}()

	return writeResponseSection(logFile, w.responseStatus, w.statusWritten, w.responseHeaders, responseReader, nil, false)
}

// NoOpStreamingLogWriter is a no-operation implementation for when logging is disabled.
// It implements the StreamingLogWriter interface but performs no actual logging operations.
type NoOpStreamingLogWriter struct{}

// WriteChunkAsync is a no-op implementation that does nothing.
//
// Parameters:
//   - chunk: The response chunk (ignored)
func (w *NoOpStreamingLogWriter) WriteChunkAsync(_ []byte) {}

// WriteStatus is a no-op implementation that does nothing and always returns nil.
//
// Parameters:
//   - status: The response status code (ignored)
//   - headers: The response headers (ignored)
//
// Returns:
//   - error: Always returns nil
func (w *NoOpStreamingLogWriter) WriteStatus(_ int, _ map[string][]string) error {
	return nil
}

// WriteAPIRequest is a no-op implementation that does nothing and always returns nil.
//
// Parameters:
//   - apiRequest: The API request data (ignored)
//
// Returns:
//   - error: Always returns nil
func (w *NoOpStreamingLogWriter) WriteAPIRequest(_ []byte) error {
	return nil
}

// WriteAPIResponse is a no-op implementation that does nothing and always returns nil.
//
// Parameters:
//   - apiResponse: The API response data (ignored)
//
// Returns:
//   - error: Always returns nil
func (w *NoOpStreamingLogWriter) WriteAPIResponse(_ []byte) error {
	return nil
}

// WriteAPIWebsocketTimeline is a no-op implementation that does nothing and always returns nil.
//
// Parameters:
//   - apiWebsocketTimeline: The upstream websocket event timeline (ignored)
//
// Returns:
//   - error: Always returns nil
func (w *NoOpStreamingLogWriter) WriteAPIWebsocketTimeline(_ []byte) error {
	return nil
}

func (w *NoOpStreamingLogWriter) SetFirstChunkTimestamp(_ time.Time) {}

// Close is a no-op implementation that does nothing and always returns nil.
//
// Returns:
//   - error: Always returns nil
func (w *NoOpStreamingLogWriter) Close() error { return nil }
