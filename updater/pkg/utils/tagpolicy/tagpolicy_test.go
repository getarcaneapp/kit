package tagpolicy_test

import (
	"testing"

	"go.getarcane.app/updater/pkg/utils/tagpolicy"
	"go.getarcane.app/updater/types"
)

func TestSelect(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		current   string
		tags      []string
		policy    types.Policy
		want      string
		wantError bool
	}{
		{name: "numeric ordering", current: "3.1.9", tags: []string{"3.1.10", "3.1.8", "4.0.0"}, want: "3.1.10"},
		{name: "major default", current: "3.1.9", tags: []string{"3.2.0", "4.0.0"}, want: "3.2.0"},
		{name: "zero minor default", current: "0.1.9", tags: []string{"0.1.10", "0.2.0", "1.0.0"}, want: "0.1.10"},
		{name: "major upgrade", current: "3.1.9", tags: []string{"4.0.0", "5.0.0"}, policy: types.Policy{Constraint: "4.x"}, want: "4.0.0"},
		{name: "stable default", current: "3.1.9", tags: []string{"3.2.0-rc.1"}, want: "3.1.9"},
		{name: "prerelease explicit", current: "3.1.9", tags: []string{"3.2.0-rc.1", "4.0.0"}, policy: types.Policy{Constraint: ">=3.1.9-0 <4.0.0"}, want: "3.2.0-rc.1"},
		{name: "prerelease current", current: "3.1.9-rc.1", tags: []string{"3.1.9-rc.2"}, policy: types.Policy{Constraint: ">=3.1.9-0 <4.0.0"}, want: "3.1.9-rc.2"},
		{name: "constraint excludes prereleases", current: "3.1.9", tags: []string{"3.2.0-rc.1"}, policy: types.Policy{Constraint: "3.x"}, want: "3.1.9"},
		{name: "v prefix preserved", current: "v3.1.9", tags: []string{"v3.1.10"}, want: "v3.1.10"},
		{name: "equivalent candidates", current: "3.1.9", tags: []string{"v3.1.10", "3.1.10"}, want: "3.1.10"},
		{name: "equivalent candidates reversed", current: "3.1.9", tags: []string{"3.1.10", "v3.1.10"}, want: "3.1.10"},
		{name: "equal version ignored", current: "3.1.9", tags: []string{"v3.1.9", "3.1.8"}, want: "3.1.9"},
		{name: "variant capture", current: "3.1.2-alpine", tags: []string{"3.1.10-alpine", "3.2.0-bookworm", "4.0.0-alpine"}, policy: types.Policy{Constraint: "3.x", TagPattern: `(?P<version>\d+\.\d+\.\d+)-alpine`}, want: "3.1.10-alpine"},
		{name: "pattern full match", current: "3.1.2", tags: []string{"prefix3.2.0", "3.2.0suffix", "3.1.3"}, policy: types.Policy{TagPattern: `\d+\.\d+\.\d+`}, want: "3.1.3"},
		{name: "variant requires policy", current: "3.1.2-alpine", wantError: true},
		{name: "invalid current", current: "latest", wantError: true},
		{name: "incomplete current", current: "3.1", wantError: true},
		{name: "invalid regex", current: "3.1.2", policy: types.Policy{TagPattern: "["}, wantError: true},
		{name: "invalid constraint", current: "3.1.2", policy: types.Policy{Constraint: "not a range"}, wantError: true},
		{name: "nonmatching current", current: "3.1.2", policy: types.Policy{TagPattern: `(?P<version>\d+\.\d+\.\d+)-alpine`}, wantError: true},
		{name: "duplicate capture", current: "3.1.2", policy: types.Policy{TagPattern: `(?P<version>.*)(?P<version>.*)`}, wantError: true},
		{name: "ignore unrelated tags", current: "3.1.2", tags: []string{"latest", "3.2", "3.1.3", "3.2.1-alpine"}, want: "3.1.3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tagpolicy.Select(tt.current, tt.tags, tt.policy)
			if tt.wantError {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()
	got, err := tagpolicy.Version("v3.1.2-alpine", types.Policy{TagPattern: `v(?P<version>\d+\.\d+\.\d+)-alpine`})
	if err != nil {
		t.Fatal(err)
	}
	if got != "3.1.2" {
		t.Fatalf("got %q", got)
	}
	if _, err := tagpolicy.Version("1.2", types.Policy{}); err == nil {
		t.Fatal("expected incomplete version error")
	}
}
