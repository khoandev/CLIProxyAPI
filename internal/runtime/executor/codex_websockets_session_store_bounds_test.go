package executor

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

// TestCodexWebsocketSessionStoreBoundsSessions proves the store never keeps
// more sessions than its cap, and that the evicted session is closed.
func TestCodexWebsocketSessionStoreBoundsSessions(t *testing.T) {
	exec := NewCodexWebsocketsExecutor(&config.Config{})
	store := &codexWebsocketSessionStore{sessions: make(map[string]*codexWebsocketSession)}
	exec.store = store

	server, _ := newWebsocketTargetServer(t)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	conn, physical := newCloseCountingWebsocketConn(t, wsURL)
	first := exec.getOrCreateSession("session-0")
	first.connMu.Lock()
	first.conn = conn
	first.connCloser = newWebsocketConnectionCloser(conn)
	first.wsURL = wsURL
	first.authID = "auth-1"
	first.readerConn = conn
	first.connMu.Unlock()

	for i := 1; i <= codexWebsocketSessionStoreMaxSessions*2; i++ {
		exec.getOrCreateSession(fmt.Sprintf("session-%d", i))
	}

	store.mu.Lock()
	count := len(store.sessions)
	_, firstPresent := store.sessions["session-0"]
	store.mu.Unlock()

	if count > codexWebsocketSessionStoreMaxSessions {
		t.Fatalf("stored sessions = %d, want at most %d", count, codexWebsocketSessionStoreMaxSessions)
	}
	if firstPresent {
		t.Fatal("the least recently used session was not evicted")
	}
	if got := physical.closes.Load(); got != 1 {
		t.Fatalf("evicted session websocket closes = %d, want 1", got)
	}
}

// TestCodexWebsocketSessionStoreSweepsIdleSessions proves a session that
// stayed idle for too long is closed and dropped.
func TestCodexWebsocketSessionStoreSweepsIdleSessions(t *testing.T) {
	exec := NewCodexWebsocketsExecutor(&config.Config{})
	store := &codexWebsocketSessionStore{sessions: make(map[string]*codexWebsocketSession)}
	exec.store = store

	server, _ := newWebsocketTargetServer(t)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	idle := exec.getOrCreateSession("idle-session")
	conn, physical := newCloseCountingWebsocketConn(t, wsURL)
	idle.connMu.Lock()
	idle.conn = conn
	idle.connCloser = newWebsocketConnectionCloser(conn)
	idle.wsURL = wsURL
	idle.authID = "auth-1"
	idle.readerConn = conn
	idle.connMu.Unlock()

	store.mu.Lock()
	idle.lastUsed = time.Now().Add(-codexWebsocketSessionIdleTTL - time.Minute)
	store.mu.Unlock()

	exec.getOrCreateSession("fresh-session")

	store.mu.Lock()
	_, idlePresent := store.sessions["idle-session"]
	store.mu.Unlock()
	if idlePresent {
		t.Fatal("idle session survived the idle sweep")
	}
	if got := physical.closes.Load(); got != 1 {
		t.Fatalf("swept session websocket closes = %d, want 1", got)
	}
}

// TestCodexWebsocketSessionStoreKeepsActiveSessions proves a session that
// currently serves a request is not evicted.
func TestCodexWebsocketSessionStoreKeepsActiveSessions(t *testing.T) {
	exec := NewCodexWebsocketsExecutor(&config.Config{})
	store := &codexWebsocketSessionStore{sessions: make(map[string]*codexWebsocketSession)}
	exec.store = store

	active := exec.getOrCreateSession("active-session")
	active.setActive(nil, make(chan codexWebsocketRead, 1))

	for i := range codexWebsocketSessionStoreMaxSessions * 2 {
		exec.getOrCreateSession(fmt.Sprintf("session-%d", i))
	}

	store.mu.Lock()
	_, activePresent := store.sessions["active-session"]
	store.mu.Unlock()
	if !activePresent {
		t.Fatal("active session was evicted")
	}
}
