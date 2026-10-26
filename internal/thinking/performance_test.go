package thinking

import (
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

// TestExtractSummaryConfigDoesNotRescanLargePayload proves the summary config
// extraction does not rescan the payload once per candidate path.
//
// The extraction probes a fixed list of paths, and a plain gjson lookup of an
// absent path scans the whole document. With a large message array that cost
// was paid for every candidate, so the extraction must index the payload once
// and answer every probe from that index.
//
// Both sides are measured inside one benchmark loop so they observe the same
// machine conditions. Timing them with two separate benchmarks made the ratio
// depend on what else the machine was doing between the two runs.
func TestExtractSummaryConfigDoesNotRescanLargePayload(t *testing.T) {
	payload := []byte(`{"messages":["` + strings.Repeat("x", 1<<20) + `"],"model":"m"}`)

	var lookupTotal, extractionTotal time.Duration
	testing.Benchmark(func(b *testing.B) {
		for range b.N {
			start := time.Now()
			if gjson.GetBytes(payload, "reasoning.summary").Exists() {
				b.Fatal("unexpected summary config")
			}
			lookupTotal += time.Since(start)

			start = time.Now()
			if config := ExtractSummaryConfig(payload, "openai"); config.Mode != SummaryUnspecified {
				b.Fatalf("unexpected summary config: %+v", config)
			}
			extractionTotal += time.Since(start)
		}
	})
	if lookupTotal <= 0 {
		t.Fatal("single lookup benchmark produced no samples")
	}

	// The extraction validates the payload and then indexes it, so it must stay
	// within a small multiple of one scan instead of scaling with the number
	// of candidate paths.
	if extractionTotal > lookupTotal*6 {
		t.Fatalf(
			"summary extraction rescans large payload: extraction %d ns/op, single lookup %d ns/op",
			extractionTotal.Nanoseconds(),
			lookupTotal.Nanoseconds(),
		)
	}
}
