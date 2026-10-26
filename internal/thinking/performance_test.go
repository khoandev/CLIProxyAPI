package thinking

import (
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

// summaryRescanProbePaths are the candidate paths ExtractSummaryConfig probes
// for the openai format. The pre-index implementation ran one full document
// scan per path; the probe below keeps that shape so the guard in the test is
// proven to still detect it.
var summaryRescanProbePaths = []string{
	"extra_body.google.thinking_config.include_thoughts",
	"extra_body.google.thinking_config.includeThoughts",
	"extra_body.google.thinkingConfig.include_thoughts",
	"extra_body.google.thinkingConfig.includeThoughts",
	"extra_body.extra_body.google.thinking_config.include_thoughts",
	"extra_body.extra_body.google.thinking_config.includeThoughts",
	"google.thinking_config.include_thoughts",
	"google.thinking_config.includeThoughts",
	"thinking.includeThoughts",
	"thinking.include_thoughts",
	"reasoning.includeThoughts",
	"reasoning.include_thoughts",
	"generationConfig.thinkingConfig.includeThoughts",
	"generationConfig.thinkingConfig.include_thoughts",
	"generation_config.thinking_config.include_thoughts",
	"generation_config.thinking_config.includeThoughts",
	"reasoning.summary",
	"reasoning.generate_summary",
	"reasoning_effort",
}

// summaryRescanProbe resolves every candidate path with a plain lookup, which
// scans the whole document once per path. The guard must reject it.
func summaryRescanProbe(body []byte) {
	for _, path := range summaryRescanProbePaths {
		if gjson.GetBytes(body, path).Exists() {
			return
		}
	}
}

// fastestSummarySample returns the shortest duration observed over samples
// runs of op. The minimum drops load spikes and scheduler noise that a mean
// would absorb.
func fastestSummarySample(samples int, op func()) time.Duration {
	best := time.Duration(0)
	for i := range samples {
		start := time.Now()
		op()
		elapsed := time.Since(start)
		if i == 0 || elapsed < best {
			best = elapsed
		}
	}
	return best
}

// TestExtractSummaryConfigDoesNotRescanLargePayload proves the summary config
// extraction does not rescan the payload once per candidate path.
//
// The extraction probes a fixed list of paths, and a plain gjson lookup of an
// absent path scans the whole document. With a large message array that cost
// was paid for every candidate, so the extraction must index the payload once
// and answer every probe from that index.
func TestExtractSummaryConfigDoesNotRescanLargePayload(t *testing.T) {
	payload := []byte(`{"messages":["` + strings.Repeat("x", 1<<20) + `"],"model":"m"}`)

	// Interleave both measurements sample by sample so they observe the same
	// machine conditions; comparing two separately timed benchmarks made the
	// ratio depend on what the machine did between them.
	const (
		samples = 24
		// The measured ratio is ~5: the extraction validates the payload and
		// builds the index, while the rescan this test guards against probes 19
		// candidate paths and lands above 20x. The limit sits between them so
		// neither scheduler noise nor GC pressure from a loaded machine can
		// move it.
		maxRatio = 12
	)
	var lookupBest, extractionBest time.Duration
	timeOnce := func(op func()) time.Duration {
		start := time.Now()
		op()
		return time.Since(start)
	}
	for i := range samples {
		lookup := timeOnce(func() {
			if gjson.GetBytes(payload, "reasoning.summary").Exists() {
				t.Fatal("unexpected summary config")
			}
		})
		extraction := timeOnce(func() {
			if config := ExtractSummaryConfig(payload, "openai"); config.Mode != SummaryUnspecified {
				t.Fatalf("unexpected summary config: %+v", config)
			}
		})
		if i == 0 || lookup < lookupBest {
			lookupBest = lookup
		}
		if i == 0 || extraction < extractionBest {
			extractionBest = extraction
		}
	}
	if lookupBest <= 0 {
		t.Fatal("single lookup measurement produced no samples")
	}

	t.Logf("extraction %d ns, single lookup %d ns, ratio %.2f", extractionBest.Nanoseconds(), lookupBest.Nanoseconds(), float64(extractionBest)/float64(lookupBest))
	// The extraction validates the payload and then indexes it, so it must stay
	// within a small multiple of one scan instead of scaling with the number
	// of candidate paths.
	if extractionBest > lookupBest*maxRatio {
		t.Fatalf(
			"summary extraction rescans large payload: extraction %d ns/op, single lookup %d ns/op",
			extractionBest.Nanoseconds(),
			lookupBest.Nanoseconds(),
		)
	}

	// The guard is only meaningful if it still rejects the pre-index behavior.
	rescanBest := fastestSummarySample(samples, func() {
		summaryRescanProbe(payload)
	})
	if rescanBest <= lookupBest*maxRatio {
		t.Fatalf(
			"guard is too loose to detect a rescan: rescan probe %d ns/op, single lookup %d ns/op, limit %dx",
			rescanBest.Nanoseconds(),
			lookupBest.Nanoseconds(),
			maxRatio,
		)
	}
}
