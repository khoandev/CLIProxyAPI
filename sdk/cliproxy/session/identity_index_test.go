package session

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
)

// TestClaudeMetadataIdentitiesMatchesIndexedLookup proves the indexed lookup
// resolves the same identities as a direct document lookup. The indexed path
// exists to avoid rescanning the payload for every candidate path, so it must
// not change which identity is picked.
func TestClaudeMetadataIdentitiesMatchesIndexedLookup(t *testing.T) {
	payloads := []string{
		`{}`,
		`{"metadata":{"user_id":"plain"}}`,
		`{"metadata":{"user_id":"{\"session_id\":\"s1\",\"parent_session_id\":\"p1\"}"}}`,
		`{"metadata":{"user_id":"{\"session_id\":\"s1\",\"agent_id\":\"a1\"}"}}`,
		`{"metadata":{"user_id":"_session_01234567-89ab-cdef-0123-456789abcdef"}}`,
		`{"metadata":{"user_id":"u"},"metadata":{"user_id":"v"}}`,
		`{"metadata":{},"metadata":{"user_id":"v"}}`,
		`{"request":{"metadata":{"user_id":"{\"session_id\":\"nested\"}"}}}`,
		`{"contents":[],"request":{"metadata":{"user_id":"{\"session_id\":\"nested\"}"}}}`,
		`{"metadata":{"user_id":"{\"session_id\":\"s1\"}"},"metadata":{"user_id":"{\"session_id\":\"s2\"}"}}`,
	}

	for _, payload := range payloads {
		wantSession, wantParent, wantAgent := ClaudeMetadataIdentities([]byte(payload))
		index := newSessionObject(util.ParseGJSONBytesNoCopy([]byte(payload)))
		gotSession, gotParent, gotAgent := claudeMetadataIdentities(index)
		if gotSession != wantSession || gotParent != wantParent || gotAgent != wantAgent {
			t.Fatalf("payload %s: indexed lookup = (%q, %q, %q), want (%q, %q, %q)",
				payload, gotSession, gotParent, gotAgent, wantSession, wantParent, wantAgent)
		}
	}
}
