package git

import "testing"

func TestIsSupportedRepositoryURL(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]bool{
		"git@github.com:getarcaneapp/kit.git":     true,
		"https://github.com/getarcaneapp/kit":     true,
		"https://github.com/getarcaneapp/kit.git": true,
		"ssh://git@github.com/getarcaneapp/kit":   true,
		"git://github.com/getarcaneapp/kit":       true,
		"https://github.com":                      false,
		"https://github.com/":                     false,
		"ftp://github.com/getarcaneapp/kit":       false,
		"./local/path":                            false,
		"":                                        false,
		"   ":                                     false,
	} {
		if got := IsSupportedRepositoryURL(input); got != want {
			t.Errorf("IsSupportedRepositoryURL(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestRequiresRemoteProbe(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]bool{
		"https://github.com/getarcaneapp/kit":      true,
		"https://github.com/getarcaneapp/kit/":     true,
		"https://github.com/getarcaneapp/kit.git":  false,
		"https://github.com/getarcaneapp/KIT.GIT/": false,
		"git@github.com:getarcaneapp/kit":          false,
		"ssh://git@github.com/getarcaneapp/kit":    false,
		"https://github.com/":                      false,
		"":                                         false,
	} {
		if got := RequiresRemoteProbe(input); got != want {
			t.Errorf("RequiresRemoteProbe(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestNormalizeForMatch(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{
		"https://github.com/getarcaneapp/kit.git":  "https://github.com/getarcaneapp/kit",
		"https://github.com/getarcaneapp/kit/":     "https://github.com/getarcaneapp/kit",
		"https://github.com/getarcaneapp/kit.git/": "https://github.com/getarcaneapp/kit",
		"git@github.com:getarcaneapp/kit.git":      "git@github.com:getarcaneapp/kit",
		"  https://github.com/getarcaneapp/kit  ":  "https://github.com/getarcaneapp/kit",
		"": "",
	} {
		if got := NormalizeForMatch(input); got != want {
			t.Errorf("NormalizeForMatch(%q) = %q, want %q", input, got, want)
		}
	}
}
