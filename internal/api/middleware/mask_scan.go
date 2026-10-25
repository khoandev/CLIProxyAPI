package middleware

import (
	"bytes"
	"strings"
)

// needleVariants returns the cased forms a rule family covers for each
// lowercase root needle: lowercase, title case and uppercase.
//
// Every replacement rule spells its source in exactly one of those three forms,
// so scanning for the variants finds every byte sequence a rule can rewrite.
// Mixed cases such as "kIrA" are not rewritten by any rule, so not detecting
// them changes no output byte. TestMaskNeedleVariantsCoverEveryRule pins the
// invariant that keeps this prefilter exact.
func needleVariants(needles []string) []string {
	variants := make([]string, 0, len(needles)*3)
	for _, needle := range needles {
		if needle == "" {
			continue
		}
		variants = append(variants, needle, titleFirst(needle), strings.ToUpper(needle))
	}
	return variants
}

// containsNeedleBytes reports whether b carries any needle variant of the set.
//
// bytes.Contains is used instead of a byte-at-a-time case-folded loop: it scans
// with SIMD, and masking runs on every streamed response chunk.
func (s maskRuleSet) containsNeedleBytes(b []byte) bool {
	for _, variant := range s.variants {
		if bytes.Contains(b, []byte(variant)) {
			return true
		}
	}
	return false
}

// containsNeedleString reports whether str carries any needle variant of the
// set.
func (s maskRuleSet) containsNeedleString(str string) bool {
	for _, variant := range s.variants {
		if strings.Contains(str, variant) {
			return true
		}
	}
	return false
}
