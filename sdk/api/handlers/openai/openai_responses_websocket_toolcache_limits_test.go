package openai

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// TestWebsocketToolOutputCacheExpiresIdleSessions proves an idle session is
// dropped instead of living for the lifetime of the process.
//
// The default caches are built with a zero TTL, which used to disable the
// cleanup entirely, so every client named session stayed cached forever.
func TestWebsocketToolOutputCacheExpiresIdleSessions(t *testing.T) {
	cache := newWebsocketToolOutputCache(time.Minute, 10)
	cache.record("session-a", "call-1", json.RawMessage(`{"a":1}`))

	cache.mu.Lock()
	session := cache.sessions["session-a"]
	if session == nil {
		cache.mu.Unlock()
		t.Fatal("session was not recorded")
	}
	session.lastSeen = time.Now().Add(-2 * time.Minute)
	cache.mu.Unlock()

	cache.record("session-b", "call-1", json.RawMessage(`{"b":1}`))

	cache.mu.Lock()
	_, stale := cache.sessions["session-a"]
	cache.mu.Unlock()
	if stale {
		t.Fatal("idle session survived the TTL")
	}
}

// TestWebsocketToolOutputCacheDefaultTTLIsApplied proves the default caches
// expire their sessions.
func TestWebsocketToolOutputCacheDefaultTTLIsApplied(t *testing.T) {
	cache := newWebsocketToolOutputCache(0, 10)
	if cache.ttl != websocketToolOutputCacheTTL {
		t.Fatalf("cache ttl = %v, want %v", cache.ttl, websocketToolOutputCacheTTL)
	}
}

// TestWebsocketToolOutputCacheBoundsSessionBytes proves a session drops its
// oldest entries when it exceeds the per-session byte cap.
func TestWebsocketToolOutputCacheBoundsSessionBytes(t *testing.T) {
	cache := newWebsocketToolOutputCache(time.Minute, 1024)

	payload := json.RawMessage(make([]byte, 1<<20))
	for i := range 6 {
		cache.record("session-a", fmt.Sprintf("call-%d", i), payload)
	}

	cache.mu.Lock()
	session := cache.sessions["session-a"]
	entries := len(session.outputs)
	bytes := session.bytes
	cache.mu.Unlock()

	if bytes > websocketToolOutputCacheMaxBytesPerSession {
		t.Fatalf("session bytes = %d, want at most %d", bytes, websocketToolOutputCacheMaxBytesPerSession)
	}
	if entries == 0 {
		t.Fatal("session lost every entry")
	}
	if _, ok := cache.get("session-a", "call-0"); ok {
		t.Fatal("oldest entry survived the byte cap")
	}
	if _, ok := cache.get("session-a", "call-5"); !ok {
		t.Fatal("newest entry was evicted")
	}
}

// TestWebsocketToolOutputCacheBoundsSessions proves the cache never tracks
// more sessions than the cap, even when every session uses a distinct client
// supplied key.
func TestWebsocketToolOutputCacheBoundsSessions(t *testing.T) {
	cache := newWebsocketToolOutputCache(time.Minute, 10)

	for i := range websocketToolOutputCacheMaxSessions * 3 {
		cache.record(fmt.Sprintf("session-%d", i), "call-1", json.RawMessage(`{"a":1}`))
	}

	cache.mu.Lock()
	sessions := len(cache.sessions)
	cache.mu.Unlock()
	if sessions > websocketToolOutputCacheMaxSessions {
		t.Fatalf("tracked sessions = %d, want at most %d", sessions, websocketToolOutputCacheMaxSessions)
	}
}
