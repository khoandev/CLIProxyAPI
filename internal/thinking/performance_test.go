package thinking

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

// TestExtractSummaryConfigDoesNotRescanLargePayload proves the summary config
// extraction does not rescan the payload once per candidate path.
//
// The extraction probes a fixed list of paths, and a plain gjson lookup of an
// absent path scans the whole document. With a large message array that cost
// was paid for every candidate, so the extraction must index the payload once
// and answer every probe from that index.
func TestExtractSummaryConfigDoesNotRescanLargePayload(t *testing.T) {
	payload := []byte(`{"messages":["` + strings.Repeat("x", 1<<20) + `"],"model":"m"}`)

	singleLookup := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			if gjson.GetBytes(payload, "reasoning.summary").Exists() {
				b.Fatal("unexpected summary config")
			}
		}
	})
	extraction := testing.Benchmark(func(b *testing.B) {
		for range b.N {
			if config := ExtractSummaryConfig(payload, "openai"); config.Mode != SummaryUnspecified {
				b.Fatalf("unexpected summary config: %+v", config)
			}
		}
	})

	// The extraction validates the payload and then indexes it, so it must stay
	// within a small multiple of one scan instead of scaling with the number
	// of candidate paths.
	if extraction.NsPerOp() > singleLookup.NsPerOp()*6 {
		t.Fatalf("summary extraction rescans large payload: extraction %d ns/op, single lookup %d ns/op", extraction.NsPerOp(), singleLookup.NsPerOp())
	}
}
