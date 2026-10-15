// Package cgroup probes Linux control-group resource limits and container
// identity for the current process: cgroup v1/v2 CPU and memory limit
// detection, Docker-vs-LXC distinction, container-ID lookup, a TTL cache for
// limit detection, and a ZFS ARC helper for memory accounting.
//
// All probes degrade gracefully on non-Linux hosts: the cgroup and procfs
// files simply fail to open and the functions report "not in a cgroup" /
// zero values.
package cgroup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/shirou/gopsutil/v4/host"
)

const unrestricted = 9223372036854771712

// cgroupV1MemoryRoot is the mount point of the cgroup v1 memory controller.
const cgroupV1MemoryRoot = "/sys/fs/cgroup/memory"

// Limits holds the detected cgroup resource limits. Fields are -1 when the
// corresponding limit is absent or unrestricted.
type Limits struct {
	MemoryLimit int64
	MemoryUsage int64
	CPUQuota    int64
	CPUPeriod   int64
	CPUCount    int
}

// Container ID detection patterns for cgroup v1 and v2
var (
	// cgroup v1: "12:memory:/docker/abc123..." or "12:memory:/kubepods/.../docker/abc123..."
	cgroupV1ContainerPattern = regexp.MustCompile(`/docker/([a-f0-9]{64})`)
	// cgroup v2: "0::/system.slice/docker-abc123.scope"
	cgroupV2ContainerPattern = regexp.MustCompile(`docker-([a-f0-9]{64})\.scope`)
)

// DetectLimits detects the cgroup (v1 or v2) CPU and memory limits applied to
// the current process. It returns an error when the process is not running
// under a cgroup with explicit limits.
func DetectLimits() (*Limits, error) {
	info, err := host.Info()
	guest := err == nil && info.VirtualizationSystem != "" && strings.EqualFold(info.VirtualizationRole, "guest")

	if _, statErr := os.Stat("/sys/fs/cgroup/cgroup.controllers"); statErr == nil {
		limits := detectCgroupV2Limits()
		if !guest && !isFiniteLimit(limits.MemoryLimit) && limits.CPUCount < 0 {
			return nil, errors.New("not running in a cgroup")
		}
		return limits, nil
	}

	cgroupPath, err := func() (string, error) {
		file, openErr := os.Open("/proc/self/cgroup")
		if openErr != nil {
			return "", openErr
		}
		defer func() { _ = file.Close() }()

		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			parts := strings.SplitN(scanner.Text(), ":", 3)
			if len(parts) == 3 && (strings.Contains(parts[1], "memory") || strings.Contains(parts[1], "cpu")) {
				return strings.TrimPrefix(parts[2], "/"), nil
			}
		}

		if scanErr := scanner.Err(); scanErr != nil {
			return "", fmt.Errorf("error scanning cgroup file: %w", scanErr)
		}

		return "", errors.New("cgroup path not found")
	}()
	if err != nil {
		if !guest {
			return nil, errors.New("not running in a cgroup")
		}
		return &Limits{
			MemoryLimit: -1,
			MemoryUsage: -1,
			CPUQuota:    -1,
			CPUPeriod:   -1,
			CPUCount:    -1,
		}, err
	}

	quota := readCgroupV1CPUControllerInt64(cgroupPath, "cpu.cfs_quota_us")
	period := readCgroupV1CPUControllerInt64(cgroupPath, "cpu.cfs_period_us")

	limits := &Limits{
		MemoryLimit: func() int64 {
			memLimit, limitErr := readCgroupV1Int64(filepath.Join(cgroupV1MemoryRoot, cgroupPath, "memory.limit_in_bytes"))
			if limitErr != nil || memLimit >= unrestricted {
				return -1
			}
			return memLimit
		}(),
		MemoryUsage: func() int64 {
			memUsage, usageErr := readCgroupV1Int64(filepath.Join(cgroupV1MemoryRoot, cgroupPath, "memory.usage_in_bytes"))
			if usageErr != nil {
				return -1
			}
			// memory.usage_in_bytes includes reclaimable page cache; subtract
			// total_inactive_file (from memory.stat) to match `docker stats`.
			inactiveFile, statErr := readMemoryStatValue(filepath.Join(cgroupV1MemoryRoot, cgroupPath, "memory.stat"), "total_inactive_file")
			if statErr != nil {
				return memUsage
			}
			return max(memUsage-inactiveFile, 0)
		}(),
		CPUQuota:  quota,
		CPUPeriod: period,
		CPUCount: func() int {
			if quota <= 0 || period <= 0 {
				return -1
			}
			return max(int((quota+period-1)/period), 1)
		}(),
	}
	if !guest && (cgroupPath == "" || !isFiniteLimit(limits.MemoryLimit) && limits.CPUCount < 0) {
		return nil, errors.New("not running in a cgroup")
	}
	return limits, nil
}

// IsDockerContainer reports whether the current process is running inside a
// Docker container (as opposed to an LXC container, a VM, or bare metal).
//
// The distinction matters for host resource stats: in Docker the cgroup
// limits (--cpus / --memory) are artificial constraints set by the operator
// and should NOT be used as the host resource totals shown in a dashboard.
// In LXC, by contrast, the cgroup limits represent the real hardware budget
// assigned to the container — gopsutil reads the host's /proc values which are
// higher, so the cgroup limits must be applied to show correct figures.
//
// Detection: Docker always creates /.dockerenv inside every container it
// starts.  LXC does not.  We fall back to a /proc/self/cgroup pattern check
// as a secondary signal.
func IsDockerContainer() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return false
	}
	return cgroupV1ContainerPattern.Match(data) || cgroupV2ContainerPattern.Match(data)
}

// CurrentContainerID detects the current container ID using multiple detection methods.
// It tries cgroup, mountinfo, and hostname in that order.
func CurrentContainerID() (string, error) {
	// Try cgroup first (works on cgroup v1 and cgroupns=host mode)
	if id, err := func() (string, error) {
		// tries to extract container ID from /proc/self/cgroup
		data, err := os.ReadFile("/proc/self/cgroup")
		if err != nil {
			return "", err
		}

		// Try cgroup v1 pattern
		if matches := cgroupV1ContainerPattern.FindStringSubmatch(string(data)); len(matches) >= 2 {
			return matches[1], nil
		}

		// Try cgroup v2 pattern
		if matches := cgroupV2ContainerPattern.FindStringSubmatch(string(data)); len(matches) >= 2 {
			return matches[1], nil
		}

		return "", errors.New("no container ID found in cgroup")
	}(); err == nil {
		slog.DebugContext(context.Background(), "CurrentContainerID: found via cgroup", "containerId", id)
		return id, nil
	}

	// Try mountinfo (works when cgroup namespace is private)
	if id, err := func() (string, error) {
		// tries to extract container ID from /proc/self/mountinfo
		// This works even when cgroup namespace is private (cgroupns=private)
		data, err := os.ReadFile("/proc/self/mountinfo")
		if err != nil {
			return "", err
		}

		for line := range strings.SplitSeq(string(data), "\n") {
			// Look for Docker container paths in mountinfo
			_, after, ok := strings.Cut(line, "/docker/containers/")
			if id, _, _ := strings.Cut(after, "/"); ok && len(id) >= 12 {
				return id[:min(len(id), 64)], nil
			}
		}

		return "", errors.New("no container ID found in mountinfo")
	}(); err == nil {
		slog.DebugContext(context.Background(), "CurrentContainerID: found via mountinfo", "containerId", id)
		return id, nil
	}

	// Try hostname (Docker often sets hostname to container ID)
	if id, err := func() (string, error) {
		// tries to use hostname as container ID
		// Docker often sets the hostname to the container ID (12 or 64 hex chars)
		hostname, err := os.Hostname()
		if err != nil {
			return "", err
		}

		// Docker often sets hostname to the short container ID (12 chars) or full ID (64 chars)
		if len(hostname) == 12 || len(hostname) == 64 {
			// Verify it looks like a hex string
			for _, c := range hostname {
				if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
					return "", errors.New("hostname doesn't match container ID pattern")
				}
			}
			return hostname, nil
		}

		return "", errors.New("hostname doesn't match expected container ID length")
	}(); err == nil {
		slog.DebugContext(context.Background(), "CurrentContainerID: found via hostname", "containerId", id)
		return id, nil
	}

	return "", errors.New("no container ID found via cgroup, mountinfo, or hostname")
}

// ZFSARCReclaimable returns the number of bytes of ZFS ARC (Adaptive
// Replacement Cache) that the kernel counts as used memory but that can be
// reclaimed under memory pressure.
//
// The kernel accounts ZFS ARC as used and excludes it from /proc/meminfo's
// MemAvailable, so gopsutil's Used (= Total - MemAvailable) counts the whole
// ARC as used. Tools like btop/htop instead treat ARC as reclaimable cache. We
// subtract the reclaimable portion so dashboards do not over-report usage
// on ZFS hosts.
//
// The arcstats file only exists when the ZFS kernel module is loaded, so the
// open fails cheaply on non-ZFS systems (including macOS dev machines) and this
// returns 0 there.
func ZFSARCReclaimable() uint64 {
	f, err := os.Open("/proc/spl/kstat/zfs/arcstats")
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	return parseARCStats(f)
}

func detectCgroupV2Limits() *Limits {
	cpuMax, err := os.ReadFile("/sys/fs/cgroup/cpu.max")
	parts := strings.Fields(string(cpuMax))
	quota := func() int64 {
		if err != nil || len(parts) < 2 || parts[0] == "max" {
			return -1
		}
		q, quotaErr := strconv.ParseInt(parts[0], 10, 64)
		if quotaErr != nil {
			return -1
		}
		return q
	}()
	period := func() int64 {
		if err != nil || len(parts) < 2 {
			return -1
		}
		p, periodErr := strconv.ParseInt(parts[1], 10, 64)
		if periodErr != nil {
			return -1
		}
		return p
	}()

	return &Limits{
		MemoryLimit: func() int64 {
			memLimit, limitErr := readCgroupV2Int64("/sys/fs/cgroup/memory.max")
			if limitErr != nil || memLimit == unrestricted {
				return -1
			}
			return memLimit
		}(),
		MemoryUsage: func() int64 {
			memUsage, usageErr := readCgroupV2Int64("/sys/fs/cgroup/memory.current")
			if usageErr != nil {
				return -1
			}
			// memory.current includes reclaimable page cache; subtract inactive_file
			// to match what `docker stats` reports as usage.
			inactiveFile, statErr := readMemoryStatValue("/sys/fs/cgroup/memory.stat", "inactive_file")
			if statErr != nil {
				return memUsage
			}
			return max(memUsage-inactiveFile, 0)
		}(),
		CPUQuota:  quota,
		CPUPeriod: period,
		CPUCount: func() int {
			if quota <= 0 || period <= 0 {
				return -1
			}
			return max(int((quota+period-1)/period), 1)
		}(),
	}
}

func readCgroupV1Int64(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	value, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, err
	}

	return value, nil
}

// readMemoryStatValue reads a single "key value" line from a cgroup memory.stat
// file and returns the value for the given key.
func readMemoryStatValue(path, key string) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || fields[0] != key {
			continue
		}
		if len(fields) != 2 {
			return 0, fmt.Errorf("invalid %q entry in %s", key, path)
		}
		return strconv.ParseInt(fields[1], 10, 64)
	}
	err = scanner.Err()
	if err != nil {
		return 0, err
	}

	return 0, fmt.Errorf("key %q not found in %s", key, path)
}

func readCgroupV2Int64(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}

	content := strings.TrimSpace(string(data))
	if content == "max" {
		return unrestricted, nil
	}

	value, err := strconv.ParseInt(content, 10, 64)
	if err != nil {
		return 0, err
	}

	return value, nil
}

func isFiniteLimit(value int64) bool {
	return value > 0 && value < unrestricted
}

func readCgroupV1CPUControllerInt64(cgroupPath, filename string) int64 {
	for _, base := range []string{
		"/sys/fs/cgroup/cpu,cpuacct",
		"/sys/fs/cgroup/cpu",
		"/sys/fs/cgroup/cpuacct",
	} {
		if value, err := readCgroupV1Int64(filepath.Join(base, cgroupPath, filename)); err == nil {
			return value
		}
	}

	return -1
}

// parseARCStats parses the kstat-format arcstats content and returns the
// reclaimable ARC size in bytes: max(size-c_min, 0). The ARC cannot shrink
// below c_min, so only the portion above c_min is genuinely reclaimable — a
// slightly more conservative choice than btop's full-size treatment, which is
// the right call for a "used" metric.
//
// Each kstat row is "name type data"; the value is the third column.
func parseARCStats(r io.Reader) uint64 {
	var size, cMin uint64
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		switch fields[0] {
		case "size":
			if v, err := strconv.ParseUint(fields[2], 10, 64); err == nil {
				size = v
			}
		case "c_min":
			if v, err := strconv.ParseUint(fields[2], 10, 64); err == nil {
				cMin = v
			}
		}
	}
	if err := scanner.Err(); err != nil {
		slog.ErrorContext(context.Background(), "failed to parse ARC stats", "error", err)
	}
	if size <= cMin {
		return 0
	}
	return size - cMin
}
