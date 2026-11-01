package normalization

import (
	"maps"
	"slices"
	"testing"
)

func TestParseMetaTag(t *testing.T) {
	got := ParseMetaTag(" label = Name ;;invalid;keywords=a,b")
	want := map[string]string{"label": "Name", "keywords": "a,b"}
	if !maps.Equal(got, want) {
		t.Fatalf("ParseMetaTag = %v, want %v", got, want)
	}
	if empty := ParseMetaTag(""); len(empty) != 0 {
		t.Fatalf("ParseMetaTag(\"\") = %v, want empty", empty)
	}
}

func TestParseKeywords(t *testing.T) {
	if got := ParseKeywords(" a, ,b ,"); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("ParseKeywords = %q, want [a b]", got)
	}
	if got := ParseKeywords("   "); got == nil || len(got) != 0 {
		t.Fatalf("ParseKeywords(blank) = %#v, want empty non-nil slice", got)
	}
}

func TestExtractCategoryMetadata(t *testing.T) {
	type settings struct {
		First  string `catmeta:"id=general;title=General"`
		Second string `catmeta:"id=general;title=Ignored"`
		Third  string `catmeta:"id=security;title=Security"`
		Plain  string
	}

	got := ExtractCategoryMetadata[settings]()
	if len(got) != 2 || got["general"]["title"] != "General" || got["security"]["title"] != "Security" {
		t.Fatalf("ExtractCategoryMetadata = %v", got)
	}
}
