package convert_test

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"kit.getarcane.app/docker/convert"
	converttypes "kit.getarcane.app/docker/convert/types"
)

func buildWithFlag(t *testing.T, name, value string) (*converttypes.Document, error) {
	t.Helper()
	return convert.Build([]converttypes.RunCommand{{
		Image: "alpine",
		Flags: []converttypes.Flag{{Name: name, Value: value}},
	}}, converttypes.Options{})
}

func TestBuildRegistersOnlyNamedVolumes(t *testing.T) {
	tests := []struct {
		value string
		want  []string
	}{
		{value: "data:/data", want: []string{"data"}},
		{value: "data:/data:ro", want: []string{"data"}},
		{value: "/data"},
		{value: "data"},
		{value: "./x:/y"},
		{value: "~/x:/y"},
		{value: `C:\x:/y`},
		{value: "${VOL}:/x"},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			doc, err := buildWithFlag(t, "volumes", tt.value)
			if err != nil {
				t.Fatalf("Build returned error: %v", err)
			}
			if got := slices.Sorted(maps.Keys(doc.Volumes)); !slices.Equal(got, tt.want) {
				t.Fatalf("registered volumes = %q, want %q", got, tt.want)
			}
		})
	}

	for _, invalid := range []string{"data:/a:ro:extra", "data::/a"} {
		if _, err := buildWithFlag(t, "volumes", invalid); !errors.Is(err, converttypes.ErrConversion) {
			t.Fatalf("Build(%q) error = %v, want ErrConversion", invalid, err)
		}
	}
}

func TestBuildParsesUlimits(t *testing.T) {
	tests := []struct {
		value      string
		soft, hard int64
		wantErr    bool
	}{
		{value: "nofile=1024:2048", soft: 1024, hard: 2048},
		{value: "nofile=4096", soft: 4096, hard: 4096},
		{value: "nofile=2048:1024", wantErr: true},
		{value: "bogus=1", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			doc, err := buildWithFlag(t, "ulimits", tt.value)
			if tt.wantErr {
				if !errors.Is(err, converttypes.ErrConversion) {
					t.Fatalf("Build error = %v, want ErrConversion", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Build returned error: %v", err)
			}
			ulimits, _ := doc.Services["alpine"]["ulimits"].(map[string]any)
			nofile, _ := ulimits["nofile"].(map[string]any)
			if nofile["soft"] != tt.soft || nofile["hard"] != tt.hard {
				t.Fatalf("nofile = %v, want soft %d hard %d", nofile, tt.soft, tt.hard)
			}
		})
	}
}

func TestConvertKeepsCommandArgumentsVerbatim(t *testing.T) {
	result, err := convert.Convert(`docker run --name app alpine awk -F'|' '{print "id=" $1}'`, converttypes.Options{})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}

	want := []string{"awk", "-F|", `{print "id=" $1}`}
	if got := []string(result.Project.Services["app"].Command); !slices.Equal(got, want) {
		t.Fatalf("compose command = %q, want %q", got, want)
	}
}

func TestConvertMapsHostnameAndNetworkEndpoint(t *testing.T) {
	result, err := convert.Convert("docker run -h web --network backend --ip 10.0.0.5 --network-alias api --network-alias svc --rm nginx", converttypes.Options{})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}

	service := result.Project.Services["nginx"]
	if service.Hostname != "web" {
		t.Fatalf("hostname = %q, want web", service.Hostname)
	}
	backend := service.Networks["backend"]
	if backend == nil || backend.Ipv4Address != "10.0.0.5" || !slices.Equal(backend.Aliases, []string{"api", "svc"}) {
		t.Fatalf("backend network = %+v", backend)
	}
	if !slices.ContainsFunc(result.Warnings, func(w converttypes.Warning) bool { return strings.Contains(w.Message, "rm") }) {
		t.Fatalf("warnings = %+v, want one for --rm", result.Warnings)
	}
}

func TestBuildWarnsOnEndpointWithoutNetwork(t *testing.T) {
	doc, err := buildWithFlag(t, "ipv4_address", "10.0.0.5")
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}
	if len(doc.Warnings) != 1 {
		t.Fatalf("warnings = %+v, want one", doc.Warnings)
	}
}

func TestConvertStopTimeoutBecomesDuration(t *testing.T) {
	result, err := convert.Convert("docker run --stop-timeout 10 nginx", converttypes.Options{})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}
	if got := result.Project.Services["nginx"].StopGracePeriod; got == nil || time.Duration(*got) != 10*time.Second {
		t.Fatalf("stop_grace_period = %v, want 10s", got)
	}
}

func TestConvertMountsUseLongSyntax(t *testing.T) {
	result, err := convert.Convert("docker run --mount type=bind,source=/a,target=/b,readonly "+
		"--mount source=data,target=/data,volume-nocopy --mount type=tmpfs,target=/tmp,tmpfs-size=64m,tmpfs-mode=1770 "+
		"-v cache:/cache nginx", converttypes.Options{})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}

	got := result.Project.Services["nginx"].Volumes
	if len(got) != 4 {
		t.Fatalf("volumes = %+v, want 4", got)
	}
	if bind := got[0]; bind.Type != "bind" || bind.Source != "/a" || bind.Target != "/b" || !bind.ReadOnly {
		t.Fatalf("bind mount = %+v", bind)
	}
	if named := got[1]; named.Type != "volume" || named.Source != "data" || named.Volume == nil || !named.Volume.NoCopy {
		t.Fatalf("volume mount = %+v", named)
	}
	if tmpfs := got[2]; tmpfs.Type != "tmpfs" || tmpfs.Tmpfs == nil || tmpfs.Tmpfs.Size != 64<<20 || tmpfs.Tmpfs.Mode != 0o1770 {
		t.Fatalf("tmpfs mount = %+v", tmpfs)
	}
	if got := slices.Sorted(maps.Keys(result.Project.Volumes)); !slices.Equal(got, []string{"cache", "data"}) {
		t.Fatalf("project volumes = %q, want cache and data", got)
	}
}
