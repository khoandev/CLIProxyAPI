package executor

import (
	"strconv"
	"testing"
	"time"
)

func TestXAIWebsocketIDStateStoreEvictsExcessSessions(t *testing.T) {
	store := &xaiWebsocketIDStateStore{sessions: make(map[string]*xaiWebsocketIDState)}
	for i := 0; i < codexWebsocketSessionStoreMaxSessions*2; i++ {
		state := getXAIWebsocketIDState(store, "session-"+strconv.Itoa(i))
		if state == nil {
			t.Fatalf("getXAIWebsocketIDState returned nil")
		}
	}
	store.mu.Lock()
	count := len(store.sessions)
	store.mu.Unlock()
	if count > codexWebsocketSessionStoreMaxSessions {
		t.Fatalf("stored id states = %d, want at most %d", count, codexWebsocketSessionStoreMaxSessions)
	}
}

func TestXAIWebsocketIDStateStoreEvictsIdleSessions(t *testing.T) {
	store := &xaiWebsocketIDStateStore{sessions: make(map[string]*xaiWebsocketIDState)}
	getXAIWebsocketIDState(store, "fresh")
	getXAIWebsocketIDState(store, "stale")

	store.mu.Lock()
	store.sessions["stale"].lastUsed = time.Now().Add(-codexWebsocketSessionIdleTTL - time.Minute)
	store.mu.Unlock()

	store.evictExcessSessions(time.Now())

	store.mu.Lock()
	_, stalePresent := store.sessions["stale"]
	_, freshPresent := store.sessions["fresh"]
	store.mu.Unlock()
	if stalePresent {
		t.Fatalf("id state older than the idle TTL must be evicted")
	}
	if !freshPresent {
		t.Fatalf("fresh id state must be kept")
	}
}

func TestXAIWebsocketTranscriptIsBounded(t *testing.T) {
	state := &xaiWebsocketIDState{downstreamToUpstream: make(map[string]string)}

	// 1000 items of 32 KiB each would be ~32 MiB without the cap.
	item := make([]byte, 32<<10)
	for i := range item {
		item[i] = 'a'
	}
	requestPayload := []byte(`{"input":[{"type":"message","role":"user","content":"` + string(item) + `"}]}`)
	completedPayload := []byte(`{"response":{"output":[{"type":"message","role":"assistant","content":"` + string(item) + `"}]}}`)

	for i := 0; i < 1000; i++ {
		state.recordTranscriptTurn(requestPayload, completedPayload, false)
	}

	state.mu.Lock()
	bytesUsed := state.transcriptBytes
	items := len(state.transcriptInput)
	actual := 0
	for _, entry := range state.transcriptInput {
		actual += len(entry)
	}
	state.mu.Unlock()

	if bytesUsed > xaiWebsocketTranscriptMaxBytes {
		t.Fatalf("transcriptBytes = %d, want at most %d", bytesUsed, xaiWebsocketTranscriptMaxBytes)
	}
	if items > xaiWebsocketTranscriptMaxItems {
		t.Fatalf("transcript items = %d, want at most %d", items, xaiWebsocketTranscriptMaxItems)
	}
	if actual != bytesUsed {
		t.Fatalf("tracked transcriptBytes = %d, actual = %d", bytesUsed, actual)
	}
}

func TestXAIWebsocketTranscriptTrimKeepsNewestItems(t *testing.T) {
	state := &xaiWebsocketIDState{downstreamToUpstream: make(map[string]string)}

	// One item per turn, so the item cap is what forces the trim.
	for i := 0; i < xaiWebsocketTranscriptMaxItems+10; i++ {
		item := `{"n":` + strconv.Itoa(i) + `}`
		state.recordTranscriptTurn([]byte(`{"input":[`+item+`]}`), []byte(`{"response":{"output":[]}}`), false)
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	if len(state.transcriptInput) != xaiWebsocketTranscriptMaxItems {
		t.Fatalf("items = %d, want %d", len(state.transcriptInput), xaiWebsocketTranscriptMaxItems)
	}
	last := string(state.transcriptInput[len(state.transcriptInput)-1])
	want := `{"n":` + strconv.Itoa(xaiWebsocketTranscriptMaxItems+9) + `}`
	if last != want {
		t.Fatalf("newest item = %s, want %s", last, want)
	}
	first := string(state.transcriptInput[0])
	wantFirst := `{"n":10}`
	if first != wantFirst {
		t.Fatalf("oldest kept item = %s, want %s", first, wantFirst)
	}
}

func TestXAIWebsocketIDMapIsBounded(t *testing.T) {
	state := &xaiWebsocketIDState{downstreamToUpstream: make(map[string]string)}
	mapper := &xaiWebsocketRequestIDMapper{state: state}
	for i := 0; i < xaiWebsocketIDMapMaxEntries*2; i++ {
		mapper.downstreamIDForUpstreamResponse("resp-" + strconv.Itoa(i))
		// Each call caches one response id, so keep the mapper from short-circuiting.
		mapper.upstreamResponseID = ""
	}
	state.mu.Lock()
	size := len(state.downstreamToUpstream)
	state.mu.Unlock()
	if size > xaiWebsocketIDMapMaxEntries {
		t.Fatalf("id map size = %d, want at most %d", size, xaiWebsocketIDMapMaxEntries)
	}
}

func TestCodexWebsocketSessionStoreNotifiesEviction(t *testing.T) {
	store := &codexWebsocketSessionStore{sessions: make(map[string]*codexWebsocketSession)}
	var evicted []string
	store.onEvict = func(sessionID string) {
		evicted = append(evicted, sessionID)
	}

	sess := &codexWebsocketSession{sessionID: "s-1", lastUsed: time.Now()}
	store.mu.Lock()
	store.sessions["s-1"] = sess
	store.mu.Unlock()

	if !store.removeSession("s-1", sess) {
		t.Fatalf("removeSession = false, want true")
	}
	if len(evicted) != 1 || evicted[0] != "s-1" {
		t.Fatalf("evicted = %v, want [s-1]", evicted)
	}

	// A stale session removed by the idle sweep must notify too.
	stale := &codexWebsocketSession{sessionID: "s-2", lastUsed: time.Now().Add(-codexWebsocketSessionIdleTTL - time.Minute)}
	store.mu.Lock()
	store.sessions["s-2"] = stale
	store.mu.Unlock()
	store.evictExcessSessions()
	if len(evicted) != 2 || evicted[1] != "s-2" {
		t.Fatalf("evicted = %v, want [s-1 s-2]", evicted)
	}
}
