package logging

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
)

const (
	// fileBodySourcePartMemoryLimit is the payload one part keeps in memory before spilling.
	fileBodySourcePartMemoryLimit = 256 << 10
	// fileBodySourceMemoryLimit is the total in-memory payload one source keeps across all parts.
	fileBodySourceMemoryLimit = 1 << 20
)

// FileBodySource stores large log sections as ordered parts that stay in memory
// until they outgrow the source budget, then spill to temp files under baseDir.
type FileBodySource struct {
	mu         sync.Mutex
	baseDir    string
	prefix     string
	dir        string
	dirCreated bool
	parts      []*fileBodySourcePart
	cleaned    bool
	memUsed    int
	partLimit  int
	totalLimit int
}

// fileBodySourcePart is one ordered section chunk, buffered in memory and spilled to a
// temp file once it outgrows the source's part limit.
type fileBodySourcePart struct {
	spool *bodySpool
}

// NewFileBodySourceInDir creates a memory-first source that spills under baseDir.
func NewFileBodySourceInDir(baseDir string, prefix string) (*FileBodySource, error) {
	return newFileBodySourceInDir(baseDir, prefix, fileBodySourcePartMemoryLimit, fileBodySourceMemoryLimit)
}

// newFileBodySourceInDir creates a source with explicit memory budgets. partLimit
// bounds one part, totalLimit bounds the whole source before the oldest parts spill.
func newFileBodySourceInDir(baseDir string, prefix string, partLimit int, totalLimit int) (*FileBodySource, error) {
	prefix = sanitizeTempPrefix(prefix)
	baseDir = strings.TrimSpace(baseDir)
	if baseDir == "" {
		return nil, fmt.Errorf("base directory is required")
	}
	if errMkdir := os.MkdirAll(baseDir, 0755); errMkdir != nil {
		return nil, errMkdir
	}
	return &FileBodySource{
		baseDir:    baseDir,
		prefix:     prefix,
		partLimit:  partLimit,
		totalLimit: totalLimit,
	}, nil
}

func sanitizeTempPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return "log"
	}
	var builder strings.Builder
	for _, r := range prefix {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-' || r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
	}
	out := strings.Trim(builder.String(), "-_")
	if out == "" {
		return "log"
	}
	return out
}

// ensureDirLocked creates the temp parts directory on first spill.
func (s *FileBodySource) ensureDirLocked() (string, error) {
	if s.dirCreated {
		return s.dir, nil
	}
	dir, errCreate := os.MkdirTemp(s.baseDir, "request-log-parts-"+s.prefix+"-*")
	if errCreate != nil {
		return "", errCreate
	}
	s.dir = dir
	s.dirCreated = true
	return dir, nil
}

// newPartLocked appends one ordered part. The part resolves its spill directory
// lazily, so a source that stays under budget never touches the filesystem.
func (s *FileBodySource) newPartLocked(prefix string) *fileBodySourcePart {
	part := &fileBodySourcePart{spool: newBodySpool("", s.partLimit).withDirFunc(s.ensureDirLocked).withPrefix(prefix)}
	s.parts = append(s.parts, part)
	return part
}

// enforceTotalLimitLocked spills the oldest in-memory parts until the source fits
// its total memory budget. A spill failure only stops spilling; appends keep
// succeeding from the remaining memory.
func (s *FileBodySource) enforceTotalLimitLocked() {
	for s.memUsed > s.totalLimit {
		spilled := false
		for _, part := range s.parts {
			data := part.spool.Bytes()
			if data == nil {
				continue
			}
			if errSpill := part.spool.spill(); errSpill != nil {
				log.WithError(errSpill).Warn("failed to spill request log part")
				return
			}
			s.memUsed -= len(data)
			spilled = true
			break
		}
		if !spilled {
			return
		}
	}
}

// CreatePart creates one ordered detail log part.
func (s *FileBodySource) CreatePart(prefix string) (io.WriteCloser, error) {
	if s == nil {
		return nil, fmt.Errorf("file body source is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cleaned {
		return nil, fmt.Errorf("file body source has been cleaned")
	}
	part := s.newPartLocked(prefix)
	return &fileBodySourcePartWriter{source: s, part: part}, nil
}

// fileBodySourcePartWriter streams one part through its in-memory spool.
type fileBodySourcePartWriter struct {
	source *FileBodySource
	part   *fileBodySourcePart
	closed bool
}

func (w *fileBodySourcePartWriter) Write(data []byte) (int, error) {
	if w == nil || w.part == nil {
		return 0, fmt.Errorf("file body source part is nil")
	}
	if w.closed {
		return 0, fmt.Errorf("file body source part is closed")
	}
	n, errWrite := w.part.spool.Write(data)
	if n > 0 && w.source != nil {
		w.source.mu.Lock()
		w.source.memUsed += n
		w.source.enforceTotalLimitLocked()
		w.source.mu.Unlock()
	}
	return n, errWrite
}

func (w *fileBodySourcePartWriter) Close() error {
	if w == nil {
		return nil
	}
	w.closed = true
	return nil
}

// AppendPart appends one complete ordered part to the source.
func (s *FileBodySource) AppendPart(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil
	}
	if s == nil {
		return fmt.Errorf("file body source is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cleaned {
		return fmt.Errorf("file body source has been cleaned")
	}
	part := s.newPartLocked("part")
	n, errWrite := part.spool.Write(data)
	s.memUsed += n
	if errWrite != nil {
		return errWrite
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		_, errWrite = part.spool.WriteString("\n")
		if errWrite != nil {
			return errWrite
		}
		s.memUsed++
	}
	s.enforceTotalLimitLocked()
	return nil
}

// AppendBytes appends raw bytes to a single ordered part.
func (s *FileBodySource) AppendBytes(data []byte) error {
	if s == nil {
		return fmt.Errorf("file body source is nil")
	}
	if len(data) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cleaned {
		return fmt.Errorf("file body source has been cleaned")
	}
	var part *fileBodySourcePart
	if len(s.parts) == 0 {
		part = s.newPartLocked("part")
	} else {
		part = s.parts[len(s.parts)-1]
	}
	n, errWrite := part.spool.Write(data)
	s.memUsed += n
	if errWrite != nil {
		return errWrite
	}
	s.enforceTotalLimitLocked()
	return nil
}

// AppendString appends text to a single ordered part without converting it to bytes.
func (s *FileBodySource) AppendString(text string) error {
	if s == nil {
		return fmt.Errorf("file body source is nil")
	}
	if len(text) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cleaned {
		return fmt.Errorf("file body source has been cleaned")
	}
	var part *fileBodySourcePart
	if len(s.parts) == 0 {
		part = s.newPartLocked("part")
	} else {
		part = s.parts[len(s.parts)-1]
	}
	n, errWrite := part.spool.WriteString(text)
	s.memUsed += n
	if errWrite != nil {
		return errWrite
	}
	s.enforceTotalLimitLocked()
	return nil
}

// HasPayload reports whether any detail parts were recorded.
func (s *FileBodySource) HasPayload() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.parts) > 0 && !s.cleaned
}

// Paths returns the spill paths of the parts that outgrew memory, in order.
func (s *FileBodySource) Paths() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.parts))
	for _, part := range s.parts {
		if path := part.spool.Path(); path != "" {
			out = append(out, path)
		}
	}
	return out
}

// WriteTo merges all ordered parts into w.
func (s *FileBodySource) WriteTo(w io.Writer) (int64, error) {
	if s == nil || w == nil {
		return 0, nil
	}
	parts := s.snapshotParts()
	var totalWritten int64
	wrote := false
	for _, part := range parts {
		payload := part.spool.Bytes()
		path := ""
		if len(payload) == 0 {
			path = part.spool.Path()
			if path == "" {
				continue
			}
		}
		if wrote {
			n, errWrite := io.WriteString(w, "\n")
			totalWritten += int64(n)
			if errWrite != nil {
				return totalWritten, errWrite
			}
		}
		if len(payload) > 0 {
			n, errWrite := w.Write(payload)
			totalWritten += int64(n)
			if errWrite != nil {
				return totalWritten, errWrite
			}
		} else {
			n, errCopy := s.copyPartFile(w, path)
			totalWritten += n
			if errCopy != nil {
				return totalWritten, errCopy
			}
		}
		wrote = true
	}
	return totalWritten, nil
}

// copyPartFile copies one spilled part into w, tolerating an already removed file.
func (s *FileBodySource) copyPartFile(w io.Writer, path string) (int64, error) {
	file, errOpen := os.Open(path)
	if errOpen != nil {
		if os.IsNotExist(errOpen) {
			return 0, nil
		}
		return 0, errOpen
	}
	n, errCopy := io.Copy(w, file)
	if errClose := file.Close(); errClose != nil {
		log.WithError(errClose).Warn("failed to close log part file")
		if errCopy == nil {
			errCopy = errClose
		}
	}
	return n, errCopy
}

// snapshotParts returns the current parts so writes happen outside the source lock.
func (s *FileBodySource) snapshotParts() []*fileBodySourcePart {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.parts
}

// Bytes merges all ordered parts into memory.
func (s *FileBodySource) Bytes() ([]byte, error) {
	var buf bytes.Buffer
	if _, errWrite := s.WriteTo(&buf); errWrite != nil {
		return nil, errWrite
	}
	return buf.Bytes(), nil
}

// Cleanup removes all temp detail parts and their directory.
func (s *FileBodySource) Cleanup() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if s.cleaned {
		s.mu.Unlock()
		return nil
	}
	parts := s.parts
	dir := s.dir
	dirCreated := s.dirCreated
	s.parts = nil
	s.cleaned = true
	s.memUsed = 0
	s.mu.Unlock()

	var firstErr error
	for _, part := range parts {
		if part == nil || part.spool == nil {
			continue
		}
		if errRelease := part.spool.Release(); errRelease != nil && firstErr == nil {
			firstErr = errRelease
		}
	}
	if dirCreated && dir != "" {
		if errRemove := os.RemoveAll(dir); errRemove != nil && firstErr == nil {
			firstErr = errRemove
		}
	}
	return firstErr
}

func cleanupFileBodySources(sources ...*FileBodySource) {
	for _, source := range sources {
		if source == nil {
			continue
		}
		if errCleanup := source.Cleanup(); errCleanup != nil {
			log.WithError(errCleanup).Warn("failed to clean up log part files")
		}
	}
}
