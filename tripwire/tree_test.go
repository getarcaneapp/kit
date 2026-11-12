package tripwire

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
)

func testTree(roots ...string) *tree {
	return newTree(roots, Config[string]{}.resolve())
}

func TestDepth(t *testing.T) {
	tests := []struct {
		rel  string
		want int
	}{
		{rel: ".", want: 0},
		{rel: "a", want: 1},
		{rel: filepath.Join("a", "b", "c"), want: 3},
		{rel: "..", want: -1},
		{rel: filepath.Join("..", "x"), want: -1},
		{rel: "..a", want: 1},
	}
	for _, tt := range tests {
		if got := depth(tt.rel); got != tt.want {
			t.Errorf("depth(%q) = %d, want %d", tt.rel, got, tt.want)
		}
	}
}

func TestLocate(t *testing.T) {
	tr := testTree("/srv/projects", "/opt/stacks")
	tests := []struct {
		logical string
		rel     string
		root    int
	}{
		{logical: "/srv/projects", root: 0, rel: "."},
		{logical: "/srv/projects/a/compose.yaml", root: 0, rel: filepath.Join("a", "compose.yaml")},
		{logical: "/opt/stacks", root: 1, rel: "."},
		{logical: "/opt/stacks/b", root: 1, rel: "b"},
		{logical: "/srv/projectsX/a", root: -1, rel: ".."},
		{logical: "/elsewhere", root: -1, rel: ".."},
	}
	for _, tt := range tests {
		if root, rel := tr.locate(tt.logical); root != tt.root || rel != tt.rel {
			t.Errorf("locate(%q) = %d, %q, want %d, %q", tt.logical, root, rel, tt.root, tt.rel)
		}
	}
}

func TestLocateAtFilesystemRoot(t *testing.T) {
	if root, rel := testTree("/").locate("/a/b"); root != 0 || rel != filepath.Join("a", "b") {
		t.Errorf("locate at / = %d, %q", root, rel)
	}
}

func TestLogical(t *testing.T) {
	tr := testTree("/srv/projects")
	tr.aliases["/data/real"] = []string{"/srv/projects/linked"}
	tr.aliases["/data/real/inner-target"] = []string{"/srv/projects/linked/inner"}

	tests := []struct {
		real string
		want string
	}{
		{real: "/srv/projects/plain/compose.yaml", want: "/srv/projects/plain/compose.yaml"},
		{real: "/data/real", want: "/srv/projects/linked"},
		{real: "/data/real/compose.yaml", want: "/srv/projects/linked/compose.yaml"},
		{real: "/data/real/a/b/compose.yaml", want: "/srv/projects/linked/a/b/compose.yaml"},
		{real: "/data/real/inner-target/x", want: "/srv/projects/linked/inner/x"},
		{real: "/data/realX/compose.yaml", want: "/data/realX/compose.yaml"},
	}
	for _, tt := range tests {
		if got := tr.logical(tt.real); got != tt.want {
			t.Errorf("logical(%q) = %q, want %q", tt.real, got, tt.want)
		}
	}
}

func TestAliasPrefersEarlierRoot(t *testing.T) {
	tr := testTree("/a", "/b")
	tr.alias("/data/shared", "/b/link")
	tr.alias("/data/shared", "/a/link")
	tr.alias("/data/shared", "/a/link")

	if got := tr.aliases["/data/shared"]; !slices.Equal(got, []string{"/a/link", "/b/link"}) {
		t.Fatalf("aliases = %q, want /a/link first", got)
	}
	if got := tr.logical("/data/shared/x"); got != "/a/link/x" {
		t.Errorf("logical = %q, want /a/link/x", got)
	}
}

func TestConfigRoots(t *testing.T) {
	tests := []struct {
		err   error
		name  string
		roots []string
		want  []string
	}{
		{name: "nil", err: ErrNoRoot},
		{name: "empty path", roots: []string{"/a", ""}, err: ErrNoRoot},
		{name: "cleaned", roots: []string{"/a/../b/", "/c//d"}, want: []string{"/b", "/c/d"}},
		{name: "duplicates", roots: []string{"/a", "/b", "/a/"}, want: []string{"/a", "/b"}},
		{name: "nested", roots: []string{"/a", "/a/b"}, err: ErrNestedRoots},
		{name: "nested reversed", roots: []string{"/a/b", "/a"}, err: ErrNestedRoots},
		{name: "sibling prefix", roots: []string{"/a", "/ab"}, want: []string{"/a", "/ab"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Config[string]{Roots: tt.roots}.roots()
			if !errors.Is(err, tt.err) {
				t.Fatalf("err = %v, want %v", err, tt.err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("roots = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConfigResolveDefaults(t *testing.T) {
	cfg := Config[string]{Key: PathOf, MaxDepth: -3, MaxDelay: 1}.resolve()
	switch {
	case cfg.Debounce != defaultDebounce:
		t.Errorf("Debounce = %v", cfg.Debounce)
	case cfg.MaxDelay != cfg.Debounce:
		t.Errorf("MaxDelay = %v, want raised to Debounce", cfg.MaxDelay)
	case cfg.MaxDepth != 0:
		t.Errorf("MaxDepth = %d, want 0", cfg.MaxDepth)
	case cfg.Backoff != Backoff{Initial: defaultBackoffInitial, Max: defaultBackoffMax}:
		t.Errorf("Backoff = %+v", cfg.Backoff)
	case cfg.OnBatch == nil || cfg.SkipDir == nil || cfg.Logger == nil:
		t.Error("nil defaults left unresolved")
	}
}
