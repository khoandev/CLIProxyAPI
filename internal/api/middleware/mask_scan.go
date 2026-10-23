package middleware

// foldLowerASCII lower-cases an ASCII letter byte, leaving everything else alone.
func foldLowerASCII(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		c |= 0x20
	}
	return c
}

// containsFoldASCII reports whether b contains the lowercase ASCII needle,
// comparing ASCII letters case-insensitively without allocating.
func containsFoldASCII(b []byte, needle string) bool {
	n := len(needle)
	if n == 0 {
		return true
	}
	if n > len(b) {
		return false
	}
	first := needle[0]
	last := len(b) - n
	for i := 0; i <= last; i++ {
		if foldLowerASCII(b[i]) != first {
			continue
		}
		if foldEqualASCII(b[i:i+n], needle) {
			return true
		}
	}
	return false
}

// containsFoldString reports whether s contains the lowercase ASCII needle,
// comparing ASCII letters case-insensitively without allocating.
func containsFoldString(s, needle string) bool {
	n := len(needle)
	if n == 0 {
		return true
	}
	if n > len(s) {
		return false
	}
	first := needle[0]
	last := len(s) - n
	for i := 0; i <= last; i++ {
		if foldLowerASCII(s[i]) != first {
			continue
		}
		if foldEqualString(s[i:i+n], needle) {
			return true
		}
	}
	return false
}

// foldEqualASCII reports whether b equals the lowercase ASCII needle, ignoring
// ASCII letter case. len(b) must equal len(needle).
func foldEqualASCII(b []byte, needle string) bool {
	for i := range b {
		if foldLowerASCII(b[i]) != needle[i] {
			return false
		}
	}
	return true
}

// foldEqualString reports whether s equals the lowercase ASCII needle, ignoring
// ASCII letter case. len(s) must equal len(needle).
func foldEqualString(s, needle string) bool {
	for i := range s {
		if foldLowerASCII(s[i]) != needle[i] {
			return false
		}
	}
	return true
}
