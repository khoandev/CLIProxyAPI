package helps

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/cache"
)

// TestSessionIDCacheIsBounded verifies that the session ID cache never holds
// more entries than its limit and that expired entries are purged.
func TestSessionIDCacheIsBounded(t *testing.T) {
	resetSessionIDCache()

	for index := range sessionIDCacheMaxEntries + 200 {
		if _, err := CachedSessionIDRequired(context.Background(), fmt.Sprintf("api-key-%d", index)); err != nil {
			t.Fatalf("CachedSessionIDRequired() error = %v", err)
		}
	}

	sessionIDCacheMu.RLock()
	count := len(sessionIDCache)
	sessionIDCacheMu.RUnlock()
	if count > sessionIDCacheMaxEntries {
		t.Fatalf("session ID cache holds %d entries, want at most %d", count, sessionIDCacheMaxEntries)
	}
	if count == 0 {
		t.Fatal("session ID cache is empty")
	}
}

// TestSessionIDCachePurgesExpiredEntries verifies that the expired entries of
// the session ID cache are removed.
func TestSessionIDCachePurgesExpiredEntries(t *testing.T) {
	resetSessionIDCache()

	now := time.Now()
	sessionIDCacheMu.Lock()
	sessionIDCache["expired-key"] = sessionIDCacheEntry{value: "value", expire: now.Add(-time.Minute)}
	sessionIDCache["live-key"] = sessionIDCacheEntry{value: "value", expire: now.Add(time.Hour)}
	sessionIDCacheMu.Unlock()

	purgeExpiredSessionIDs()

	sessionIDCacheMu.RLock()
	_, expiredPresent := sessionIDCache["expired-key"]
	_, livePresent := sessionIDCache["live-key"]
	sessionIDCacheMu.RUnlock()

	if expiredPresent {
		t.Fatal("the expired session ID entry must be purged")
	}
	if !livePresent {
		t.Fatal("the live session ID entry must be kept")
	}
}

// TestUserIDCacheIsBounded verifies that the user ID cache never holds more
// entries than its limit.
func TestUserIDCacheIsBounded(t *testing.T) {
	resetUserIDCache()

	for index := range userIDCacheMaxEntries + 200 {
		if _, err := CachedUserIDRequired(context.Background(), fmt.Sprintf("api-key-%d", index)); err != nil {
			t.Fatalf("CachedUserIDRequired() error = %v", err)
		}
	}

	userIDCacheMu.RLock()
	count := len(userIDCache)
	userIDCacheMu.RUnlock()
	if count > userIDCacheMaxEntries {
		t.Fatalf("user ID cache holds %d entries, want at most %d", count, userIDCacheMaxEntries)
	}
	if count == 0 {
		t.Fatal("user ID cache is empty")
	}
}

// TestUserIDCachePurgesExpiredEntries verifies that the expired entries of the
// user ID cache are removed.
func TestUserIDCachePurgesExpiredEntries(t *testing.T) {
	resetUserIDCache()

	now := time.Now()
	userIDCacheMu.Lock()
	userIDCache["expired-key"] = userIDCacheEntry{value: "value", expire: now.Add(-time.Minute)}
	userIDCache["live-key"] = userIDCacheEntry{value: "value", expire: now.Add(time.Hour)}
	userIDCacheMu.Unlock()

	purgeExpiredUserIDs()

	userIDCacheMu.RLock()
	_, expiredPresent := userIDCache["expired-key"]
	_, livePresent := userIDCache["live-key"]
	userIDCacheMu.RUnlock()

	if expiredPresent {
		t.Fatal("the expired user ID entry must be purged")
	}
	if !livePresent {
		t.Fatal("the live user ID entry must be kept")
	}
}

// TestClaudeDeviceProfileCacheIsBounded verifies that the device profile cache
// never holds more entries than its limit.
func TestClaudeDeviceProfileCacheIsBounded(t *testing.T) {
	ResetClaudeDeviceProfileCache()

	now := time.Now()
	claudeDeviceProfileCacheMu.Lock()
	for index := range claudeDeviceProfileCacheMaxEntries + 100 {
		key := fmt.Sprintf("profile-key-%d", index)
		claudeDeviceProfileCache[key] = claudeDeviceProfileCacheEntry{
			profile: ClaudeDeviceProfile{UserAgent: "claude-cli/1.0.0"},
			expire:  now.Add(claudeDeviceProfileTTL),
		}
	}
	cache.TrimExpiredEntries(claudeDeviceProfileCache, claudeDeviceProfileCacheMaxEntries, func(entry claudeDeviceProfileCacheEntry) time.Time {
		return entry.expire
	}, now)
	count := len(claudeDeviceProfileCache)
	claudeDeviceProfileCacheMu.Unlock()

	if count > claudeDeviceProfileCacheMaxEntries {
		t.Fatalf("device profile cache holds %d entries, want at most %d", count, claudeDeviceProfileCacheMaxEntries)
	}
	if count == 0 {
		t.Fatal("device profile cache is empty")
	}
}

// TestClaudeDeviceProfileCachePurgesExpiredEntries verifies that the expired
// entries of the device profile cache are removed.
func TestClaudeDeviceProfileCachePurgesExpiredEntries(t *testing.T) {
	ResetClaudeDeviceProfileCache()

	now := time.Now()
	claudeDeviceProfileCacheMu.Lock()
	claudeDeviceProfileCache["expired-key"] = claudeDeviceProfileCacheEntry{
		profile: ClaudeDeviceProfile{UserAgent: "claude-cli/1.0.0"},
		expire:  now.Add(-time.Minute),
	}
	claudeDeviceProfileCache["live-key"] = claudeDeviceProfileCacheEntry{
		profile: ClaudeDeviceProfile{UserAgent: "claude-cli/1.0.0"},
		expire:  now.Add(claudeDeviceProfileTTL),
	}
	claudeDeviceProfileCacheMu.Unlock()

	purgeExpiredClaudeDeviceProfiles()

	claudeDeviceProfileCacheMu.RLock()
	_, expiredPresent := claudeDeviceProfileCache["expired-key"]
	_, livePresent := claudeDeviceProfileCache["live-key"]
	claudeDeviceProfileCacheMu.RUnlock()

	if expiredPresent {
		t.Fatal("the expired device profile entry must be purged")
	}
	if !livePresent {
		t.Fatal("the live device profile entry must be kept")
	}
}
