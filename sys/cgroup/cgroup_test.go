package cgroup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadMemoryStatValue(t *testing.T) {
	// Trimmed cgroup memory.stat: flat "key value" lines.
	const memoryStat = `anon 1048576
file 2097152
inactive_file 524288
active_file 1572864
total_inactive_file 786432
`
	path := filepath.Join(t.TempDir(), "memory.stat")
	require.NoError(t, os.WriteFile(path, []byte(memoryStat), 0o600))

	// Exact key match: inactive_file must not be shadowed by total_inactive_file.
	value, err := readMemoryStatValue(path, "inactive_file")
	require.NoError(t, err)
	require.Equal(t, int64(524288), value)

	value, err = readMemoryStatValue(path, "total_inactive_file")
	require.NoError(t, err)
	require.Equal(t, int64(786432), value)
}

func TestReadMemoryStatValue_MissingKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "memory.stat")
	require.NoError(t, os.WriteFile(path, []byte("anon 1048576\n"), 0o600))

	_, err := readMemoryStatValue(path, "inactive_file")
	require.Error(t, err)
}

func TestParseARCStats(t *testing.T) {
	// Abbreviated kstat dump: rows are "name type data".
	const arcstats = `13 1 0x01 98 26656 5560875779 158279494432
name                            type data
hits                            4    1234567
misses                          4    89012
c_min                           4    1073741824
c_max                           4    8589934592
size                            4    7516192768
`
	// size (7 GiB) - c_min (1 GiB) = 6 GiB reclaimable.
	require.Equal(t, uint64(6442450944), parseARCStats(strings.NewReader(arcstats)))
}

func TestParseARCStats_ClampsAndHandlesMissing(t *testing.T) {
	// No arcstats content (non-ZFS host): nothing reclaimable.
	require.Equal(t, uint64(0), parseARCStats(strings.NewReader("")))

	// ARC already at its floor (size <= c_min): clamped to 0, never underflows.
	const atFloor = `size  4  1073741824
c_min 4  1073741824
`
	require.Equal(t, uint64(0), parseARCStats(strings.NewReader(atFloor)))
}

func TestParseProcCgroup(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    procCgroup
		wantErr bool
	}{
		{
			name: "systemd ordering puts cpuset before cpu",
			input: `12:pids:/user.slice/user-1000.slice
11:cpuset:/
7:cpu,cpuacct:/user.slice
5:memory:/user.slice/user-1000.slice/session-1.scope
1:name=systemd:/user.slice/user-1000.slice/session-1.scope
`,
			want: procCgroup{
				Memory: "/user.slice/user-1000.slice/session-1.scope",
				CPU:    "/user.slice",
			},
		},
		{
			name: "separate cpu and cpuacct hierarchies",
			input: `4:cpuacct:/acct
3:cpu:/quota
2:memory:/docker/abc
`,
			want: procCgroup{
				Memory: "/docker/abc",
				CPU:    "/quota",
			},
		},
		{
			name:  "cpuacct only",
			input: "3:cpuacct:/acct\n",
			want:  procCgroup{CPU: "/acct"},
		},
		{
			name:  "controller root",
			input: "5:memory:/\n4:cpu,cpuacct:/\n",
			want: procCgroup{
				Memory: "/",
				CPU:    "/",
			},
		},
		{
			name:  "cgroup v2 only",
			input: "0::/system.slice/docker-abc.scope\n",
			want:  procCgroup{Unified: "/system.slice/docker-abc.scope"},
		},
		{
			name:  "hybrid hierarchy",
			input: "5:memory:/docker/abc\n0::/docker/abc\n",
			want: procCgroup{
				Memory:  "/docker/abc",
				Unified: "/docker/abc",
			},
		},
		{
			name:    "no cpu or memory controller",
			input:   "11:cpuset:/\n10:memoryx:/x\n",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProcCgroup(strings.NewReader(tt.input))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseCgroupMounts(t *testing.T) {
	const mountinfo = `22 1 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw
30 22 0:26 / /sys/fs/cgroup ro,nosuid shared:9 - tmpfs tmpfs ro,mode=755
31 30 0:27 /docker/abc /sys/fs/cgroup/memory rw,nosuid master:10 - cgroup cgroup rw,memory
32 30 0:28 /docker/abc /sys/fs/cgroup/cpu,cpuacct rw,nosuid master:11 shared:12 - cgroup cgroup rw,cpu,cpuacct
33 22 0:29 / /sys/fs/cgroup/unified rw,nosuid - cgroup2 cgroup2 rw,nsdelegate
34 22 0:30 / /broken rw - cgroup
`
	got, err := parseCgroupMounts(strings.NewReader(mountinfo))
	require.NoError(t, err)
	require.Equal(t, []cgroupMount{
		{
			Root:        "/docker/abc",
			Point:       "/sys/fs/cgroup/memory",
			Controllers: []string{"rw", "memory"},
		},
		{
			Root:        "/docker/abc",
			Point:       "/sys/fs/cgroup/cpu,cpuacct",
			Controllers: []string{"rw", "cpu", "cpuacct"},
		},
		{
			Root:  "/",
			Point: "/sys/fs/cgroup/unified",
			V2:    true,
		},
	}, got)
}

func TestResolveCgroupDir(t *testing.T) {
	v1Docker := []cgroupMount{
		{Root: "/docker/abc", Point: "/sys/fs/cgroup/memory", Controllers: []string{"rw", "memory"}},
		{Root: "/docker/abc", Point: "/sys/fs/cgroup/cpu,cpuacct", Controllers: []string{"rw", "cpu", "cpuacct"}},
	}
	v1Host := []cgroupMount{
		{Root: "/", Point: "/sys/fs/cgroup/memory", Controllers: []string{"rw", "memory"}},
		{Root: "/", Point: "/sys/fs/cgroup/cpuset", Controllers: []string{"rw", "cpuset"}},
	}
	v2Root := []cgroupMount{{Root: "/", Point: "/sys/fs/cgroup", V2: true}}

	tests := []struct {
		name       string
		controller string
		path       string
		want       string
		mounts     []cgroupMount
	}{
		{
			name:       "v1 docker mount is already the container subtree",
			mounts:     v1Docker,
			controller: "memory",
			path:       "/docker/abc",
			want:       "/sys/fs/cgroup/memory",
		},
		{
			name:       "v1 docker nested cgroup",
			mounts:     v1Docker,
			controller: "cpu",
			path:       "/docker/abc/child",
			want:       "/sys/fs/cgroup/cpu,cpuacct/child",
		},
		{
			name:       "v1 host mount at hierarchy root",
			mounts:     v1Host,
			controller: "memory",
			path:       "/user.slice",
			want:       "/sys/fs/cgroup/memory/user.slice",
		},
		{
			name:       "v1 cpuset mount does not serve cpu",
			mounts:     v1Host,
			controller: "cpu",
			path:       "/user.slice",
		},
		{
			name:       "v1 path outside the mount root",
			mounts:     v1Docker,
			controller: "memory",
			path:       "/docker/other",
		},
		{
			name:   "v2 private namespace",
			mounts: v2Root,
			path:   "/",
			want:   "/sys/fs/cgroup",
		},
		{
			name:   "v2 host namespace",
			mounts: v2Root,
			path:   "/system.slice/docker-abc.scope",
			want:   "/sys/fs/cgroup/system.slice/docker-abc.scope",
		},
		{
			name:   "v2 ignores v1 mounts",
			mounts: v1Host,
			path:   "/user.slice",
		},
		{
			name:   "v2 path above the namespace root",
			mounts: v2Root,
			path:   "/../outside",
		},
		{
			name:       "empty path",
			mounts:     v1Host,
			controller: "memory",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, resolveCgroupDir(tt.mounts, tt.controller, tt.path))
		})
	}
}
