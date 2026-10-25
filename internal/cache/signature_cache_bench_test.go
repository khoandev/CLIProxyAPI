package cache

import (
	"bytes"
	"context"
	"testing"
)

var benchmarkSignatureCacheSink string

// BenchmarkSignatureCacheSetGet measures the in-memory signature cache set and
// get pair on a realistic thinking payload.
func BenchmarkSignatureCacheSetGet(b *testing.B) {
	ctx := context.Background()
	text := string(bytes.Repeat([]byte(`thinking about the request `), 80)) // ~2 KiB
	signature := string(bytes.Repeat([]byte("c2lnbmF0dXJl"), 40))
	benchmarkSignatureCacheSink = signature

	b.ReportAllocs()
	b.SetBytes(int64(len(text)))
	b.ResetTimer()
	for b.Loop() {
		if !CacheSignatureBestEffort(ctx, "claude-sonnet-4-5-20250929", text, signature) {
			b.Fatal("CacheSignatureBestEffort reported no write")
		}
		cached, err := GetCachedSignatureRequired(ctx, "claude-sonnet-4-5-20250929", text)
		if err != nil {
			b.Fatalf("GetCachedSignatureRequired: %v", err)
		}
		if cached != signature {
			b.Fatalf("cached signature = %q, want %q", cached, signature)
		}
	}
}
