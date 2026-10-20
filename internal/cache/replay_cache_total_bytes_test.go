package cache

import (
	"context"
	"fmt"
	"testing"
)

// codexReasoningReplayActualBytes reports the serialized size of the cached
// Codex entries, computed from the entries themselves.
func codexReasoningReplayActualBytes() int {
	total := 0
	for _, entry := range codexReasoningReplayEntries {
		total += codexReasoningReplayEntryBytes(entry.Items)
	}
	return total
}

// xaiReasoningReplayActualBytes reports the serialized size of the cached xAI
// entries, computed from the entries themselves.
func xaiReasoningReplayActualBytes() int {
	total := 0
	for _, entry := range xaiReasoningReplayEntries {
		total += xaiReasoningReplayEntryBytes(entry.Items)
	}
	return total
}

// TestCodexReasoningReplayTotalBytesMatchesEntries proves the aggregate byte
// counter tracks the cached content. A counter that drifts upward would evict
// the whole cache even though it is far below the budget.
func TestCodexReasoningReplayTotalBytesMatchesEntries(t *testing.T) {
	ClearCodexReasoningReplayCache()
	t.Cleanup(ClearCodexReasoningReplayCache)

	item := validCodexReasoningReplayItemForTest(9)
	for i := range 64 {
		if !CacheCodexReasoningReplayItem("gpt-5.4", fmt.Sprintf("session-%d", i), item) {
			t.Fatalf("cache insert %d failed", i)
		}
	}
	// Reading an expired entry removes it, so the counter must follow.
	if _, _, errGet := GetCodexReasoningReplayItemsRequired(context.Background(), "gpt-5.4", "session-0"); errGet != nil {
		t.Fatalf("read entry: %v", errGet)
	}
	DeleteCodexReasoningReplayItem("gpt-5.4", "session-1")

	codexReasoningReplayMu.Lock()
	tracked := codexReasoningReplayTotalBytes
	actual := codexReasoningReplayActualBytes()
	codexReasoningReplayMu.Unlock()

	if tracked != actual {
		t.Fatalf("tracked bytes = %d, actual bytes = %d", tracked, actual)
	}
}

// TestXAIReasoningReplayTotalBytesMatchesEntries proves the aggregate byte
// counter tracks the cached content.
func TestXAIReasoningReplayTotalBytesMatchesEntries(t *testing.T) {
	ClearXAIReasoningReplayCache()
	t.Cleanup(ClearXAIReasoningReplayCache)

	encryptedContent := validGrokEncryptedContentForReplayCacheTest()
	item := []byte(`{"type":"reasoning","summary":[{"type":"summary_text","text":"visible"}],"content":null,"encrypted_content":"` + encryptedContent + `"}`)
	for i := range 64 {
		if StoreXAIReasoningReplayItems(context.Background(), "grok-4.3", fmt.Sprintf("session-%d", i), [][]byte{item}) != XAIReasoningReplayStored {
			t.Fatalf("cache insert %d failed", i)
		}
	}
	DeleteXAIReasoningReplayItem("grok-4.3", "session-1")

	xaiReasoningReplayMu.Lock()
	tracked := xaiReasoningReplayTotalBytes
	actual := xaiReasoningReplayActualBytes()
	xaiReasoningReplayMu.Unlock()

	if tracked != actual {
		t.Fatalf("tracked bytes = %d, actual bytes = %d", tracked, actual)
	}
}

// TestAntigravityReasoningReplayTotalBytesMatchesEntries proves the aggregate
// byte counter tracks the cached content.
func TestAntigravityReasoningReplayTotalBytesMatchesEntries(t *testing.T) {
	ClearAntigravityReasoningReplayCache()
	t.Cleanup(ClearAntigravityReasoningReplayCache)

	encryptedContent := validGrokEncryptedContentForReplayCacheTest()
	item := antigravityReplayTestItem(encryptedContent)
	for i := range 64 {
		if !CacheAntigravityReasoningReplayItems("gemini-3.6-flash-high", fmt.Sprintf("session-%d", i), [][]byte{item}) {
			t.Fatalf("cache insert %d failed", i)
		}
	}
	if errDelete := DeleteAntigravityReasoningReplayItemRequired(context.Background(), "gemini-3.6-flash-high", "session-1"); errDelete != nil {
		t.Fatalf("delete entry: %v", errDelete)
	}

	antigravityReasoningReplayMu.Lock()
	tracked := antigravityReasoningReplayTotalBytes
	actual := 0
	for _, entry := range antigravityReasoningReplayEntries {
		actual += antigravityReasoningReplayEntryBytes(entry.Items)
	}
	antigravityReasoningReplayMu.Unlock()

	if tracked != actual {
		t.Fatalf("tracked bytes = %d, actual bytes = %d", tracked, actual)
	}
}
