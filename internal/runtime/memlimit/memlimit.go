// Package memlimit derives a soft heap limit from the container memory limit.
//
// The Go runtime has no notion of a container memory ceiling: it sizes the
// heap from the host memory and only reacts when the kernel OOM killer fires.
// On a small host the proxy is then killed instead of being throttled. When a
// cgroup memory limit is visible, this package applies a soft limit slightly
// below it so the garbage collector keeps the resident set under the ceiling.
//
// GOMEMLIMIT always wins: an operator who sets it keeps full control.
package memlimit

import (
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"

	log "github.com/sirupsen/logrus"
)

// appliedLimit records the soft heap limit installed by the last apply call
// so the admission controller can derive a concurrency cap from it.
var appliedLimit atomic.Int64

// Limit returns the soft heap limit in bytes installed by Apply.
// It returns 0 when no limit is active (GOMEMLIMIT=off, or no container limit visible).
func Limit() int64 {
	return appliedLimit.Load()
}

const (
	// cgroupV2MaxFile holds the memory ceiling of a cgroup v2 group.
	cgroupV2MaxFile = "/sys/fs/cgroup/memory.max"
	// cgroupV1LimitFile holds the memory ceiling of a cgroup v1 memory group.
	cgroupV1LimitFile = "/sys/fs/cgroup/memory/memory.limit_in_bytes"
	// procSelfCgroup lists the cgroups the process belongs to.
	procSelfCgroup = "/proc/self/cgroup"

	// cgroupV2Root is the mount point of the unified cgroup v2 hierarchy.
	cgroupV2Root = "/sys/fs/cgroup"
	// cgroupV1MemoryRoot is the mount point of the cgroup v1 memory hierarchy.
	cgroupV1MemoryRoot = "/sys/fs/cgroup/memory"

	// unlimitedV1Limit is the value cgroup v1 uses to report "no limit".
	// The kernel sentinel is 9223372036854771712, slightly below the int64
	// maximum, so anything at or above 2^62 is treated as unlimited.
	unlimitedV1Limit = int64(1) << 62

	// limitRatio is the fraction of the container limit used as the soft
	// heap limit. The remainder covers the non-heap resident set (stacks,
	// runtime metadata, mmap'd files) that the GC cannot reclaim.
	limitRatio = 9
	limitDiv   = 10
)

// environment holds the injectable dependencies of apply so the detection
// logic can be tested without depending on the host.
type environment struct {
	getenv         func(string) string
	readFile       func(string) ([]byte, error)
	setMemoryLimit func(int64)
}

// Apply derives the container memory limit and installs a soft heap limit
// below it. It returns the applied limit and true when a limit was set, and
// (0, false) when GOMEMLIMIT is set or no container limit is visible.
func Apply() (int64, bool) {
	return apply(environment{
		getenv:         os.Getenv,
		readFile:       os.ReadFile,
		setMemoryLimit: func(limit int64) { debug.SetMemoryLimit(limit) },
	})
}

// apply performs the limit detection and installation.
func apply(env environment) (int64, bool) {
	if env.getenv == nil {
		env.getenv = os.Getenv
	}
	if env.readFile == nil {
		env.readFile = os.ReadFile
	}
	if env.setMemoryLimit == nil {
		env.setMemoryLimit = func(limit int64) { debug.SetMemoryLimit(limit) }
	}

	if raw := strings.TrimSpace(env.getenv("GOMEMLIMIT")); raw != "" {
		if raw == "off" {
			appliedLimit.Store(0)
			return 0, false
		}
		if value, err := strconv.ParseInt(raw, 10, 64); err == nil && value > 0 {
			// The Go runtime already applied the operator limit; do not override it.
			appliedLimit.Store(value)
			return 0, false
		}
		log.Warnf("ignoring invalid GOMEMLIMIT %q; deriving the soft memory limit from the container limit", raw)
	}

	limit, ok := detectLimitFrom(env.readFile, cgroupRelativePath(env.readFile))
	if !ok || limit <= 0 {
		appliedLimit.Store(0)
		return 0, false
	}

	// The division comes first so the multiplication cannot overflow.
	soft := limit / limitDiv * limitRatio
	if soft <= 0 {
		appliedLimit.Store(0)
		return 0, false
	}
	appliedLimit.Store(soft)
	env.setMemoryLimit(soft)
	log.Infof("soft memory limit set to %d (cgroup limit %d)", soft, limit)
	return soft, true
}

// detectLimitFrom reads the cgroup memory ceiling using the supplied reader.
// cgroupPath is the cgroup relative path of the process (for example
// "/docker/abc123"), empty for the root cgroup.
//
// It returns the limit in bytes and true when a finite limit was found.
func detectLimitFrom(readFile func(string) ([]byte, error), cgroupPath string) (int64, bool) {
	if readFile == nil {
		return 0, false
	}

	// cgroup v2 first: the unified hierarchy is what modern containers use.
	if value, ok := readCgroupLimit(readFile, joinCgroupPath(cgroupV2Root, cgroupPath)+"/memory.max"); ok {
		return value, true
	}
	if value, ok := readCgroupLimit(readFile, joinCgroupPath(cgroupV1MemoryRoot, cgroupPath)+"/memory.limit_in_bytes"); ok {
		return value, true
	}
	return 0, false
}

// readCgroupLimit parses one cgroup limit file. cgroup v2 writes the literal
// "max" for an unlimited group, cgroup v1 writes a huge sentinel instead.
func readCgroupLimit(readFile func(string) ([]byte, error), path string) (int64, bool) {
	raw, err := readFile(path)
	if err != nil {
		return 0, false
	}
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "max" {
		return 0, false
	}
	limit, err := strconv.ParseInt(value, 10, 64)
	if err != nil || limit <= 0 {
		return 0, false
	}
	if limit >= unlimitedV1Limit {
		return 0, false
	}
	return limit, true
}

// cgroupRelativePath returns the cgroup path of the current process relative
// to the hierarchy root. It returns an empty string when the path cannot be
// determined or when the process sits in the root cgroup.
func cgroupRelativePath(readFile func(string) ([]byte, error)) string {
	raw, err := readFile(procSelfCgroup)
	if err != nil {
		return ""
	}
	var v1Path string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue
		}
		controllers := parts[1]
		path := parts[2]
		if controllers == "" {
			// Unified hierarchy (cgroup v2).
			return path
		}
		for _, controller := range strings.Split(controllers, ",") {
			if controller == "memory" {
				v1Path = path
			}
		}
	}
	return v1Path
}

// joinCgroupPath appends a cgroup relative path to a hierarchy mount point.
func joinCgroupPath(root string, rel string) string {
	rel = strings.TrimSpace(rel)
	if rel == "" || rel == "/" {
		return root
	}
	return root + "/" + strings.Trim(rel, "/")
}
