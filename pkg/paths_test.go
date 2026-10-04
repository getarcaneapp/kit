package kit

import "testing"

func TestSQLitePathFromDSN(t *testing.T) {
	t.Parallel()

	for dsn, want := range map[string]string{
		"file:data/arcane.db?_pragma=busy_timeout(5000)": "data/arcane.db",
		"file:/abs/path.db": "/abs/path.db",
		"file::memory:":     ":memory:",
	} {
		got, err := SQLitePathFromDSN(dsn)
		if err != nil || got != want {
			t.Errorf("SQLitePathFromDSN(%q) = %q, %v; want %q", dsn, got, err, want)
		}
	}
}

func TestNormalizeRelativePath(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"", ".", "..", "/absolute", "../escape", "a/../../b", `folder\file`, "name\x00"} {
		if _, err := NormalizeRelativePath(input); err == nil {
			t.Errorf("NormalizeRelativePath(%q) accepted invalid input", input)
		}
	}
	for _, test := range []struct{ input, want string }{
		{"folder/file.txt", "folder/file.txt"},
		{" ./a//b/../c/ ", "a/c"},
		{"folder/", "folder"},
	} {
		if got, err := NormalizeRelativePath(test.input); err != nil || got != test.want {
			t.Errorf("NormalizeRelativePath(%q) = %q, %v; want %q", test.input, got, err, test.want)
		}
	}
}

func TestValidateFileName(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"", ".", "..", "folder/name", `folder\name`, "name\x00"} {
		if _, err := ValidateFileName(input); err == nil {
			t.Errorf("ValidateFileName(%q) accepted invalid input", input)
		}
	}
	if got, err := ValidateFileName(" notes.txt "); err != nil || got != "notes.txt" {
		t.Errorf("ValidateFileName = %q, %v; want notes.txt", got, err)
	}
}

func TestFilePathMatches(t *testing.T) {
	t.Parallel()

	if !FilePathMatches("a/b", "a") || !FilePathMatches("a", "a") {
		t.Error("FilePathMatches should match the root and paths beneath it")
	}
	if FilePathMatches("ab", "a") || FilePathMatches("b/a", "a") {
		t.Error("FilePathMatches matched a sibling prefix")
	}
}

func TestSanitizeBrowsePath(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ input, want string }{
		{".", "/"},
		{"", "/"},
		{"/", "/"},
		{"a/b", "/a/b"},
		{"/a/../b", "/b"},
		{" /a/./b/ ", "/a/b"},
	} {
		if got, err := SanitizeBrowsePath(test.input); err != nil || got != test.want {
			t.Errorf("SanitizeBrowsePath(%q) = %q, %v; want %q", test.input, got, err, test.want)
		}
	}
	for _, input := range []string{"..", "a/../../../etc", "../x"} {
		if _, err := SanitizeBrowsePath(input); err == nil {
			t.Errorf("SanitizeBrowsePath(%q) accepted traversal", input)
		}
	}
}
