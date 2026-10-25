#!/bin/sh
# Benchmark harness for the performance-hardening work.
#
# Usage:
#   scripts/bench.sh <label>            run every tracked benchmark, append to bench-out/<label>.txt
#   scripts/bench.sh compare <a> <b>    benchstat bench-out/<a>.txt bench-out/<b>.txt
#
# Run from the repository root.
set -eu

out_dir="bench-out"

# package|benchmark-regex pairs. Keep the regex anchored enough to avoid
# pulling in unrelated benchmarks from the same package.
bench_table='
./internal/api/middleware/|Benchmark(Masking|ResponseWriter|StreamingChunkPath)
./internal/api/|BenchmarkInFlightAdmission
./internal/translator/|Benchmark(ResponseTranslationLargePayload|RequestTranslationHistorySizes)
./internal/translator/gemini/openai/chat-completions/|BenchmarkConvertGeminiResponseToOpenAI_TextDelta
./internal/translator/gemini/openai/responses/|BenchmarkConvertGeminiResponseToOpenAIResponses_TextDelta
./internal/translator/antigravity/claude/|BenchmarkAntigravityClaudeLongRequest
./internal/translator/claude/openai/chat-completions/|BenchmarkClaudeOpenAIStreamChunk
./internal/util/|BenchmarkSharedRequestBody
./internal/runtime/executor/helps/|BenchmarkDeferAPIRequestLargeBody
./internal/cache/|BenchmarkSignatureCache
./internal/registry/|BenchmarkLookupStaticModelInfo
./internal/thinking/|BenchmarkApplyThinking
'

usage() {
	echo "usage: $0 <label>" >&2
	echo "       $0 compare <baseline-label> <after-label>" >&2
	exit 2
}

run_all() {
	label="$1"
	mkdir -p "$out_dir"
	out="$out_dir/$label.txt"
	: >"$out"
	echo "$bench_table" | while IFS='|' read -r pkg regex; do
		[ -n "$pkg" ] || continue
		echo "## $pkg $regex" >>"$out"
		go test -run '^$' -bench "$regex" -benchmem -count 5 "$pkg" >>"$out" 2>&1 || true
	done
	echo "wrote $out"
}

compare() {
	a="$1"
	b="$2"
	if ! command -v benchstat >/dev/null 2>&1; then
		echo "benchstat not found; install it with:" >&2
		echo "  go install golang.org/x/perf/cmd/benchstat@latest" >&2
		exit 1
	fi
	benchstat "$out_dir/$a.txt" "$out_dir/$b.txt"
}

[ $# -ge 1 ] || usage

case "$1" in
compare)
	[ $# -eq 3 ] || usage
	compare "$2" "$3"
	;;
*)
	[ $# -eq 1 ] || usage
	run_all "$1"
	;;
esac
