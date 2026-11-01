package tripwire

import (
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

func testTree(root string) *tree {
	return &tree{
		aliases: make(map[string]string),
		skip:    func(string) bool { return false },
		log:     slog.Default(),
		root:    root,
		prefix:  strings.TrimSuffix(root, sep) + sep,
	}
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

func TestRel(t *testing.T) {
	tr := testTree("/srv/projects")
	tests := []struct {
		logical string
		want    string
	}{
		{logical: "/srv/projects", want: "."},
		{logical: "/srv/projects/a/compose.yaml", want: filepath.Join("a", "compose.yaml")},
		{logical: "/srv/projectsX/a", want: filepath.Join("..", "projectsX", "a")},
		{logical: "/elsewhere", want: filepath.Join("..", "..", "elsewhere")},
	}
	for _, tt := range tests {
		if got := tr.rel(tt.logical); got != tt.want {
			t.Errorf("rel(%q) = %q, want %q", tt.logical, got, tt.want)
		}
	}
}

func TestRelAtFilesystemRoot(t *testing.T) {
	if got := testTree("/").rel("/a/b"); got != filepath.Join("a", "b") {
		t.Errorf("rel at / = %q", got)
	}
}

func TestLogical(t *testing.T) {
	tr := testTree("/srv/projects")
	tr.aliases["/data/real"] = "/srv/projects/linked"
	tr.aliases["/data/real/inner-target"] = "/srv/projects/linked/inner"

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
