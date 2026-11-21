package cgroup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	paths, err := readProcFile("/proc/self/cgroup", parseProcCgroup)
	if err != nil {
		return "", err
	}
	processPath := paths.Unified
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

	mounts, err := readProcFile("/proc/self/mountinfo", parseCgroupMounts)
	if err != nil {
		return "", err
	}
	if !slices.ContainsFunc(mounts, func(m cgroupMount) bool { return m.V2 && m.Root == "/" && m.Point == root }) {
		return "", errors.New("cgroup v2 namespace root is not mounted")
	}
	_, err = os.Stat(filepath.Join(root, processPath, "cgroup.procs"))
	if err != nil {
		return "", fmt.Errorf("resolve process beneath cgroup root: %w", err)
	}
	// The physical hierarchy root has no memory.current. A guest root does.
	_, err = MemoryUsage(root)
	if err != nil {
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
	cache, err := readMemoryStatValue(filepath.Join(root, "memory.stat"), "file")
	if err != nil {
		return 0, err
	}
	if cache < 0 {
		return 0, errors.New("negative cgroup memory.stat file entry")
	}
	return current - min(current, uint64(cache)), nil
}
