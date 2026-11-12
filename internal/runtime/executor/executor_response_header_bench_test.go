package executor

import (
	"net/http"
	"testing"
)

// upstreamResponseHeaders returns a header set with the size of a typical
// upstream response.
func upstreamResponseHeaders() http.Header {
	headers := http.Header{}
	headers.Set("Content-Type", "application/json")
	headers.Set("Content-Length", "2048")
	headers.Set("Date", "Mon, 12 Nov 2026 10:00:00 GMT")
	headers.Set("Server", "cloudflare")
	headers.Set("Cf-Ray", "8a1b2c3d4e5f6789-CDG")
	headers.Set("Openai-Processing-Ms", "128")
	headers.Set("Openai-Version", "2020-10-01")
	headers.Set("X-Request-Id", "req_8a1b2c3d4e5f6789a1b2c3d4e5f67890")
	headers.Set("X-Ratelimit-Limit-Requests", "10000")
	headers.Set("X-Ratelimit-Remaining-Requests", "9999")
	headers.Set("X-Ratelimit-Reset-Requests", "6ms")
	headers.Set("X-Ratelimit-Limit-Tokens", "2000000")
	headers.Set("X-Ratelimit-Remaining-Tokens", "1999000")
	headers.Set("X-Request-Cost", "0.00123")
	headers.Set("Openai-Organization", "org-1234567890")
	headers.Set("Set-Cookie", "session=0123456789abcdef; Path=/; HttpOnly")
	return headers
}

// BenchmarkUpstreamResponseHeaderClone measures the cost of copying the
// upstream response headers.
//
// The executors used to clone the header map before handing it to the request
// logger. The logger copies the headers itself, so the clone was a second
// copy of the same data on every response. The benchmark keeps the number of
// allocations and bytes the removed clone cost.
func BenchmarkUpstreamResponseHeaderClone(b *testing.B) {
	headers := upstreamResponseHeaders()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		cloned := headers.Clone()
		if len(cloned) == 0 {
			b.Fatal("clone lost the headers")
		}
	}
}
