package responses

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	"github.com/tidwall/gjson"
)

// TestResponsesRequestIndexedLookupDoesNotRescanPayload proves the indexed
// lookup path is not slower and does not allocate more than the plain parse
// path.
//
// A plain gjson lookup of an absent path scans the whole document, so a
// payload with many candidate paths pays one full scan per path. The index
// performs that scan once.
func TestResponsesRequestIndexedLookupDoesNotRescanPayload(t *testing.T) {
	request := responsesPerfRequest(10)

	lookupPaths := []string{
		"text.format",
		"max_output_tokens",
		"instructions",
		"reasoning.effort",
		"reasoning_effort",
		"reasoning",
		"parallel_tool_calls",
		"tool_choice",
	}

	plain := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			root := gjson.ParseBytes(request)
			for _, path := range lookupPaths {
				if root.Get(path).Exists() {
					b.Fatalf("unexpected %s", path)
				}
			}
		}
	})
	indexed := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			index := util.NewJSONIndex(request)
			for _, path := range lookupPaths {
				if index.Get(path).Exists() {
					b.Fatalf("unexpected %s", path)
				}
			}
		}
	})

	if indexed.NsPerOp() > plain.NsPerOp()*11/10 {
		t.Fatalf("indexed lookup is slower than the plain parse path: indexed %d ns/op, plain %d ns/op", indexed.NsPerOp(), plain.NsPerOp())
	}
	if indexed.AllocsPerOp() > plain.AllocsPerOp() {
		t.Fatalf("indexed lookup allocates more than the plain parse path: indexed %d allocs/op, plain %d allocs/op", indexed.AllocsPerOp(), plain.AllocsPerOp())
	}
	if indexed.AllocedBytesPerOp() > plain.AllocedBytesPerOp() {
		t.Fatalf("indexed lookup allocates more bytes than the plain parse path: indexed %d B/op, plain %d B/op", indexed.AllocedBytesPerOp(), plain.AllocedBytesPerOp())
	}
}
