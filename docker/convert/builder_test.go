package convert_test

import (
	"errors"
	"maps"
	"slices"
	"testing"

	"go.getarcane.app/docker/convert"
	converttypes "go.getarcane.app/docker/convert/types"
)

func buildWithFlagInternal(t *testing.T, name, value string) (*converttypes.Document, error) {
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
			doc, err := buildWithFlagInternal(t, "volumes", tt.value)
			if err != nil {
				t.Fatalf("Build returned error: %v", err)
			}
			if got := slices.Sorted(maps.Keys(doc.Volumes)); !slices.Equal(got, tt.want) {
				t.Fatalf("registered volumes = %q, want %q", got, tt.want)
			}
		})
	}

	for _, invalid := range []string{"data:/a:ro:extra", "data::/a"} {
		if _, err := buildWithFlagInternal(t, "volumes", invalid); !errors.Is(err, converttypes.ErrConversion) {
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
			doc, err := buildWithFlagInternal(t, "ulimits", tt.value)
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
