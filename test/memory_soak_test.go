package test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/api"
	proxyconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	runtimeexecutor "github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor"
	_ "github.com/router-for-me/CLIProxyAPI/v8/internal/translator"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v8/sdk/access"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
	log "github.com/sirupsen/logrus"
)

// Memory soak harness.
//
// It drives the real proxy request path (gin middleware chain, credential
// selection, request translation, upstream call, response streaming) against a
// stub upstream and reports how much memory the process holds for three
// workloads:
//
//  1. non-stream  - 20 sequential JSON responses,
//  2. sse-stream  - 20 sequential streaming responses,
//  3. large-body  - 1 request whose body is ~10 MiB.
//
// The harness is gated because it allocates a few hundred megabytes of traffic
// and takes several seconds:
//
//	CLIPROXY_MEMSOAK=1 go test -run TestMemorySoak ./test/ -v
//
// The label and output path can be overridden:
//
//	CLIPROXY_MEMSOAK=1 CLIPROXY_MEMSOAK_LABEL=after CLIPROXY_MEMSOAK_OUT=bench-out/mem-after.txt go test -run TestMemorySoak ./test/ -v
const (
	memSoakGateEnv     = "CLIPROXY_MEMSOAK"
	memSoakLabelEnv    = "CLIPROXY_MEMSOAK_LABEL"
	memSoakOutEnv      = "CLIPROXY_MEMSOAK_OUT"
	memSoakBaselineEnv = "CLIPROXY_MEMSOAK_BASELINE"
)

// memSoakModel is the model served by the stub upstream.
const memSoakModel = "gpt-5-codex"

// memSoakRSSGrowthLimit is the maximum resident set growth a single scenario
// may cause before the harness fails. It is deliberately generous: the harness
// reports numbers, it does not enforce a performance budget. The guard uses the
// peak RSS because a post-GC heap snapshot can never show the transient
// allocations of a request.
const memSoakRSSGrowthLimit = 512 << 20

// memSoakRepeats is how many times each scenario runs. The median of the
// repeats is reported, so one noisy run does not define the baseline.
const memSoakRepeats = 3

// memSample is one memory observation of the test process.
type memSample struct {
	HeapAlloc   uint64
	HeapInuse   uint64
	HeapObjects uint64
	Sys         uint64
	RSSBytes    int64
}

// memScenario describes one workload of the soak.
type memScenario struct {
	name      string
	requests  int
	stream    bool
	bodyBytes int
}

// memScenarioResult is the outcome of one workload.
type memScenarioResult struct {
	scenario   memScenario
	before     memSample
	after      memSample
	peakRSS    int64
	maxPeakRSS int64
	heapGrowth int64
	bytesRead  int64
	repeats    int
}

func TestMemorySoak(t *testing.T) {
	if os.Getenv(memSoakGateEnv) != "1" {
		t.Skipf("set %s=1 to run the memory soak harness", memSoakGateEnv)
	}

	previousLevel := log.GetLevel()
	log.SetLevel(log.PanicLevel)
	defer log.SetLevel(previousLevel)

	proxy := memSoakStartProxy(t)
	defer proxy.Close()

	client := &http.Client{
		Timeout:   60 * time.Second,
		Transport: &http.Transport{MaxIdleConnsPerHost: 64},
	}

	scenarios := []memScenario{
		{name: "non-stream", requests: 20},
		{name: "sse-stream", requests: 20, stream: true},
		{name: "large-body", requests: 1, bodyBytes: 10 << 20},
	}

	// The warm-up requests absorb the lazy initialization of the executors,
	// the translators and the transport pools, so the first scenario does not
	// pay for them.
	memSoakWarmUp(t, client, proxy.URL)

	results := make([]memScenarioResult, 0, len(scenarios))
	for _, scenario := range scenarios {
		result := memSoakRunScenario(t, client, proxy.URL, scenario)
		results = append(results, result)
		t.Logf("scenario=%s requests=%d stream=%t body_bytes=%d repeats=%d heap_alloc=%d->%d heap_inuse=%d sys=%d rss_peak=%d rss_max=%d rss_end=%d heap_growth=%d",
			scenario.name, scenario.requests, scenario.stream, scenario.bodyBytes, result.repeats,
			result.before.HeapAlloc, result.after.HeapAlloc, result.after.HeapInuse, result.after.Sys,
			result.peakRSS, result.maxPeakRSS, result.after.RSSBytes, result.heapGrowth)
	}

	report := memSoakBuildReport(results)
	if delta := memSoakBuildDelta(results, memSoakBaselinePath()); delta != "" {
		report += delta
	}
	outPath := memSoakOutputPath()
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		t.Fatalf("create report directory: %v", err)
	}
	if err := os.WriteFile(outPath, []byte(report), 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	t.Logf("report written to %s", outPath)
}

// memSoakRunScenario runs a workload several times and reports the median run,
// so a single noisy measurement does not define the baseline.
func memSoakRunScenario(t *testing.T, client *http.Client, baseURL string, scenario memScenario) memScenarioResult {
	t.Helper()

	runs := make([]memScenarioResult, 0, memSoakRepeats)
	for range memSoakRepeats {
		runs = append(runs, memSoakRunScenarioOnce(t, client, baseURL, scenario))
	}

	// The reported before/after samples must come from the same run as the
	// reported growth, otherwise the table mixes numbers of different runs.
	medianGrowth := memSoakMedianInt64(runs, func(run memScenarioResult) int64 { return run.heapGrowth })
	median := runs[0]
	for _, run := range runs {
		if run.heapGrowth == medianGrowth {
			median = run
			break
		}
	}
	median.repeats = len(runs)
	median.peakRSS = memSoakMedianInt64(runs, func(run memScenarioResult) int64 { return run.peakRSS })
	median.maxPeakRSS = memSoakMaxInt64(runs, func(run memScenarioResult) int64 { return run.peakRSS })
	median.heapGrowth = medianGrowth
	return median
}

// memSoakRunScenarioOnce runs one workload and measures the memory it retains.
func memSoakRunScenarioOnce(t *testing.T, client *http.Client, baseURL string, scenario memScenario) memScenarioResult {
	t.Helper()

	before := memSoakSample()
	peak := &atomic.Int64{}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if rss := memSoakRSS(); rss > peak.Load() {
					peak.Store(rss)
				}
			}
		}
	}()

	var bytesRead int64
	for range scenario.requests {
		read, err := memSoakDoRequest(client, baseURL, scenario)
		if err != nil {
			close(stop)
			<-done
			t.Fatalf("scenario %s: request failed: %v", scenario.name, err)
		}
		bytesRead += read
		// A single large request can finish faster than the polling interval,
		// so the resident set is sampled right after every request too.
		if rss := memSoakRSS(); rss > peak.Load() {
			peak.Store(rss)
		}
	}

	close(stop)
	<-done

	after := memSoakSample()
	if rss := memSoakRSS(); rss > peak.Load() {
		peak.Store(rss)
	}

	result := memScenarioResult{
		scenario:   scenario,
		before:     before,
		after:      after,
		peakRSS:    peak.Load(),
		heapGrowth: int64(after.HeapAlloc) - int64(before.HeapAlloc),
		bytesRead:  bytesRead,
	}
	if result.peakRSS-result.before.RSSBytes > memSoakRSSGrowthLimit {
		t.Fatalf("scenario %s grew the resident set by %d bytes, want at most %d",
			scenario.name, result.peakRSS-result.before.RSSBytes, int64(memSoakRSSGrowthLimit))
	}
	return result
}

// memSoakWarmUp sends one non-stream and one streaming request so the lazy
// initialization of the executors, the translators and the transport pools is
// paid before the first measurement.
func memSoakWarmUp(t *testing.T, client *http.Client, baseURL string) {
	t.Helper()
	for _, scenario := range []memScenario{
		{name: "warmup", requests: 1},
		{name: "warmup", requests: 1, stream: true},
	} {
		if _, err := memSoakDoRequest(client, baseURL, scenario); err != nil {
			t.Fatalf("warm-up request failed: %v", err)
		}
	}
}

// memSoakMedianInt64 returns the median of the selected values.
func memSoakMedianInt64(runs []memScenarioResult, pick func(memScenarioResult) int64) int64 {
	values := make([]int64, 0, len(runs))
	for _, run := range runs {
		values = append(values, pick(run))
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	return values[len(values)/2]
}

// memSoakMaxInt64 returns the largest of the selected values.
func memSoakMaxInt64(runs []memScenarioResult, pick func(memScenarioResult) int64) int64 {
	var max int64
	for _, run := range runs {
		if value := pick(run); value > max {
			max = value
		}
	}
	return max
}

// memSoakDoRequest sends one request through the proxy and returns the number
// of body bytes read from the downstream response.
func memSoakDoRequest(client *http.Client, baseURL string, scenario memScenario) (int64, error) {
	payload := memSoakPayload(scenario)
	req, err := http.NewRequest(http.MethodPost, baseURL+"/v1/responses", bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer test-key")
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	read, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		return read, err
	}
	if resp.StatusCode != http.StatusOK {
		return read, fmt.Errorf("status = %d, want 200", resp.StatusCode)
	}
	return read, nil
}

// memSoakPayload builds the downstream request body for a scenario.
func memSoakPayload(scenario memScenario) []byte {
	content := "hello"
	if scenario.bodyBytes > 0 {
		content = strings.Repeat("x", scenario.bodyBytes)
	}
	payload := fmt.Sprintf(`{"model":%q,"input":[{"type":"message","role":"user","content":%q}]`, memSoakModel, content)
	if scenario.stream {
		payload += `,"stream":true`
	}
	return []byte(payload + "}")
}

// memSoakStartProxy starts the stub upstream and the proxy in front of it.
func memSoakStartProxy(t *testing.T) *httptest.Server {
	t.Helper()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The body is drained but never buffered: the stub must not contribute
		// its own copy of the payload, otherwise the measurement would report
		// the stub instead of the proxy.
		if _, errCopy := io.Copy(io.Discard, r.Body); errCopy != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"role\":\"assistant\"}}\n\n"))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_memsoak\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resp_memsoak","object":"response","status":"completed","model":"gpt-5-codex","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
	}))
	t.Cleanup(upstream.Close)

	tmpDir := t.TempDir()
	authDir := filepath.Join(tmpDir, "auth")
	if err := os.MkdirAll(authDir, 0o700); err != nil {
		t.Fatalf("create auth dir: %v", err)
	}

	cfg := &proxyconfig.Config{
		SDKConfig: sdkconfig.SDKConfig{
			APIKeys: []string{"test-key"},
		},
	}
	cfg.Port = 0
	cfg.AuthDir = authDir
	cfg.LoggingToFile = false
	cfg.UsageStatisticsEnabled = false

	authManager := cliproxyauth.NewManager(nil, nil, nil)
	accessManager := sdkaccess.NewManager()
	server := api.NewServer(cfg, authManager, accessManager, filepath.Join(tmpDir, "config.yaml"))

	authManager.RegisterExecutor(runtimeexecutor.NewCodexExecutor(cfg))

	credential := &cliproxyauth.Auth{
		ID:       "mem-soak-codex",
		Provider: "codex",
		Status:   cliproxyauth.StatusActive,
		Attributes: map[string]string{
			cliproxyauth.AttributeAPIKey: "mem-soak-key",
			"base_url":                   upstream.URL,
		},
	}
	if _, err := authManager.Register(context.Background(), credential); err != nil {
		t.Fatalf("register auth: %v", err)
	}
	registry.GetGlobalRegistry().RegisterClient(credential.ID, credential.Provider, []*registry.ModelInfo{{ID: memSoakModel}})
	t.Cleanup(func() {
		registry.GetGlobalRegistry().UnregisterClient(credential.ID)
	})

	proxy := httptest.NewServer(server.Handler())
	t.Cleanup(proxy.Close)
	return proxy
}

// memSoakSample reads the current memory state of the test process.
func memSoakSample() memSample {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return memSample{
		HeapAlloc:   stats.HeapAlloc,
		HeapInuse:   stats.HeapInuse,
		HeapObjects: stats.HeapObjects,
		Sys:         stats.Sys,
		RSSBytes:    memSoakRSS(),
	}
}

// memSoakRSS returns the resident set size of the test process in bytes, or -1
// when it cannot be determined.
func memSoakRSS() int64 {
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err != nil {
		return -1
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return -1
	}
	pages, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return -1
	}
	return pages * 1024
}

// memSoakBuildReport renders the measurements as a plain text table.
func memSoakBuildReport(results []memScenarioResult) string {
	var sb strings.Builder
	sb.WriteString("CLIProxyAPI memory soak report\n")
	sb.WriteString(fmt.Sprintf("go=%s os=%s arch=%s cpus=%d label=%s\n",
		runtime.Version(), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), memSoakLabel()))
	sb.WriteString("\n")
	sb.WriteString("The numbers are relative and measured on the same machine. The absolute values\n")
	sb.WriteString("depend on the host, the Go version and the load of the machine, so only the\n")
	sb.WriteString("difference between two runs of the same host is meaningful.\n")
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("%-12s %8s %8s %12s %12s %12s %12s %12s %12s %12s\n",
		"scenario", "requests", "repeats", "heap_before", "heap_after", "heap_inuse", "heap_sys", "rss_peak", "rss_max", "heap_growth"))
	for _, result := range results {
		sb.WriteString(fmt.Sprintf("%-12s %8d %8d %12d %12d %12d %12d %12d %12d %12d\n",
			result.scenario.name, result.scenario.requests, result.repeats,
			result.before.HeapAlloc, result.after.HeapAlloc, result.after.HeapInuse,
			result.after.Sys, result.peakRSS, result.maxPeakRSS, result.heapGrowth))
	}
	sb.WriteString("\n")
	for _, result := range results {
		sb.WriteString(fmt.Sprintf("scenario=%s response_bytes=%d rss_end=%d\n",
			result.scenario.name, result.bytesRead, result.after.RSSBytes))
	}
	return sb.String()
}

// memSoakLabel returns the label used in the report file name.
func memSoakLabel() string {
	if label := strings.TrimSpace(os.Getenv(memSoakLabelEnv)); label != "" {
		return label
	}
	return "baseline"
}

// memSoakBaselinePath returns the path of the baseline report the results are
// compared against.
func memSoakBaselinePath() string {
	if path := strings.TrimSpace(os.Getenv(memSoakBaselineEnv)); path != "" {
		return memSoakResolvePath(path)
	}
	return memSoakResolvePath(filepath.Join("bench-out", "mem-baseline.txt"))
}

// memSoakResolvePath resolves a relative path against the repository root,
// because go test runs the test binary with the package directory as the
// working directory.
func memSoakResolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(memSoakRepoRoot(), path)
}

// memSoakBuildDelta renders the difference between the results and a baseline
// report. It returns an empty string when the baseline is missing.
func memSoakBuildDelta(results []memScenarioResult, baselinePath string) string {
	content, err := os.ReadFile(baselinePath)
	if err != nil {
		return ""
	}
	type baselineRow struct {
		heapAfter int64
		heapInuse int64
		heapSys   int64
		rssPeak   int64
	}
	baseline := make(map[string]baselineRow)
	// Only the measurement table is read: the delta table of a previous report
	// has the same shape and would be mixed in otherwise.
	measurements := string(content)
	if marker := strings.Index(measurements, "delta vs baseline"); marker >= 0 {
		measurements = measurements[:marker]
	}
	for _, line := range strings.Split(measurements, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 || !isDigits(fields[1]) || !isDigits(fields[2]) || !isDigits(fields[9]) {
			continue
		}
		baseline[fields[0]] = baselineRow{
			heapAfter: parseInt64(fields[4]),
			heapInuse: parseInt64(fields[5]),
			heapSys:   parseInt64(fields[6]),
			rssPeak:   parseInt64(fields[7]),
		}
	}
	if len(baseline) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("delta vs baseline (%s)\n", baselinePath))
	sb.WriteString("The base columns are the samples of the comparison report, the cur columns are\n")
	sb.WriteString("the samples of this run. Both are taken after the workload, so the heap\n")
	sb.WriteString("columns show what the process retains. The heap_sys column is the heap the\n")
	sb.WriteString("Go runtime reserved, which follows the GC pacing and swings between two\n")
	sb.WriteString("runs of the same code, so compare it against a same code control run.\n")
	sb.WriteString(fmt.Sprintf("%-12s %14s %14s %8s %14s %14s %8s %12s %12s %8s\n",
		"scenario",
		"inuse_base", "inuse_cur", "delta",
		"heap_base", "heap_cur", "delta",
		"sys_base", "sys_cur", "delta"))
	for _, result := range results {
		row, ok := baseline[result.scenario.name]
		if !ok {
			continue
		}
		// A baseline report that misses a column parses as zero, and a zero
		// denominator would report an infinite delta.
		if row.heapInuse == 0 || row.heapAfter == 0 || row.heapSys == 0 || row.rssPeak == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("%-12s %14d %14d %7.1f%% %14d %14d %7.1f%% %12d %12d %7.1f%%\n",
			result.scenario.name,
			row.heapInuse, int64(result.after.HeapInuse),
			100*float64(int64(result.after.HeapInuse)-row.heapInuse)/float64(row.heapInuse),
			row.heapAfter, int64(result.after.HeapAlloc),
			100*float64(int64(result.after.HeapAlloc)-row.heapAfter)/float64(row.heapAfter),
			row.heapSys, int64(result.after.Sys),
			100*float64(int64(result.after.Sys)-row.heapSys)/float64(row.heapSys)))
	}

	sb.WriteString("\n")
	sb.WriteString(fmt.Sprintf("%-12s %16s %12s %9s\n", "scenario", "rss_base_peak", "rss_cur_peak", "delta"))
	for _, result := range results {
		row, ok := baseline[result.scenario.name]
		if !ok || row.rssPeak == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("%-12s %16d %12d %8.1f%%\n",
			result.scenario.name, row.rssPeak, result.peakRSS,
			100*float64(result.peakRSS-row.rssPeak)/float64(row.rssPeak)))
	}
	return sb.String()
}

// isDigits reports whether the string only holds digits.
func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// parseInt64 parses a decimal number and returns zero when it fails.
func parseInt64(value string) int64 {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

// memSoakOutputPath returns the path the report is written to. Relative paths
// are resolved against the repository root because go test runs the test binary
// with the package directory as the working directory.
func memSoakOutputPath() string {
	path := strings.TrimSpace(os.Getenv(memSoakOutEnv))
	if path == "" {
		path = filepath.Join("bench-out", "mem-"+memSoakLabel()+".txt")
	}
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(memSoakRepoRoot(), path)
}

// memSoakRepoRoot returns the directory that holds go.mod, starting from the
// working directory of the test binary.
func memSoakRepoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, errStat := os.Stat(filepath.Join(dir, "go.mod")); errStat == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}
