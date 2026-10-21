package cache

import (
	"fmt"
	"testing"
	"time"
)

// withSignatureGlobalBudget lowers the global signature cache budget for one
// test and restores it afterwards.
func withSignatureGlobalBudget(t *testing.T, maxBytes, maxEntries int64) {
	t.Helper()
	previousBytes := signatureCacheMaxTotalBytes
	previousEntries := signatureCacheMaxTotalEntries
	signatureCacheMaxTotalBytes = maxBytes
	signatureCacheMaxTotalEntries = maxEntries
	t.Cleanup(func() {
		signatureCacheMaxTotalBytes = previousBytes
		signatureCacheMaxTotalEntries = previousEntries
	})
}

// signatureGroupTotals sums the per-group counters of every cached group.
func signatureGroupTotals() (int64, int64) {
	var bytes int64
	var entries int64
	signatureCache.Range(func(_, value any) bool {
		sc := value.(*groupCache)
		sc.mu.RLock()
		bytes += int64(sc.bytes)
		entries += int64(len(sc.entries))
		sc.mu.RUnlock()
		return true
	})
	return bytes, entries
}

// TestSignatureCacheBoundsGlobalBytes proves the aggregate signature bytes
// stay inside the global budget even when many model groups are used.
//
// The group key is the raw model name for unknown model families, so the
// number of groups is client controlled and the per-group budget cannot bound
// the cache on its own.
func TestSignatureCacheBoundsGlobalBytes(t *testing.T) {
	ClearSignatureCache("")
	t.Cleanup(func() { ClearSignatureCache("") })
	withSignatureGlobalBudget(t, 8<<10, 1<<20)

	signature := "s" + string(make([]byte, 1024))
	for i := range 64 {
		model := fmt.Sprintf("client-model-%d", i)
		if !CacheSignatureBestEffort(t.Context(), model, fmt.Sprintf("text-%d", i), signature) {
			t.Fatalf("cache write %d failed", i)
		}
	}

	bytesUsed, _ := signatureGlobalUsage()
	if bytesUsed > signatureCacheMaxTotalBytes {
		t.Fatalf("global signature bytes = %d, want at most %d", bytesUsed, signatureCacheMaxTotalBytes)
	}
	if bytesUsed <= 0 {
		t.Fatalf("global signature bytes = %d, want a positive counter", bytesUsed)
	}
}

// TestSignatureCacheBoundsGlobalEntries proves the aggregate signature count
// stays inside the global entry budget.
func TestSignatureCacheBoundsGlobalEntries(t *testing.T) {
	ClearSignatureCache("")
	t.Cleanup(func() { ClearSignatureCache("") })
	withSignatureGlobalBudget(t, 1<<30, 16)

	signature := "s" + string(make([]byte, 64))
	for i := range 128 {
		model := fmt.Sprintf("client-model-%d", i)
		if !CacheSignatureBestEffort(t.Context(), model, fmt.Sprintf("text-%d", i), signature) {
			t.Fatalf("cache write %d failed", i)
		}
	}

	_, entriesUsed := signatureGlobalUsage()
	if entriesUsed > signatureCacheMaxTotalEntries {
		t.Fatalf("global signature entries = %d, want at most %d", entriesUsed, signatureCacheMaxTotalEntries)
	}
}

// TestSignatureCacheEvictsLeastRecentlyUsedGroup proves the global eviction
// drops the entries of the least recently used group first.
func TestSignatureCacheEvictsLeastRecentlyUsedGroup(t *testing.T) {
	ClearSignatureCache("")
	t.Cleanup(func() { ClearSignatureCache("") })
	withSignatureGlobalBudget(t, 1<<30, 3)

	signature := "s" + string(make([]byte, 64))
	for _, model := range []string{"model-a", "model-b", "model-c"} {
		if !CacheSignatureBestEffort(t.Context(), model, "text", signature) {
			t.Fatalf("cache write for %s failed", model)
		}
	}

	// Pin the recency of every group so the eviction order does not depend on
	// the wall clock resolution.
	recency := map[string]int64{"model-a": 300, "model-b": 100, "model-c": 200}
	for model, used := range recency {
		sc := getOrCreateGroupCache(GetModelGroup(model))
		sc.lastUsed.Store(used)
	}

	if !CacheSignatureBestEffort(t.Context(), "model-d", "text", signature) {
		t.Fatal("cache write for model-d failed")
	}

	if got := GetCachedSignature("model-b", "text"); got != "" {
		t.Fatalf("least recently used group kept its signature: %q", got)
	}
	for _, model := range []string{"model-a", "model-c"} {
		if got := GetCachedSignature(model, "text"); got != signature {
			t.Fatalf("group %s lost its signature: %q", model, got)
		}
	}
}

// TestSignatureCacheGlobalCountersMatchGroups proves the global counters
// follow the per-group counters after inserts, deletes and expiry.
func TestSignatureCacheGlobalCountersMatchGroups(t *testing.T) {
	ClearSignatureCache("")
	t.Cleanup(func() { ClearSignatureCache("") })

	signature := "s" + string(make([]byte, 128))
	for i := range 8 {
		model := fmt.Sprintf("counter-model-%d", i)
		if !CacheSignatureBestEffort(t.Context(), model, fmt.Sprintf("text-%d", i), signature) {
			t.Fatalf("cache write %d failed", i)
		}
	}

	assertCountersMatch := func(stage string) {
		t.Helper()
		globalBytes, globalEntries := signatureGlobalUsage()
		groupBytes, groupEntries := signatureGroupTotals()
		if globalBytes != groupBytes {
			t.Fatalf("%s: global bytes = %d, group bytes = %d", stage, globalBytes, groupBytes)
		}
		if globalEntries != groupEntries {
			t.Fatalf("%s: global entries = %d, group entries = %d", stage, globalEntries, groupEntries)
		}
		if globalBytes < 0 || globalEntries < 0 {
			t.Fatalf("%s: negative counters bytes=%d entries=%d", stage, globalBytes, globalEntries)
		}
	}
	assertCountersMatch("after insert")

	if err := DeleteCachedSignatureRequired(t.Context(), "counter-model-0", "text-0"); err != nil {
		t.Fatalf("delete signature: %v", err)
	}
	assertCountersMatch("after delete")

	ClearSignatureCache("counter-model-1")
	assertCountersMatch("after clear")

	ClearSignatureCache("")
	assertCountersMatch("after clear all")

	globalBytes, globalEntries := signatureGlobalUsage()
	if globalBytes != 0 || globalEntries != 0 {
		t.Fatalf("counters after clearing everything = %d/%d, want 0/0", globalBytes, globalEntries)
	}
}

// TestSignatureCacheGlobalCountersSurviveExpiry proves the counters follow
// the expiry sweep.
func TestSignatureCacheGlobalCountersSurviveExpiry(t *testing.T) {
	ClearSignatureCache("")
	t.Cleanup(func() { ClearSignatureCache("") })

	signature := "s" + string(make([]byte, 64))
	if !CacheSignatureBestEffort(t.Context(), "claude-model", "text", signature) {
		t.Fatal("cache write failed")
	}

	sc := getOrCreateGroupCache(GetModelGroup("claude-model"))
	sc.mu.Lock()
	entry := sc.entries[hashText("text")]
	entry.Timestamp = time.Now().Add(-2 * SignatureCacheTTL)
	sc.entries[hashText("text")] = entry
	sc.mu.Unlock()

	purgeExpiredCaches()

	globalBytes, globalEntries := signatureGlobalUsage()
	if globalBytes != 0 || globalEntries != 0 {
		t.Fatalf("counters after expiry = %d/%d, want 0/0", globalBytes, globalEntries)
	}
}
