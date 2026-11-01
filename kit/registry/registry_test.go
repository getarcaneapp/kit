package registry

import (
	"slices"
	"testing"
)

func TestNormalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{in: "docker.io", want: DefaultDomain},
		{in: "index.docker.io", want: DefaultDomain},
		{in: "registry-1.docker.io", want: DefaultDomain},
		{in: "http" + "://registry-1.docker.io/v2/", want: DefaultDomain},
		{in: "https://index.docker.io/v1/", want: DefaultDomain},
		{in: "HTTPS://Index.Docker.IO/v1/", want: DefaultDomain},
		{in: "docker.io/acme", want: DefaultDomain},
		{in: "https://ghcr.io", want: "ghcr.io"},
		{in: "https://ghcr.io/", want: "ghcr.io"},
		{in: "ghcr.io/", want: "ghcr.io"},
		{in: "/ghcr.io/", want: "ghcr.io"},
		{in: "GHCR.IO", want: "ghcr.io"},
		{in: "GHCR.IO/getarcaneapp", want: "ghcr.io"},
		{in: "https://ghcr.io/path", want: "ghcr.io"},
		{in: "https://ghcr.io/acme/", want: "ghcr.io"},
		{in: "http" + "://localhost:5000/team/sub", want: "localhost:5000"},
		{in: "registry.example.com:5000/aa/bb", want: "registry.example.com:5000"},
		{in: "  ghcr.io  ", want: "ghcr.io"},
		{in: "", want: ""},
		{in: "   ", want: ""},
	}
	for _, tt := range tests {
		if got := Normalize(tt.in); got != tt.want {
			t.Errorf("Normalize(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestLookupKeys(t *testing.T) {
	t.Parallel()

	dockerHub := []string{"docker.io", "index.docker.io", "registry-1.docker.io"}
	tests := []struct {
		in   string
		want []string
	}{
		{in: "https://index.docker.io/v1/", want: dockerHub},
		{in: "registry-1.docker.io", want: dockerHub},
		{in: "https://GHCR.IO/", want: []string{"ghcr.io"}},
		{in: "ghcr.io", want: []string{"ghcr.io"}},
		{in: ""},
		{in: "   "},
	}
	for _, tt := range tests {
		if got := LookupKeys(tt.in); !slices.Equal(got, tt.want) || (tt.want == nil) != (got == nil) {
			t.Errorf("LookupKeys(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
