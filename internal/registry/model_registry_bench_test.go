package registry

import "testing"

var benchmarkLookupStaticModelInfoSink *ModelInfo

// BenchmarkLookupStaticModelInfo measures a static catalog hit, which is the
// path every request takes when it needs model capabilities.
func BenchmarkLookupStaticModelInfo(b *testing.B) {
	info := LookupStaticModelInfo("claude-sonnet-4-5-20250929")
	if info == nil {
		b.Fatal("static catalog lookup missed claude-sonnet-4-5-20250929")
	}
	benchmarkLookupStaticModelInfoSink = info

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		benchmarkLookupStaticModelInfoSink = LookupStaticModelInfo("claude-sonnet-4-5-20250929")
	}
}
