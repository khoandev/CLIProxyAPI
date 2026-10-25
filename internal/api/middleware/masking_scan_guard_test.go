package middleware

import (
	"strings"
	"testing"
)

// TestMaskingScanIsExactAndAllocationFree pins the two properties the response
// prefilter must keep.
//
// Exactness: every rule source the writer would rewrite must be detected by the
// prefilter, otherwise the rewrite is skipped and vendor branding leaks. The
// scan looks for cased needle variants, so this also proves each variant family
// covers its rules.
//
// Allocation: masking runs on every streamed chunk. A prefilter that allocates
// (a lower-cased copy of the chunk, a regexp match, a converted string) costs a
// heap allocation per chunk on the hot path.
func TestMaskingScanIsExactAndAllocationFree(t *testing.T) {
	const sentence = "the quick brown fox jumps over the lazy dog "
	clean := []byte(strings.Repeat(sentence, (2<<20)/len(sentence)+1)) // ~2 MiB
	if len(clean) < 2<<20 {
		t.Fatalf("clean body = %d bytes, want at least 2 MiB", len(clean))
	}

	sets := map[string]maskRuleSet{
		"brandOnly":        brandOnlySet,
		"brandClaude":      brandClaudeSet,
		"brandOpenAI":      brandOpenAISet,
		"brandAntigravity": brandAntigravitySet,
	}

	if allocs := testing.AllocsPerRun(5, func() { brandClaudeSet.containsNeedleBytes(clean) }); allocs != 0 {
		t.Fatalf("clean-body scan allocates %v per run, want 0", allocs)
	}
	if allocs := testing.AllocsPerRun(5, func() { brandClaudeSet.mask(clean) }); allocs != 0 {
		t.Fatalf("clean-body mask allocates %v per run, want 0", allocs)
	}

	for name, set := range sets {
		if set.containsNeedleBytes(clean) {
			t.Errorf("%s: clean body reported a needle hit", name)
		}
		for _, rule := range set.rules {
			body := []byte(sentence + rule.from + sentence)
			if !set.containsNeedleBytes(body) {
				t.Errorf("%s: prefilter missed rule source %q", name, rule.from)
			}
			if masked := string(set.mask(body)); strings.Contains(masked, rule.from) {
				t.Errorf("%s: mask left rule source %q in place: %q", name, rule.from, masked)
			}
		}
	}
}
