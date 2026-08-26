package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.getarcane.app/acfs"
	acfstypes "go.getarcane.app/acfs/types"
)

var modTimePattern = regexp.MustCompile(`"modTime":"[^"]+"`)
var modTimeUnixNanoPattern = regexp.MustCompile(`"modTimeUnixNano":[0-9]+`)

func runCommand(t *testing.T, args []string, stdin string) (int, []byte, string) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(context.Background(), args, strings.NewReader(stdin), &stdout, &stderr)
	return exitCode, stdout.Bytes(), stderr.String()
}

func readGolden(t *testing.T, name string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func TestListAndStatGolden(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	filename := filepath.Join(root, "hello world.txt")
	if err := os.WriteFile(filename, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filename, 0o640); err != nil {
		t.Fatal(err)
	}

	tests := map[string]struct {
		args   []string
		golden string
	}{
		"list": {
			args:   []string{"list", "--root", root, "--path", "/"},
			golden: "list.golden.json",
		},
		"stat": {
			args:   []string{"stat", "--root", root, "--path", "/hello world.txt"},
			golden: "stat.golden.json",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			exitCode, stdout, stderr := runCommand(t, test.args, "")
			if exitCode != exitSuccess || stderr != "" {
				t.Fatalf("Run = (exit %d, stderr %q), want success", exitCode, stderr)
			}
			normalized := modTimePattern.ReplaceAllString(string(stdout), `"modTime":"<modtime>"`)
			normalized = modTimeUnixNanoPattern.ReplaceAllString(normalized, `"modTimeUnixNano":<modtime-ns>`)
			if normalized != readGolden(t, test.golden) {
				t.Fatalf("output:\n%s\nwant:\n%s", normalized, readGolden(t, test.golden))
			}
		})
	}
}

func TestReadFramingAndLimit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "payload"), []byte("hello world"), 0o600); err != nil {
		t.Fatal(err)
	}
	exitCode, stdout, stderr := runCommand(t, []string{
		"read", "--root", root, "--path", "/payload", "--limit", "5",
	}, "")
	if exitCode != exitSuccess || stderr != "" {
		t.Fatalf("Run = (exit %d, stderr %q), want success", exitCode, stderr)
	}
	reader := bytes.NewReader(stdout)
	size, err := acfs.ReadStreamHeader(reader)
	if err != nil {
		t.Fatal(err)
	}
	if size != 5 {
		t.Fatalf("header size = %d, want 5", size)
	}
	if payload := make([]byte, reader.Len()); string(func() []byte {
		_, _ = reader.Read(payload)
		return payload
	}()) != "hello" {
		t.Fatalf("payload = %q, want hello", payload)
	}
}

func TestWriteMkdirWalkAndRemove(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	exitCode, stdout, stderr := runCommand(t, []string{
		"mkdir", "--root", root, "--path", "/nested/directory", "--mode", "0750",
	}, "")
	if exitCode != exitSuccess || len(stdout) != 0 || stderr != "" {
		t.Fatalf("mkdir = (exit %d, stdout %q, stderr %q)", exitCode, stdout, stderr)
	}

	exitCode, stdout, stderr = runCommand(t, []string{
		"write", "--root", root, "--path", "/nested/directory/file", "--size", "7", "--mode", "0640",
	}, "payload")
	if exitCode != exitSuccess || len(stdout) != 0 || stderr != "" {
		t.Fatalf("write = (exit %d, stdout %q, stderr %q)", exitCode, stdout, stderr)
	}

	exitCode, stdout, stderr = runCommand(t, []string{"walk", "--root", root, "--path", "/"}, "")
	if exitCode != exitSuccess || stderr != "" {
		t.Fatalf("walk = (exit %d, stderr %q)", exitCode, stderr)
	}
	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	var paths []string
	for scanner.Scan() {
		var record acfstypes.WalkRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("decode walk record: %v", err)
		}
		if record.Version != acfstypes.ProtocolVersion {
			t.Fatalf("record version = %d", record.Version)
		}
		if record.Entry != nil {
			paths = append(paths, record.Entry.Path)
			continue
		}
		if !record.End || record.Truncated || record.Count != 3 {
			t.Fatalf("unexpected walk trailer: %#v", record)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	want := []string{"/nested", "/nested/directory", "/nested/directory/file"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("walk paths = %q, want %q", paths, want)
	}

	exitCode, stdout, stderr = runCommand(t, []string{
		"remove", "--root", root, "--path", "/nested",
	}, "")
	if exitCode != exitSuccess || len(stdout) != 0 || stderr != "" {
		t.Fatalf("remove = (exit %d, stdout %q, stderr %q)", exitCode, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "nested")); !os.IsNotExist(err) {
		t.Fatalf("removed tree still exists: %v", err)
	}
}

func TestWriteSizeMismatchKeepsStdoutClean(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	filename := filepath.Join(root, "destination")
	if err := os.WriteFile(filename, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]string{"short": "four", "excess": "sixsix"} {
		t.Run(name, func(t *testing.T) {
			exitCode, stdout, stderr := runCommand(t, []string{
				"write", "--root", root, "--path", "/destination", "--size", "5",
			}, input)
			if exitCode == exitSuccess || len(stdout) != 0 || stderr == "" {
				t.Fatalf("Run = (exit %d, stdout %q, stderr %q), want stderr-only failure", exitCode, stdout, stderr)
			}
			var response acfstypes.ErrorResponse
			if err := json.Unmarshal([]byte(stderr), &response); err != nil {
				t.Fatalf("decode structured error: %v", err)
			}
			if response.Version != acfstypes.ProtocolVersion || response.Code != acfstypes.ErrorSizeMismatch {
				t.Fatalf("structured error = %#v", response)
			}
			contents, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			if string(contents) != "original" {
				t.Fatalf("destination changed: %q", contents)
			}
		})
	}
}

func TestBoundedWalkAndApply(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	staging := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "two"), []byte("2"), 0o600); err != nil {
		t.Fatal(err)
	}
	exitCode, stdout, stderr := runCommand(t, []string{
		"walk", "--root", root, "--path", "/", "--max-entries", "1",
	}, "")
	if exitCode != exitSuccess || stderr != "" {
		t.Fatalf("bounded walk = (exit %d, stderr %q)", exitCode, stderr)
	}
	scanner := bufio.NewScanner(bytes.NewReader(stdout))
	var records []acfstypes.WalkRecord
	for scanner.Scan() {
		var record acfstypes.WalkRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Entry == nil || !records[1].End || !records[1].Truncated || records[1].Count != 1 {
		t.Fatalf("bounded walk records = %#v", records)
	}

	if err := os.WriteFile(filepath.Join(staging, "payload"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := acfstypes.ApplyManifest{
		Version: acfstypes.ProtocolVersion,
		Changes: []acfstypes.ApplyChange{{
			Operation:  acfstypes.ApplyCreateFile,
			Path:       "/created",
			StagedName: "payload",
			Size:       7,
		}},
	}
	encodedManifest, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "manifest.json"), encodedManifest, 0o600); err != nil {
		t.Fatal(err)
	}
	exitCode, stdout, stderr = runCommand(t, []string{
		"apply", "--root", root, "--staging", staging, "--manifest", "manifest.json",
	}, "")
	if exitCode != exitSuccess || stderr != "" {
		t.Fatalf("apply = (exit %d, stdout %q, stderr %q)", exitCode, stdout, stderr)
	}
	var response acfstypes.ApplyResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		t.Fatal(err)
	}
	if response.Version != acfstypes.ProtocolVersion || response.Applied != 1 {
		t.Fatalf("apply response = %#v", response)
	}
}

func TestListFailureKeepsStdoutCleanAndRemovesSpool(t *testing.T) {
	spoolDirectory := t.TempDir()
	t.Setenv("TMPDIR", spoolDirectory)
	workspaceRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspaceRoot, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(ctx, []string{
		"list", "--root", workspaceRoot, "--path", "/",
	}, strings.NewReader(""), &stdout, &stderr)
	if exitCode == exitSuccess || stdout.Len() != 0 || stderr.Len() == 0 {
		t.Fatalf("Run = (exit %d, stdout %q, stderr %q), want stderr-only failure", exitCode, stdout.String(), stderr.String())
	}
	spoolEntries, err := os.ReadDir(spoolDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if len(spoolEntries) != 0 {
		t.Fatalf("temporary list output was not removed: %#v", spoolEntries)
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()
	exitCode, stdout, stderr := runCommand(t, []string{"version"}, "")
	if exitCode != exitSuccess || stderr != "" {
		t.Fatalf("Run = (exit %d, stderr %q)", exitCode, stderr)
	}
	var response acfstypes.VersionResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		t.Fatal(err)
	}
	if response.Version == "" || response.Protocol != acfstypes.ProtocolVersion {
		t.Fatalf("unexpected version response: %#v", response)
	}
}
