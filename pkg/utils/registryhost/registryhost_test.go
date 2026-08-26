package registryhost

import (
	"slices"
	"testing"
)

func TestNormalize(t *testing.T) {
	t.Parallel()

	insecureRegistryURL := "http" + "://registry-1.docker.io/v2/"
	for _, alias := range []string{"docker.io", "index.docker.io", "registry-1.docker.io", insecureRegistryURL} {
		if got := Normalize(alias); got != DefaultDomain {
			t.Errorf("Normalize(%q) = %q, want %q", alias, got, DefaultDomain)
		}
	}

	for input, want := range map[string]string{
		"GHCR.IO/getarcaneapp":            "ghcr.io",
		"https://ghcr.io/":                "ghcr.io",
		"registry.example.com:5000/aa/bb": "registry.example.com:5000",
		"":                                "",
	} {
		if got := Normalize(input); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLookupKeys(t *testing.T) {
	t.Parallel()

	want := []string{"docker.io", "index.docker.io", "registry-1.docker.io"}
	if got := LookupKeys("https://index.docker.io/v1/"); !slices.Equal(got, want) {
		t.Errorf("LookupKeys(docker hub) = %q, want %q", got, want)
	}

	if got := LookupKeys("ghcr.io"); !slices.Equal(got, []string{"ghcr.io"}) {
		t.Errorf("LookupKeys(ghcr.io) = %q, want [ghcr.io]", got)
	}

	if got := LookupKeys(""); got != nil {
		t.Errorf("LookupKeys(\"\") = %q, want nil", got)
	}
}
