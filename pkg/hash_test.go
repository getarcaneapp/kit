package kit

import (
	"strings"
	"testing"
	"time"
)

func TestSHA256Hex(t *testing.T) {
	t.Parallel()

	const emptyDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	const abcDigest = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"

	t.Run("empty string", func(t *testing.T) {
		t.Parallel()
		if got := SHA256Hex(""); got != emptyDigest {
			t.Errorf("SHA256Hex(empty string) = %q, want %q", got, emptyDigest)
		}
	})

	t.Run("string", func(t *testing.T) {
		t.Parallel()
		if got := SHA256Hex("abc"); got != abcDigest {
			t.Errorf("SHA256Hex(abc) = %q, want %q", got, abcDigest)
		}
	})

	t.Run("bytes", func(t *testing.T) {
		t.Parallel()
		if got := SHA256Hex([]byte("abc")); got != abcDigest {
			t.Errorf("SHA256Hex(abc bytes) = %q, want %q", got, abcDigest)
		}
	})

	t.Run("binary bytes", func(t *testing.T) {
		t.Parallel()
		const want = "6e340b9cffb37a989ca544e6bb780a2c78901d3fb33738768511a30617afa01d"
		if got := SHA256Hex([]byte{0}); got != want {
			t.Errorf("SHA256Hex(zero byte) = %q, want %q", got, want)
		}
	})
}

func TestWriteRecord(t *testing.T) {
	t.Parallel()

	var buf strings.Builder
	WriteRecord(&buf, "folder/file.txt", "file", int64(12), false)
	if got, want := buf.String(), "folder/file.txt\x00file\x0012\x00false\n"; got != want {
		t.Errorf("WriteRecord wrote %q, want %q", got, want)
	}
}

func TestFingerprint(t *testing.T) {
	t.Parallel()

	type cert struct {
		CommonName *string
		ExpiresAt  *time.Time
		secret     string
	}
	type env struct {
		ID       string
		Enabled  bool
		LastSeen *time.Time
		Tags     []string
		Cert     *cert
		Labels   map[string]string
	}
	now := time.Unix(1700000000, 0)
	name := "edge"
	base := env{ID: "a", Enabled: true, LastSeen: &now, Tags: []string{"x", "y"}, Cert: &cert{CommonName: &name, ExpiresAt: &now}, Labels: map[string]string{"k": "v", "a": "b"}}

	same := base
	same.Labels = map[string]string{"a": "b", "k": "v"}
	if Fingerprint([]env{base}) != Fingerprint([]env{same}) {
		t.Error("equal values with different map insertion order must hash the same")
	}

	changes := map[string]func(*env){
		"field":          func(e *env) { e.ID = "b" },
		"bool":           func(e *env) { e.Enabled = false },
		"nil vs zero":    func(e *env) { e.LastSeen = nil },
		"slice order":    func(e *env) { e.Tags = []string{"y", "x"} },
		"slice split":    func(e *env) { e.Tags = []string{"xy"} },
		"nested nil":     func(e *env) { e.Cert = nil },
		"nested pointer": func(e *env) { e.Cert = &cert{CommonName: nil, ExpiresAt: &now} },
		"map value":      func(e *env) { e.Labels = map[string]string{"k": "v", "a": "c"} },
	}
	for name, change := range changes {
		changed := base
		change(&changed)
		if Fingerprint([]env{base}) == Fingerprint([]env{changed}) {
			t.Errorf("%s: change was not observed", name)
		}
	}

	hidden := base
	hidden.Cert = &cert{CommonName: &name, ExpiresAt: &now, secret: "ignored"}
	if Fingerprint([]env{base}) != Fingerprint([]env{hidden}) {
		t.Error("unexported fields must not affect the fingerprint")
	}

	if Fingerprint("a", "bc") == Fingerprint("ab", "c") {
		t.Error("adjacent strings must not be re-splittable")
	}
	if Fingerprint([]string{}) == Fingerprint([]string{""}) {
		t.Error("slice length must be part of the fingerprint")
	}
	if first := Fingerprint(int64(1), true); first != Fingerprint(int64(1), true) {
		t.Error("fingerprint must be deterministic")
	}
}
