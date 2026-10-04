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

	out, sanitized, engine, err := PrepareRecreateHostConfigForEngine(t.Context(), nil, nil)
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
			swappiness := int64(60)
			input := &containertypes.HostConfig{
				MemorySwappiness: &swappiness, CPUShares: 1024, PublishAllPorts: true, NetworkMode: "bridge",
				Annotations: map[string]string{"test": "value"}, MaskedPaths: []string{"/proc/kcore"},
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

	platform := client.ServerVersionResult{}
	platform.Platform.Name = "Podman Engine"
	tests := []struct {
		name    string
		version client.ServerVersionResult
		info    systemtypes.Info
		want    EngineCompatibilityInfo
	}{
		{name: "prefers platform name for podman detection", version: platform, info: systemtypes.Info{CgroupVersion: "2"}, want: EngineCompatibilityInfo{Name: "podman", CgroupVersion: "2"}},
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
		{name: "falls back to docker markers", info: systemtypes.Info{CgroupVersion: "2", ServerVersion: "Docker Engine - Community"}, want: EngineCompatibilityInfo{Name: "docker", CgroupVersion: "2"}},
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
