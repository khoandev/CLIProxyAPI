package memlimit

import (
	"fmt"
	"os"
	"strings"
	"testing"

	log "github.com/sirupsen/logrus"
)

func fakeReader(files map[string]string) func(string) ([]byte, error) {
	return func(path string) ([]byte, error) {
		content, ok := files[path]
		if !ok {
			return nil, fmt.Errorf("no such file: %s", path)
		}
		return []byte(content), nil
	}
}

func TestDetectLimitFromCgroupV2Value(t *testing.T) {
	readFile := fakeReader(map[string]string{
		"/sys/fs/cgroup/memory.max": "536870912\n",
	})
	limit, ok := detectLimitFrom(readFile, "")
	if !ok {
		t.Fatalf("expected a limit to be detected")
	}
	if limit != 536870912 {
		t.Fatalf("expected 536870912, got %d", limit)
	}
}

func TestDetectLimitFromCgroupV2Max(t *testing.T) {
	readFile := fakeReader(map[string]string{
		"/sys/fs/cgroup/memory.max": "max\n",
	})
	if _, ok := detectLimitFrom(readFile, ""); ok {
		t.Fatalf("expected no limit for the cgroup v2 max sentinel")
	}
}

func TestDetectLimitFromCgroupV2Subgroup(t *testing.T) {
	readFile := fakeReader(map[string]string{
		"/sys/fs/cgroup/docker/abc/memory.max": "268435456",
	})
	limit, ok := detectLimitFrom(readFile, "/docker/abc")
	if !ok || limit != 268435456 {
		t.Fatalf("expected 268435456 from the subgroup limit, got %d (ok=%v)", limit, ok)
	}
}

func TestDetectLimitFromCgroupV1Sentinel(t *testing.T) {
	readFile := fakeReader(map[string]string{
		"/sys/fs/cgroup/memory/memory.limit_in_bytes": "9223372036854771712",
	})
	if _, ok := detectLimitFrom(readFile, ""); ok {
		t.Fatalf("expected no limit for the cgroup v1 unlimited sentinel")
	}
}

func TestDetectLimitFromCgroupV1Value(t *testing.T) {
	readFile := fakeReader(map[string]string{
		"/sys/fs/cgroup/memory/memory.limit_in_bytes": "1073741824",
	})
	limit, ok := detectLimitFrom(readFile, "")
	if !ok || limit != 1073741824 {
		t.Fatalf("expected 1073741824, got %d (ok=%v)", limit, ok)
	}
}

func TestDetectLimitFromMissingFiles(t *testing.T) {
	readFile := fakeReader(map[string]string{})
	if _, ok := detectLimitFrom(readFile, ""); ok {
		t.Fatalf("expected no limit when no cgroup file exists")
	}
}

func TestApplyGOMEMLIMITWins(t *testing.T) {
	applied := int64(-1)
	limit, ok := apply(environment{
		getenv: func(key string) string {
			if key == "GOMEMLIMIT" {
				return "268435456"
			}
			return ""
		},
		readFile: fakeReader(map[string]string{
			"/sys/fs/cgroup/memory.max": "1073741824",
		}),
		setMemoryLimit: func(value int64) { applied = value },
	})
	if ok || limit != 0 {
		t.Fatalf("expected GOMEMLIMIT to win, got limit=%d ok=%v", limit, ok)
	}
	if applied != -1 {
		t.Fatalf("expected no memory limit to be set, got %d", applied)
	}
	if got := Limit(); got != 268435456 {
		t.Fatalf("expected Limit() to report the GOMEMLIMIT value 268435456, got %d", got)
	}
}

func TestApplyGOMEMLIMITOff(t *testing.T) {
	applied := int64(-1)
	limit, ok := apply(environment{
		getenv: func(key string) string {
			if key == "GOMEMLIMIT" {
				return "off"
			}
			return ""
		},
		readFile: fakeReader(map[string]string{
			"/sys/fs/cgroup/memory.max": "1073741824",
		}),
		setMemoryLimit: func(value int64) { applied = value },
	})
	if ok || limit != 0 {
		t.Fatalf("expected GOMEMLIMIT=off to disable the limit, got limit=%d ok=%v", limit, ok)
	}
	if applied != -1 {
		t.Fatalf("expected no memory limit to be set, got %d", applied)
	}
	if got := Limit(); got != 0 {
		t.Fatalf("expected Limit() to report 0 when GOMEMLIMIT=off, got %d", got)
	}
}

func TestApplyUsesCgroupLimit(t *testing.T) {
	applied := int64(-1)
	var logged strings.Builder
	previousOut := log.StandardLogger().Out
	previousLevel := log.GetLevel()
	log.SetOutput(&logged)
	log.SetLevel(log.InfoLevel)
	t.Cleanup(func() {
		log.SetOutput(previousOut)
		log.SetLevel(previousLevel)
	})

	limit, ok := apply(environment{
		getenv: func(string) string { return "" },
		readFile: fakeReader(map[string]string{
			"/sys/fs/cgroup/memory.max": "1000000000",
		}),
		setMemoryLimit: func(value int64) { applied = value },
	})
	if !ok {
		t.Fatalf("expected the cgroup limit to be applied")
	}
	if limit != 900000000 {
		t.Fatalf("expected 900000000, got %d", limit)
	}
	if applied != 900000000 {
		t.Fatalf("expected the runtime limit to be 900000000, got %d", applied)
	}
	if !strings.Contains(logged.String(), "soft memory limit set to 900000000") {
		t.Fatalf("expected the soft memory limit log line, got %q", logged.String())
	}
}

func TestApplyWithoutContainerLimit(t *testing.T) {
	applied := int64(-1)
	limit, ok := apply(environment{
		getenv:         func(string) string { return "" },
		readFile:       fakeReader(map[string]string{}),
		setMemoryLimit: func(value int64) { applied = value },
	})
	if ok || limit != 0 {
		t.Fatalf("expected no limit without a container limit, got limit=%d ok=%v", limit, ok)
	}
	if applied != -1 {
		t.Fatalf("expected no memory limit to be set, got %d", applied)
	}
}

func TestCgroupRelativePath(t *testing.T) {
	readFile := fakeReader(map[string]string{
		"/proc/self/cgroup": "0::/docker/abc123\n",
	})
	if got := cgroupRelativePath(readFile); got != "/docker/abc123" {
		t.Fatalf("expected /docker/abc123, got %q", got)
	}
}

func TestCgroupRelativePathV1(t *testing.T) {
	readFile := fakeReader(map[string]string{
		"/proc/self/cgroup": "1:name=systemd:/init.scope\n5:memory:/user.slice\n",
	})
	if got := cgroupRelativePath(readFile); got != "/user.slice" {
		t.Fatalf("expected /user.slice, got %q", got)
	}
}

func TestJoinCgroupPath(t *testing.T) {
	cases := map[string]string{
		"":           "/sys/fs/cgroup",
		"/":          "/sys/fs/cgroup",
		"/docker/x":  "/sys/fs/cgroup/docker/x",
		"docker/x":   "/sys/fs/cgroup/docker/x",
		"/docker/x/": "/sys/fs/cgroup/docker/x",
	}
	for rel, expected := range cases {
		if got := joinCgroupPath(cgroupV2Root, rel); got != expected {
			t.Fatalf("joinCgroupPath(%q) = %q, want %q", rel, got, expected)
		}
	}
}

func TestDetectLimitFromHostFilesystem(t *testing.T) {
	// The host may expose a cgroup limit or not. The detection must work
	// against the real files either way.
	limit, ok := detectLimitFrom(os.ReadFile, cgroupRelativePath(os.ReadFile))
	if ok && limit <= 0 {
		t.Fatalf("detected limit = %d, want a positive limit", limit)
	}
}

func TestApplyRealEnvironmentDoesNotPanic(t *testing.T) {
	// The host may or may not expose a cgroup limit; the call must never panic
	// and must never lower an operator provided GOMEMLIMIT.
	t.Setenv("GOMEMLIMIT", "268435456")
	if _, ok := Apply(); ok {
		t.Fatalf("expected Apply to respect GOMEMLIMIT on this host")
	}
	_ = os.Getpid()
}
