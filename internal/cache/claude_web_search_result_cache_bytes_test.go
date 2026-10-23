package cache

import (
	"bytes"
	"strconv"
	"testing"
	"time"
)

// claudeWebSearchResultCachedBytes reports the tracked aggregate size and the
// size summed from the cached entries.
func claudeWebSearchResultCachedBytes() (int64, int64) {
	claudeWebSearchResultMu.Lock()
	defer claudeWebSearchResultMu.Unlock()
	var sum int64
	for element := claudeWebSearchResultOrder.Front(); element != nil; element = element.Next() {
		entry := element.Value.(*claudeWebSearchResultEntry)
		sum += int64(len(entry.content))
	}
	return claudeWebSearchResultTotalBytes, sum
}

// TestClaudeWebSearchResultCacheRejectsOversizedEntry proves a block larger
// than the per-entry cap is not cached.
func TestClaudeWebSearchResultCacheRejectsOversizedEntry(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	useClaudeWebSearchResultClock(t, &now)

	content := bytes.Repeat([]byte("a"), claudeWebSearchResultMaxEntryBytes+1)
	if CacheClaudeWebSearchResultBestEffort(t.Context(), "s", "srvtoolu_1", content, time.Hour, 16) {
		t.Fatal("oversized entry was cached")
	}
	if _, ok := GetClaudeWebSearchResult("s", "srvtoolu_1"); ok {
		t.Fatal("oversized entry was stored")
	}
	if got := claudeWebSearchResultOrder.Len(); got != 0 {
		t.Fatalf("cached entries = %d, want 0", got)
	}
	tracked, sum := claudeWebSearchResultCachedBytes()
	if tracked != 0 || sum != 0 {
		t.Fatalf("tracked bytes = %d, sum = %d, want 0/0", tracked, sum)
	}
}

// TestClaudeWebSearchResultCacheBoundsAggregateBytes proves the aggregate
// cached content stays inside the byte budget.
func TestClaudeWebSearchResultCacheBoundsAggregateBytes(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	useClaudeWebSearchResultClock(t, &now)

	// Every entry fits the per-entry cap, so only the aggregate cap applies.
	chunk := bytes.Repeat([]byte("b"), claudeWebSearchResultMaxEntryBytes)
	for i := range 16 {
		session := "session-" + strconv.Itoa(i)
		CacheClaudeWebSearchResult(session, "srvtoolu_1", chunk, time.Hour, 1024)
	}

	tracked, sum := claudeWebSearchResultCachedBytes()
	if tracked > claudeWebSearchResultMaxTotalBytes {
		t.Fatalf("tracked bytes = %d, want at most %d", tracked, claudeWebSearchResultMaxTotalBytes)
	}
	if tracked != sum {
		t.Fatalf("tracked bytes = %d, sum of entries = %d", tracked, sum)
	}
}

// TestClaudeWebSearchResultCacheTracksBytesAfterExpiry proves the tracked size
// follows the entries when they expire.
func TestClaudeWebSearchResultCacheTracksBytesAfterExpiry(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	useClaudeWebSearchResultClock(t, &now)

	content := []byte(`[{"url":"a"}]`)
	CacheClaudeWebSearchResult("s", "srvtoolu_1", content, 10*time.Minute, 16)
	CacheClaudeWebSearchResult("s", "srvtoolu_2", content, 10*time.Minute, 16)

	tracked, sum := claudeWebSearchResultCachedBytes()
	if tracked != int64(2*len(content)) || sum != tracked {
		t.Fatalf("tracked bytes = %d, sum = %d, want %d", tracked, sum, 2*len(content))
	}

	now = now.Add(20 * time.Minute)
	purgeExpiredClaudeWebSearchResultCache(now)

	tracked, sum = claudeWebSearchResultCachedBytes()
	if tracked != 0 || sum != 0 {
		t.Fatalf("tracked bytes after expiry = %d, sum = %d, want 0/0", tracked, sum)
	}
}

// TestClaudeWebSearchResultCacheOverwriteKeepsCounterExact proves replacing an
// entry does not double count its bytes.
func TestClaudeWebSearchResultCacheOverwriteKeepsCounterExact(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	useClaudeWebSearchResultClock(t, &now)

	CacheClaudeWebSearchResult("s", "srvtoolu_1", []byte(`[1]`), time.Hour, 16)
	CacheClaudeWebSearchResult("s", "srvtoolu_1", []byte(`[2]`), time.Hour, 16)

	tracked, sum := claudeWebSearchResultCachedBytes()
	if tracked != int64(len(`[2]`)) {
		t.Fatalf("tracked bytes = %d, want %d", tracked, len(`[2]`))
	}
	if tracked != sum {
		t.Fatalf("tracked bytes = %d, sum of entries = %d", tracked, sum)
	}
}
