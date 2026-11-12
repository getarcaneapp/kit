package tripwire

import (
	"strconv"
	"testing"
)

// BenchmarkLogical measures path resolution as the number of followed
// symlinks grows. The cost should stay flat: it depends on path depth, not on
// how many aliases exist.
func BenchmarkLogical(b *testing.B) {
	for _, n := range []int{10, 1_000, 10_000} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			tr := testTree("/srv/projects")
			for i := range n {
				tr.aliases["/data/target-"+strconv.Itoa(i)] = []string{"/srv/projects/link-" + strconv.Itoa(i)}
			}
			realPath := "/data/target-" + strconv.Itoa(n/2) + "/app/config/compose.yaml"
			b.ReportAllocs()
			for b.Loop() {
				_ = tr.logical(realPath)
			}
		})
	}
}

// BenchmarkOffer measures the per-event cost of keying and deduplicating.
func BenchmarkOffer(b *testing.B) {
	w, err := New(Config[string]{
		Roots: []string{"/srv/projects", "/opt/stacks"},
		Key:   composeKey,
	})
	if err != nil {
		b.Fatal(err)
	}
	tr := testTree("/srv/projects", "/opt/stacks")
	p := &pending[string]{keys: map[string]struct{}{}}
	paths := make([]string, 256)
	for i := range paths {
		paths[i] = tr.roots[i%2].path + "/app-" + strconv.Itoa(i) + "/compose.yaml"
	}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		path := paths[i%len(paths)]
		w.offer(tr, path, tr.logical(path), Write, p)
		i++
	}
}
