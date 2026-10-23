package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	homekv "github.com/router-for-me/CLIProxyAPI/v8/internal/home"
	log "github.com/sirupsen/logrus"
)

// SignatureEntry holds a cached thinking signature with timestamp
type SignatureEntry struct {
	Signature string
	Timestamp time.Time
}

const (
	// SignatureCacheTTL is how long signatures are valid
	SignatureCacheTTL = 3 * time.Hour

	// SignatureTextHashLen is the length of the hash key (16 hex chars = 64-bit key space)
	SignatureTextHashLen = 16

	// MinValidSignatureLen is the minimum length for a signature to be considered valid
	MinValidSignatureLen = 50

	// CacheCleanupInterval controls how often stale entries are purged
	CacheCleanupInterval = 10 * time.Minute

	// SignatureCacheMaxEntriesPerGroup bounds how many signatures one model
	// group keeps. The cache lives for the whole signature TTL, so without a
	// bound a long running server accumulates every signature it ever saw.
	SignatureCacheMaxEntriesPerGroup = 4096

	// SignatureCacheMaxBytesPerGroup bounds the signature bytes one model
	// group keeps, for the same reason.
	SignatureCacheMaxBytesPerGroup = 32 << 20

	// SignatureCacheMaxTotalBytes bounds the signature bytes kept across
	// every model group. The per-group bound does not bound the cache: the
	// group key is the raw model name for unknown model families, so the
	// number of groups is client controlled.
	SignatureCacheMaxTotalBytes = 128 << 20

	// SignatureCacheMaxTotalEntries bounds the signatures kept across every
	// model group.
	SignatureCacheMaxTotalEntries = 32768
)

// The effective global budgets. Tests lower them to exercise the eviction
// without allocating the full budget.
var (
	signatureCacheMaxTotalBytes   = int64(SignatureCacheMaxTotalBytes)
	signatureCacheMaxTotalEntries = int64(SignatureCacheMaxTotalEntries)
)

// signatureCache stores signatures by model group -> textHash -> SignatureEntry
var signatureCache sync.Map

// Global accounting for the signature cache. The per-group counters do not
// bound the cache, because the group key is the raw model name for unknown
// model families, so the number of groups is client controlled.
var (
	signatureGlobalMu     sync.Mutex
	signatureTotalBytes   int64
	signatureTotalEntries int64
)

// addSignatureGlobalBytes adjusts the global byte counter.
func addSignatureGlobalBytes(delta int64) {
	signatureGlobalMu.Lock()
	signatureTotalBytes += delta
	if signatureTotalBytes < 0 {
		signatureTotalBytes = 0
	}
	signatureGlobalMu.Unlock()
}

// addSignatureGlobalEntries adjusts the global entry counter.
func addSignatureGlobalEntries(delta int64) {
	signatureGlobalMu.Lock()
	signatureTotalEntries += delta
	if signatureTotalEntries < 0 {
		signatureTotalEntries = 0
	}
	signatureGlobalMu.Unlock()
}

// signatureGlobalUsage reports the current global byte and entry counters.
func signatureGlobalUsage() (int64, int64) {
	signatureGlobalMu.Lock()
	defer signatureGlobalMu.Unlock()
	return signatureTotalBytes, signatureTotalEntries
}

// cacheCleanupOnce ensures the background cleanup goroutine starts only once
var cacheCleanupOnce sync.Once

type signatureKVClient interface {
	KVGet(ctx context.Context, key string) ([]byte, bool, error)
	KVSet(ctx context.Context, key string, value []byte, opts homekv.KVSetOptions) (bool, error)
	KVDel(ctx context.Context, keys ...string) (int64, error)
	KVExpire(ctx context.Context, key string, ttl time.Duration) (bool, error)
}

var currentSignatureKVClient = func() (signatureKVClient, bool, error) {
	return homekv.CurrentKVClient()
}

// groupCache is the inner map type
type groupCache struct {
	mu      sync.RWMutex
	entries map[string]SignatureEntry
	// bytes tracks the total size of the cached signatures, so the group can
	// be bounded by size as well as by entry count.
	bytes int
	// lastUsed tracks the last access of the group so the global eviction can
	// drop the least recently used group first.
	lastUsed atomic.Int64
}

// touch records that the group was used.
func (sc *groupCache) touch() {
	if sc == nil {
		return
	}
	sc.lastUsed.Store(time.Now().UnixNano())
}

// setEntryLocked stores one signature and keeps the group and global counters
// in sync. The caller must hold sc.mu.
func (sc *groupCache) setEntryLocked(key string, entry SignatureEntry) {
	if previous, exists := sc.entries[key]; exists {
		sc.bytes -= len(previous.Signature)
	} else {
		addSignatureGlobalEntries(1)
	}
	sc.entries[key] = entry
	sc.bytes += len(entry.Signature)
	addSignatureGlobalBytes(int64(len(entry.Signature)))
}

// deleteEntryLocked removes one signature and keeps the group and global
// counters in sync. The caller must hold sc.mu.
func (sc *groupCache) deleteEntryLocked(key string) {
	entry, exists := sc.entries[key]
	if !exists {
		return
	}
	sc.bytes -= len(entry.Signature)
	delete(sc.entries, key)
	addSignatureGlobalBytes(-int64(len(entry.Signature)))
	addSignatureGlobalEntries(-1)
}

// resetLocked drops every entry of the group and resets the counters. The
// caller must hold sc.mu.
func (sc *groupCache) resetLocked() {
	addSignatureGlobalBytes(-int64(sc.bytes))
	addSignatureGlobalEntries(-int64(len(sc.entries)))
	sc.bytes = 0
	sc.entries = make(map[string]SignatureEntry)
}

// hashText creates a stable, Unicode-safe key from text content
func hashText(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])[:SignatureTextHashLen]
}

// getOrCreateGroupCache gets or creates a cache bucket for a model group
func getOrCreateGroupCache(groupKey string) *groupCache {
	// Start background cleanup on first access
	cacheCleanupOnce.Do(startCacheCleanup)

	if val, ok := signatureCache.Load(groupKey); ok {
		sc := val.(*groupCache)
		sc.touch()
		return sc
	}
	sc := &groupCache{entries: make(map[string]SignatureEntry)}
	sc.touch()
	actual, _ := signatureCache.LoadOrStore(groupKey, sc)
	actual.(*groupCache).touch()
	return actual.(*groupCache)
}

// startCacheCleanup launches a background goroutine that periodically
// removes caches where all entries have expired.
func startCacheCleanup() {
	go func() {
		ticker := time.NewTicker(CacheCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			purgeExpiredCaches()
		}
	}()
}

// purgeExpiredCaches removes caches with no valid (non-expired) entries.
func purgeExpiredCaches() {
	now := time.Now()
	signatureCache.Range(func(key, value any) bool {
		sc := value.(*groupCache)
		sc.mu.Lock()
		// Remove expired entries
		for k, entry := range sc.entries {
			if now.Sub(entry.Timestamp) > SignatureCacheTTL {
				sc.deleteEntryLocked(k)
			}
		}
		isEmpty := len(sc.entries) == 0
		sc.mu.Unlock()
		// Remove cache bucket if empty
		if isEmpty {
			signatureCache.CompareAndDelete(key, value)
		}
		return true
	})
	purgeExpiredCodexReasoningReplayCache(now)
	purgeExpiredXAIReasoningReplayCache(now)
	purgeExpiredAntigravityReasoningReplayCache(now)
	purgeExpiredKimiThinkingReplayCache(now)
	purgeExpiredClaudeThinkingReplayCache(now)
	purgeExpiredClaudeWebSearchResultCache(now)
}

// CacheSignature stores a thinking signature for a given model group and text.
// Used for Claude models that require signed thinking blocks in multi-turn conversations.
func CacheSignature(modelName, text, signature string) {
	CacheSignatureBestEffort(context.Background(), modelName, text, signature)
}

// CacheSignatureBestEffort stores a thinking signature for completed response paths.
func CacheSignatureBestEffort(ctx context.Context, modelName, text, signature string) bool {
	if text == "" || signature == "" {
		return false
	}
	if len(signature) < MinValidSignatureLen {
		return false
	}

	if client, homeMode, errClient := currentSignatureKVClient(); homeMode {
		if errClient != nil {
			log.Errorf("home kv best-effort signature set failed prefix=cpa:signature:*: %v", errClient)
			return false
		}
		written, errSet := client.KVSet(ctx, signatureKVKey(modelName, text), []byte(signature), homekv.KVSetOptions{EX: SignatureCacheTTL})
		if errSet != nil {
			log.Errorf("home kv best-effort signature set failed prefix=cpa:signature:*: %v", errSet)
			return false
		}
		return written
	}

	groupKey := GetModelGroup(modelName)
	textHash := hashText(text)
	entry := SignatureEntry{
		Signature: signature,
		Timestamp: time.Now(),
	}

	// The group can be removed from the map while this call waits for the
	// group lock, because an empty group is dropped. Inserting into such an
	// orphaned group would hide the signature from the expiry sweep, so the
	// current group is re-resolved until it matches the map.
	var sc *groupCache
	for range 3 {
		sc = getOrCreateGroupCache(groupKey)
		sc.mu.Lock()
		if current, ok := signatureCache.Load(groupKey); ok && current.(*groupCache) == sc {
			break
		}
		sc.mu.Unlock()
		sc = nil
	}
	if sc == nil {
		sc = getOrCreateGroupCache(groupKey)
		sc.mu.Lock()
	}

	sc.setEntryLocked(textHash, entry)
	enforceSignatureCacheLimitsLocked(sc)
	sc.mu.Unlock()

	// The global budget is enforced outside the group lock so the eviction can
	// lock any group, including this one.
	enforceSignatureGlobalLimits()
	return true
}

// oldestSignatureEntryLocked returns the key of the oldest entry of the group.
// The caller must hold sc.mu.
func oldestSignatureEntryLocked(sc *groupCache) (string, bool) {
	if sc == nil {
		return "", false
	}
	oldestKey := ""
	var oldest time.Time
	for key, entry := range sc.entries {
		if oldestKey == "" || entry.Timestamp.Before(oldest) {
			oldestKey = key
			oldest = entry.Timestamp
		}
	}
	return oldestKey, oldestKey != ""
}

// enforceSignatureCacheLimitsLocked drops the oldest entries until the group
// fits its entry count and byte budget.
//
// The cache keeps every signature for the whole signature TTL, so without a
// bound a long running server accumulates every signature it ever saw. The
// oldest entries are dropped first, because a stale signature is the least
// useful one to keep.
func enforceSignatureCacheLimitsLocked(sc *groupCache) {
	if sc == nil {
		return
	}
	for len(sc.entries) > SignatureCacheMaxEntriesPerGroup || sc.bytes > SignatureCacheMaxBytesPerGroup {
		if len(sc.entries) == 0 {
			sc.resetLocked()
			return
		}
		oldestKey, ok := oldestSignatureEntryLocked(sc)
		if !ok {
			sc.resetLocked()
			return
		}
		sc.deleteEntryLocked(oldestKey)
	}
}

// enforceSignatureGlobalLimits drops entries until the cache fits the global
// byte and entry budget.
//
// The per-group budget cannot bound the cache on its own: the group key is the
// raw model name for unknown model families, so a client can create an
// unbounded number of groups. The least recently used group gives up its
// oldest entry first.
func enforceSignatureGlobalLimits() {
	for {
		bytesUsed, entriesUsed := signatureGlobalUsage()
		if bytesUsed <= signatureCacheMaxTotalBytes && entriesUsed <= signatureCacheMaxTotalEntries {
			return
		}
		if !evictLeastRecentlyUsedGroupEntry() {
			return
		}
	}
}

// evictLeastRecentlyUsedGroupEntry drops the oldest entry of the least
// recently used group and reports whether an entry was dropped.
func evictLeastRecentlyUsedGroupEntry() bool {
	var lruKey string
	var lruGroup *groupCache
	var lruUsed int64
	signatureCache.Range(func(key, value any) bool {
		sc := value.(*groupCache)
		used := sc.lastUsed.Load()
		if lruGroup == nil || used < lruUsed {
			lruKey, lruGroup, lruUsed = key.(string), sc, used
		}
		return true
	})
	if lruGroup == nil {
		return false
	}

	lruGroup.mu.Lock()
	if oldestKey, ok := oldestSignatureEntryLocked(lruGroup); ok {
		lruGroup.deleteEntryLocked(oldestKey)
	}
	isEmpty := len(lruGroup.entries) == 0
	lruGroup.mu.Unlock()

	if isEmpty {
		// Drop the empty group so its slot is not kept alive by the map.
		signatureCache.CompareAndDelete(lruKey, lruGroup)
	}
	return true
}

// GetCachedSignature retrieves a cached signature for a given model group and text.
// Returns empty string if not found or expired.
func GetCachedSignature(modelName, text string) string {
	signature, errSignature := GetCachedSignatureRequired(context.Background(), modelName, text)
	if errSignature != nil {
		return ""
	}
	return signature
}

// GetCachedSignatureRequired retrieves a cached signature for request-time paths.
func GetCachedSignatureRequired(ctx context.Context, modelName, text string) (string, error) {
	groupKey := GetModelGroup(modelName)

	if text == "" {
		if groupKey == "gemini" {
			return "skip_thought_signature_validator", nil
		}
		return "", nil
	}

	if client, homeMode, errClient := currentSignatureKVClient(); homeMode {
		if errClient != nil {
			return "", errClient
		}
		key := signatureKVKey(modelName, text)
		raw, found, errGet := client.KVGet(ctx, key)
		if errGet != nil {
			return "", errGet
		}
		if !found {
			if groupKey == "gemini" {
				return "skip_thought_signature_validator", nil
			}
			return "", nil
		}
		if _, errExpire := client.KVExpire(ctx, key, SignatureCacheTTL); errExpire != nil {
			return "", errExpire
		}
		return string(raw), nil
	}

	val, ok := signatureCache.Load(groupKey)
	if !ok {
		if groupKey == "gemini" {
			return "skip_thought_signature_validator", nil
		}
		return "", nil
	}
	sc := val.(*groupCache)

	textHash := hashText(text)

	now := time.Now()

	sc.mu.Lock()
	entry, exists := sc.entries[textHash]
	if !exists {
		sc.mu.Unlock()
		if groupKey == "gemini" {
			return "skip_thought_signature_validator", nil
		}
		return "", nil
	}
	if now.Sub(entry.Timestamp) > SignatureCacheTTL {
		sc.deleteEntryLocked(textHash)
		sc.mu.Unlock()
		if groupKey == "gemini" {
			return "skip_thought_signature_validator", nil
		}
		return "", nil
	}

	// Refresh TTL on access (sliding expiration).
	entry.Timestamp = now
	sc.entries[textHash] = entry
	sc.touch()
	sc.mu.Unlock()

	return entry.Signature, nil
}

// ClearSignatureCache clears signature cache for a specific model group or all groups.
func ClearSignatureCache(modelName string) {
	if modelName == "" {
		signatureCache.Range(func(key, value any) bool {
			sc := value.(*groupCache)
			sc.mu.Lock()
			sc.resetLocked()
			sc.mu.Unlock()
			signatureCache.CompareAndDelete(key, value)
			return true
		})
		return
	}
	groupKey := GetModelGroup(modelName)
	if value, ok := signatureCache.Load(groupKey); ok {
		sc := value.(*groupCache)
		sc.mu.Lock()
		sc.resetLocked()
		sc.mu.Unlock()
		signatureCache.CompareAndDelete(groupKey, value)
	}
}

// DeleteCachedSignatureRequired removes one exact cached signature.
func DeleteCachedSignatureRequired(ctx context.Context, modelName, text string) error {
	if text == "" {
		return nil
	}
	if client, homeMode, errClient := currentSignatureKVClient(); homeMode {
		if errClient != nil {
			return errClient
		}
		_, errDel := client.KVDel(ctx, signatureKVKey(modelName, text))
		return errDel
	}
	groupKey := GetModelGroup(modelName)
	textHash := hashText(text)
	val, ok := signatureCache.Load(groupKey)
	if !ok {
		return nil
	}
	sc := val.(*groupCache)
	sc.mu.Lock()
	sc.deleteEntryLocked(textHash)
	isEmpty := len(sc.entries) == 0
	sc.mu.Unlock()
	if isEmpty {
		signatureCache.CompareAndDelete(groupKey, val)
	}
	return nil
}

// HasValidSignature checks if a signature is valid (non-empty and long enough)
func HasValidSignature(modelName, signature string) bool {
	return (signature != "" && len(signature) >= MinValidSignatureLen) || (signature == "skip_thought_signature_validator" && GetModelGroup(modelName) == "gemini")
}

func GetModelGroup(modelName string) string {
	if strings.Contains(modelName, "gpt") {
		return "gpt"
	} else if strings.Contains(modelName, "claude") {
		return "claude"
	} else if strings.Contains(modelName, "gemini") {
		return "gemini"
	}
	return modelName
}

func signatureKVKey(modelName, text string) string {
	return fmt.Sprintf("cpa:signature:%s:%s", GetModelGroup(modelName), homekv.HashKeyPart(text))
}

var signatureCacheEnabled atomic.Bool
var signatureBypassStrictMode atomic.Bool

func init() {
	signatureCacheEnabled.Store(true)
	signatureBypassStrictMode.Store(false)
}

// SetSignatureCacheEnabled switches Antigravity signature handling between cache mode and bypass mode.
func SetSignatureCacheEnabled(enabled bool) {
	previous := signatureCacheEnabled.Swap(enabled)
	if previous == enabled {
		return
	}
	if !enabled {
		log.Info("antigravity signature cache DISABLED - bypass mode active, cached signatures will not be used for request translation")
	}
}

// SignatureCacheEnabled returns whether signature cache validation is enabled.
func SignatureCacheEnabled() bool {
	return signatureCacheEnabled.Load()
}

// SetSignatureBypassStrictMode controls whether bypass mode uses strict protobuf-tree validation.
func SetSignatureBypassStrictMode(strict bool) {
	previous := signatureBypassStrictMode.Swap(strict)
	if previous == strict {
		return
	}
	if strict {
		log.Debug("antigravity bypass signature validation: strict mode (protobuf tree)")
	} else {
		log.Debug("antigravity bypass signature validation: basic mode (R/E + 0x12)")
	}
}

// SignatureBypassStrictMode returns whether bypass mode uses strict protobuf-tree validation.
func SignatureBypassStrictMode() bool {
	return signatureBypassStrictMode.Load()
}
