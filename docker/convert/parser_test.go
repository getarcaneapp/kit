package convert_test

import (
	"errors"
	"slices"
	"testing"

	"go.getarcane.app/docker/convert"
	converttypes "go.getarcane.app/docker/convert/types"
)

// summarizeCommandsInternal renders each parsed command as its mapped flags
// ("name=value") followed by the image and command arguments.
func summarizeCommandsInternal(commands []converttypes.RunCommand) [][]string {
	out := make([][]string, 0, len(commands))
	for _, cmd := range commands {
		var summary []string
		for _, flag := range cmd.Flags {
			summary = append(summary, flag.Name+"="+flag.Value)
		}
		summary = append(summary, cmd.Image)
		out = append(out, append(summary, cmd.Command...))
	}
	return out
}

func TestParseSplitsStatements(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  [][]string
	}{
		{name: "newline", input: "docker run --name one alpine\ndocker run --name two busybox", want: [][]string{{"name=one", "alpine"}, {"name=two", "busybox"}}},
		{name: "crlf", input: "docker run alpine\r\ndocker run busybox\r\n", want: [][]string{{"alpine"}, {"busybox"}}},
		{name: "and", input: "docker run alpine && docker run busybox", want: [][]string{{"alpine"}, {"busybox"}}},
		{name: "or", input: "docker run alpine || docker run busybox", want: [][]string{{"alpine"}, {"busybox"}}},
		{name: "pipe", input: "docker run alpine | docker run busybox", want: [][]string{{"alpine"}, {"busybox"}}},
		{name: "background", input: "docker run -d alpine & docker run busybox", want: [][]string{{"detach=true", "alpine"}, {"busybox"}}},
		{name: "semicolon without spaces", input: "docker run alpine;docker run busybox", want: [][]string{{"alpine"}, {"busybox"}}},
		{name: "operators inside quotes stay in the argument", input: "docker run alpine sh -c 'echo a; echo b && c'", want: [][]string{{"alpine", "sh", "-c", "echo a; echo b && c"}}},
		{name: "quoted real newline", input: "docker run -e 'X=a\nb' alpine", want: [][]string{{"environment=X=a\nb", "alpine"}}},
		{name: "line continuation", input: "docker run \\\n  --name web \\\n  alpine", want: [][]string{{"name=web", "alpine"}}},
		{name: "trailing comment", input: "docker run alpine # start it", want: [][]string{{"alpine"}}},
		{name: "leading comment line", input: "# header\ndocker run alpine", want: [][]string{{"alpine"}}},
		{name: "hash inside a word", input: "docker run --label a#b alpine", want: [][]string{{"labels=a#b", "alpine"}}},
		{name: "hash inside quotes", input: "docker run -e 'X=a # b' alpine", want: [][]string{{"environment=X=a # b", "alpine"}}},
		{name: "trailing operator", input: "docker run alpine;", want: [][]string{{"alpine"}}},
		{name: "leading operator", input: "; docker run alpine", want: [][]string{{"alpine"}}},
		{name: "doubled operator", input: "docker run alpine;; docker run busybox", want: [][]string{{"alpine"}, {"busybox"}}},
		{name: "non-ascii before operator", input: "docker run --label note=héllo✓ alpine;docker run busybox", want: [][]string{{"labels=note=héllo✓", "alpine"}, {"busybox"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commands, err := convert.Parse(tt.input, converttypes.ParseOptions{})
			if err != nil {
				t.Fatalf("Parse returned error: %v", err)
			}
			got := summarizeCommandsInternal(commands)
			if !slices.EqualFunc(got, tt.want, slices.Equal) {
				t.Fatalf("Parse = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseRejectsRedirectionsAndBadQuoting(t *testing.T) {
	for _, input := range []string{
		"docker run alpine > out.log",
		"docker run alpine 2>&1",
		"docker run alpine &> out.log",
		"docker run alpine < in",
		"docker run -e 'X=1 alpine",
	} {
		t.Run(input, func(t *testing.T) {
			_, err := convert.Parse(input, converttypes.ParseOptions{})
			if !errors.Is(err, converttypes.ErrParse) {
				t.Fatalf("Parse error = %v, want ErrParse", err)
			}
		})
	}
}
