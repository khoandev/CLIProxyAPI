#!/bin/sh
# Benchmark harness for the performance-hardening work.
#
# Usage:
#   scripts/bench.sh <label>            run every tracked benchmark, append to bench-out/<label>.txt
#   scripts/bench.sh compare <a> <b>    benchstat bench-out/<a>.txt bench-out/<b>.txt
#
# Environment:
#   BENCH_TIME   value passed to -benchtime (default: the Go default of 1s).
#                The translator benchmarks move 8 MiB payloads, so the default
#                benchtime makes a full run take hours. The recorded settings
#                are part of the output, so a comparison run must use the same
#                values.
#   BENCH_COUNT  value passed to -count (default: 5).
#
# Run from the repository root.
set -eu

out_dir="bench-out"
bench_time="${BENCH_TIME:-}"
bench_count="${BENCH_COUNT:-5}"

if [ -n "$bench_time" ]; then
	bench_flags="-benchtime $bench_time"
else
	bench_flags=""
fi

# keep_pattern selects the lines that belong into the result file. The
# packages log warnings to stdout and stderr, and an interleaved log line
# lands between the benchmark name and its metrics, which produces a sample
# without any numbers. Only complete result lines are kept.
keep_pattern='^(goos|goarch|pkg|cpu):|^Benchmark.* ns/op|^(PASS|FAIL|ok |--- )'

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
	raw="$out_dir/$label.raw.txt"
	: >"$out"
	: >"$raw"
	echo "# settings benchtime=${bench_time:-default} count=$bench_count" >>"$out"
	echo "$bench_table" | while IFS='|' read -r pkg regex; do
		[ -n "$pkg" ] || continue
		echo "## $pkg $regex" >>"$out"
		group_raw="$out_dir/.bench-group.raw"
		: >"$group_raw"
		# shellcheck disable=SC2086
		go test -run '^$' -bench "$regex" -benchmem -count "$bench_count" $bench_flags "$pkg" >"$group_raw" 2>&1 || true
		grep -E "$keep_pattern" "$group_raw" >>"$out" || true
		if grep -qE 'FAIL|build failed|cannot find package|no Go files' "$group_raw"; then
			echo "# stderr: failure in $pkg" >>"$out"
		fi
		cat "$group_raw" >>"$raw"
		rm -f "$group_raw"
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
