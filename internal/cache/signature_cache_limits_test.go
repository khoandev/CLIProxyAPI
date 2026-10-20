package cache

import (
	"fmt"
	"testing"
)

// TestSignatureCacheBoundsEntries proves the signature cache keeps a bounded
// number of entries per model group.
//
// The cache holds every signature for the whole signature TTL, so without a
// bound a long running server accumulates every signature it ever saw.
func TestSignatureCacheBoundsEntries(t *testing.T) {
	ClearSignatureCache("")
	t.Cleanup(func() { ClearSignatureCache("") })

	signature := "s" + string(make([]byte, 64))
	for i := range SignatureCacheMaxEntriesPerGroup + 16 {
		if !CacheSignatureBestEffort(t.Context(), "claude-model", fmt.Sprintf("text-%d", i), signature) {
			t.Fatalf("cache write %d failed", i)
		}
	}

	sc := getOrCreateGroupCache(GetModelGroup("claude-model"))
	sc.mu.RLock()
	entries := len(sc.entries)
	bytes := sc.bytes
	sc.mu.RUnlock()

	if entries > SignatureCacheMaxEntriesPerGroup {
		t.Fatalf("cache entries = %d, want at most %d", entries, SignatureCacheMaxEntriesPerGroup)
	}
	if bytes > SignatureCacheMaxBytesPerGroup {
		t.Fatalf("cache bytes = %d, want at most %d", bytes, SignatureCacheMaxBytesPerGroup)
	}
}

// TestSignatureCacheTracksBytes proves the byte counter follows the cached
// content, so it neither drifts up nor down.
func TestSignatureCacheTracksBytes(t *testing.T) {
	ClearSignatureCache("")
	t.Cleanup(func() { ClearSignatureCache("") })

	signature := "s" + string(make([]byte, 64))
	if !CacheSignatureBestEffort(t.Context(), "claude-model", "text-a", signature) {
		t.Fatal("cache write failed")
	}
	sc := getOrCreateGroupCache(GetModelGroup("claude-model"))
	sc.mu.RLock()
	bytes := sc.bytes
	sc.mu.RUnlock()
	if bytes != len(signature) {
		t.Fatalf("cache bytes = %d, want %d", bytes, len(signature))
	}

	// Overwriting the same key must not double count.
	if !CacheSignatureBestEffort(t.Context(), "claude-model", "text-a", signature) {
		t.Fatal("cache overwrite failed")
	}
	sc.mu.RLock()
	bytes = sc.bytes
	sc.mu.RUnlock()
	if bytes != len(signature) {
		t.Fatalf("cache bytes after overwrite = %d, want %d", bytes, len(signature))
	}

	// Dropping the entry must bring the counter back to zero.
	if err := DeleteCachedSignatureRequired(t.Context(), "claude-model", "text-a"); err != nil {
		t.Fatalf("delete signature: %v", err)
	}
	sc = getOrCreateGroupCache(GetModelGroup("claude-model"))
	sc.mu.RLock()
	bytes = sc.bytes
	sc.mu.RUnlock()
	if bytes != 0 {
		t.Fatalf("cache bytes after delete = %d, want 0", bytes)
	}
}
