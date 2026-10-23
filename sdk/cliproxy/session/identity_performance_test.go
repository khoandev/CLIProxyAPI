package session

import (
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
)

// TestExtractSessionInfoDoesNotRescanLargePayloadWithLateMetadata proves the
// session extraction does not rescan the payload once per candidate path.
//
// The extraction probes a fixed list of paths, and a plain gjson lookup of an
// absent path scans the whole document. The session identity usually sits
// behind the message array, so the scan was paid for every candidate.
func TestExtractSessionInfoDoesNotRescanLargePayloadWithLateMetadata(t *testing.T) {
	payload := []byte(`{"messages":["` + strings.Repeat("x", 1<<20) + `"],"metadata":{"user_id":"{\"session_id\":\"s1\"}"}}`)

	singleLookup := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			root := util.ParseGJSONBytesNoCopy(payload)
			if root.Get("missing_session_id").Exists() {
				b.Fatal("unexpected session ID")
			}
		}
	})
	extraction := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			if info, ok := ExtractSessionInfo(nil, payload, nil); !ok || info.SessionID == "" {
				b.Fatal("unexpected session")
			}
		}
	})

	// The extraction indexes the payload once and answers every probe from that
	// index, so it must stay close to a single scan. Without the shared index
	// the extraction paid one scan per candidate path.
	if extraction.NsPerOp() > singleLookup.NsPerOp()*2 {
		t.Fatalf("session extraction rescans large payload: extraction %d ns/op, single lookup %d ns/op", extraction.NsPerOp(), singleLookup.NsPerOp())
	}
}
