package compat

import (
	"slices"
	"testing"

	containertypes "github.com/moby/moby/api/types/container"
	systemtypes "github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"
)

func TestPrepareRecreateHostConfigForEngine_NilHostConfig(t *testing.T) {
	t.Parallel()

	out, sanitized, engine, err := PrepareRecreateHostConfigForEngine(t.Context(), nil, "", nil)
	if err != nil || out != nil || sanitized || engine != (EngineCompatibilityInfo{}) {
		t.Errorf("got (%v, %t, %+v, %v), want nil config and zero engine info", out, sanitized, engine, err)
	}
}

func TestSanitizeRecreateHostConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		engine        EngineCompatibilityInfo
		wantSanitized bool
	}{
		{name: "podman cgroup v2 strips memory swappiness", engine: EngineCompatibilityInfo{Name: "podman", CgroupVersion: "2"}, wantSanitized: true},
		{name: "podman cgroup v1 preserves memory swappiness", engine: EngineCompatibilityInfo{Name: "podman", CgroupVersion: "1"}},
		{name: "docker cgroup v2 preserves memory swappiness", engine: EngineCompatibilityInfo{Name: "docker", CgroupVersion: "2"}},
		{name: "nil host config", engine: EngineCompatibilityInfo{Name: "podman", CgroupVersion: "2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.name == "nil host config" {
				if tt.engine.SanitizeRecreateHostConfig(nil) {
					t.Error("nil host config must not report sanitization")
				}
				return
			}
			input := &containertypes.HostConfig{
				NetworkMode:      "bridge",
				Annotations:      map[string]string{"test": "value"},
				PublishAllPorts:  true,
				CPUShares:        1024,
				MemorySwappiness: new(int64(60)),
				MaskedPaths:      []string{"/proc/kcore"},
			}
			cloned := new(*input)
			if got := tt.engine.SanitizeRecreateHostConfig(cloned); got != tt.wantSanitized {
				t.Fatalf("sanitized = %t, want %t", got, tt.wantSanitized)
			}
			if (cloned.MemorySwappiness == nil) != tt.wantSanitized {
				t.Errorf("MemorySwappiness nil = %t, want %t", cloned.MemorySwappiness == nil, tt.wantSanitized)
			}
			if input.MemorySwappiness == nil || *input.MemorySwappiness != 60 {
				t.Error("input host config must not be modified")
			}
			if cloned.CPUShares != 1024 || !cloned.PublishAllPorts || cloned.NetworkMode != "bridge" || cloned.Annotations["test"] != "value" || !slices.Equal(cloned.MaskedPaths, []string{"/proc/kcore"}) {
				t.Errorf("unrelated fields changed: %+v", cloned)
			}
		})
	}
}

func TestDetectEngineCompatibility(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version client.ServerVersionResult
		info    systemtypes.Info
		want    EngineCompatibilityInfo
	}{
		{
			name:    "prefers platform name for podman detection",
			version: client.ServerVersionResult{Platform: client.PlatformInfo{Name: "Podman Engine"}},
			info:    systemtypes.Info{CgroupVersion: "2"},
			want:    EngineCompatibilityInfo{Name: "podman", CgroupVersion: "2"},
		},
		{
			name:    "detects podman from component names",
			version: client.ServerVersionResult{Components: []systemtypes.ComponentVersion{{Name: "Podman Engine"}}},
			info:    systemtypes.Info{CgroupVersion: "2"},
			want:    EngineCompatibilityInfo{Name: "podman", CgroupVersion: "2"},
		},
		{
			name:    "detects from component details",
			version: client.ServerVersionResult{Components: []systemtypes.ComponentVersion{{Name: "Engine", Details: map[string]string{"Vendor": "Docker Inc."}}}},
			info:    systemtypes.Info{CgroupVersion: " 1 "},
			want:    EngineCompatibilityInfo{Name: "docker", CgroupVersion: "1"},
		},
		{
			name: "falls back to docker markers",
			info: systemtypes.Info{CgroupVersion: "2", ServerVersion: "Docker Engine - Community"},
			want: EngineCompatibilityInfo{Name: "docker", CgroupVersion: "2"},
		},
		{name: "unknown engine", info: systemtypes.Info{CgroupVersion: "2"}, want: EngineCompatibilityInfo{CgroupVersion: "2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := DetectEngineCompatibility(tt.version, tt.info); got != tt.want {
				t.Errorf("DetectEngineCompatibility = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestKeepIDUsernsMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		uidMap  []string
		gidMap  []string
		want    containertypes.UsernsMode
		wantErr bool
	}{
		{
			name:   "keep-id with uid and gid 1000",
			uidMap: []string{"0:1:1000", "1000:0:1", "1001:1001:64536"},
			gidMap: []string{"0:1:1000", "1000:0:1", "1001:1001:64536"},
			want:   "keep-id:uid=1000,gid=1000",
		},
		{
			name:   "keep-id with different uid and gid",
			uidMap: []string{"0:1:1000", "1000:0:1", "1001:1001:64536"},
			gidMap: []string{"0:1:2000", "2000:0:1", "2001:2001:63536"},
			want:   "keep-id:uid=1000,gid=2000",
		},
		{
			name:    "no keep-id entry",
			uidMap:  []string{"0:1:65536"},
			gidMap:  []string{"0:1:65536"},
			wantErr: true,
		},
		{
			name:    "malformed entry",
			uidMap:  []string{"1000:0"},
			gidMap:  []string{"1000:0:1"},
			wantErr: true,
		},
		{
			name:    "non-numeric entry",
			uidMap:  []string{"1000:0:1"},
			gidMap:  []string{"abc:0:1"},
			wantErr: true,
		},
		{
			name:    "empty maps",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := keepIDUsernsMode(tt.uidMap, tt.gidMap)
			if (err != nil) != tt.wantErr {
				t.Fatalf("keepIDUsernsMode error = %v, wantErr %t", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("keepIDUsernsMode = %q, want %q", got, tt.want)
			}
		})
	}
}
