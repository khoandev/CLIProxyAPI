package helps

import "bytes"

// jsonMayContainASCII reports whether decoding the JSON document body could
// yield a key or string value containing any of needles.
//
// It lets the request classifiers below skip their gjson walks: a Claude Code
// request body carries the whole conversation (often several MB), and each
// walk re-scans it. bytes.Contains is far cheaper than a gjson walk.
//
// Every needle must be printable ASCII without '\\' or '/' (JSON may spell
// '/' as `\/`). A '"' may only stand at
// either end of a needle, where it matches a structural string delimiter
// (`"1h"` matches the whole string value 1h). JSON can only
// spell such a character as itself or as a \u00XX escape, so when body holds
// no \u escape of a printable ASCII character, a needle that is absent from
// the raw bytes is absent from every decoded string too. If such an escape is
// present the function answers true and the caller falls back to its full
// walk, so the result never changes, only the cost.
//
// A needle longer than eight bytes is searched for by its first eight bytes
// only. The search for a longer needle is markedly slower, and a body that
// holds the whole needle always holds that prefix, so the answer only ever
// becomes more cautious: a caller that skips a walk it would otherwise have
// performed still skips it only when the needle is truly absent.
func jsonMayContainASCII(body []byte, needles ...string) bool {
	if hasPrintableASCIIUnicodeEscape(body) {
		return true
	}
	for _, needle := range needles {
		prefix := needle
		if len(prefix) > jsonPrefilterNeedleSearchLimit {
			prefix = prefix[:jsonPrefilterNeedleSearchLimit]
		}
		if bytes.Contains(body, []byte(prefix)) {
			return true
		}
	}
	return false
}

// jsonPrefilterNeedleSearchLimit is the number of leading needle bytes that
// are searched for. Go searches a needle of at most this length considerably
// faster than a longer one.
const jsonPrefilterNeedleSearchLimit = 8

// hasPrintableASCIIUnicodeEscape reports whether body contains a \u00XX
// escape whose first hex digit is 2-7, i.e. a printable ASCII character. It
// errs only toward "yes": it also matches DEL (7F), malformed escapes such as
// a 2-7 digit followed by a non-hex character, an escape truncated after that
// digit, and an escaped backslash followed by literal text such as `\\u0041`.
// Each of those only makes the caller fall back to a full walk.
func hasPrintableASCIIUnicodeEscape(body []byte) bool {
	for rest := body; ; {
		i := bytes.Index(rest, []byte(`\u00`))
		if i < 0 || i+4 >= len(rest) {
			return false
		}
		if c := rest[i+4]; c >= '2' && c <= '7' {
			return true
		}
		rest = rest[i+4:]
	}
}
