package cgroup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DockerHostRoot finds the guest root for a Docker process in a shared cgroup v2
// namespace. The caller must also verify host cgroup mode with the Docker daemon.
func DockerHostRoot(containerID string) (string, error) {
	const root = "/sys/fs/cgroup"
	if len(containerID) != 64 {
		return "", errors.New("full Docker container identity is unavailable")
	}
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", err
	}
	var processPath string
	for line := range strings.SplitSeq(string(data), "\n") {
		if path, ok := strings.CutPrefix(line, "0::"); ok {
			processPath = path
			break
		}
	}
	if !filepath.IsAbs(processPath) || processPath == "/" || filepath.Clean(processPath) != processPath {
		return "", errors.New("process is not in a descendant cgroup v2 path")
	}
	matched := false
	parts := strings.Split(processPath, "/")
	for i, part := range parts {
		if part == "docker-"+containerID+".scope" ||
			(part == containerID && i > 0 && parts[i-1] == "docker") {
			matched = true
			break
		}
	}
	if !matched {
		return "", errors.New("process cgroup does not match the Docker container identity")
	}

	mounts, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return "", err
	}
	mounted := false
	for line := range strings.SplitSeq(string(mounts), "\n") {
		before, after, ok := strings.Cut(line, " - ")
		fields := strings.Fields(before)
		// These fixed paths contain no mountinfo escape sequences.
		if ok && len(fields) >= 6 && fields[3] == "/" && fields[4] == root && strings.HasPrefix(after, "cgroup2 ") {
			mounted = true
			break
		}
	}
	if !mounted {
		return "", errors.New("cgroup v2 namespace root is not mounted")
	}
	if _, err := os.Stat(filepath.Join(root, processPath, "cgroup.procs")); err != nil {
		return "", fmt.Errorf("resolve process beneath cgroup root: %w", err)
	}
	// The physical hierarchy root has no memory.current. A guest root does.
	if _, err := MemoryUsage(root); err != nil {
		return "", err
	}
	return root, nil
}

// MemoryUsage reads cgroup v2 memory usage excluding file cache, in bytes.
func MemoryUsage(root string) (uint64, error) {
	data, err := os.ReadFile(filepath.Join(root, "memory.current"))
	if err != nil {
		return 0, err
	}
	current, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse cgroup memory.current: %w", err)
	}
	cache, err := readMemoryStatValueInternal(filepath.Join(root, "memory.stat"), "file")
	if err != nil {
		return 0, err
	}
	if cache < 0 {
		return 0, errors.New("negative cgroup memory.stat file entry")
	}
	return current - min(current, uint64(cache)), nil
}
