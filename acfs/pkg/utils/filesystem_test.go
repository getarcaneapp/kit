package utils

import (
	"errors"
	"os"
	"testing"
)

func TestNormalizeLogicalPath(t *testing.T) {
	t.Parallel()
	for input, want := range map[string]string{
		"/":               ".",
		"/directory":      "directory",
		"/directory/file": "directory/file",
	} {
		got, err := NormalizeLogicalPath(input)
		if err != nil || got != want {
			t.Errorf("NormalizeLogicalPath(%q) = (%q, %v), want (%q, nil)", input, got, err, want)
		}
	}
	for _, input := range []string{"", "relative", "/trailing/", "/a//b", "/a/./b", "/a/../b", "/nul\x00byte"} {
		if _, err := NormalizeLogicalPath(input); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("NormalizeLogicalPath(%q) error = %v, want ErrInvalidPath", input, err)
		}
	}
}

func TestFormatMode(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		mode os.FileMode
		want string
	}{
		"regular":        {mode: 0o640, want: "-rw-r-----"},
		"directory":      {mode: os.ModeDir | 0o755, want: "drwxr-xr-x"},
		"symlink":        {mode: os.ModeSymlink | 0o777, want: "lrwxrwxrwx"},
		"setuid":         {mode: os.ModeSetuid | 0o750, want: "-rwsr-x---"},
		"setgid-no-exec": {mode: os.ModeSetgid | 0o640, want: "-rw-r-S---"},
		"sticky":         {mode: os.ModeSticky | 0o777, want: "-rwxrwxrwt"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := FormatMode(test.mode); got != test.want {
				t.Fatalf("FormatMode(%v) = %q, want %q", test.mode, got, test.want)
			}
		})
	}
}
