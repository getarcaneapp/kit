package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	acfstypes "go.getarcane.app/acfs/types"
)

func TestBinaryKeepsProtocolAndDiagnosticsSeparate(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess integration test")
	}

	binaryName := "acfs"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	binaryPath := filepath.Join(t.TempDir(), binaryName)
	build := exec.Command("go", "build", "-trimpath", "-o", binaryPath, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build acfs: %v\n%s", err, output)
	}

	command := exec.Command(binaryPath, "version")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("acfs version: %v (%s)", err, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("version stderr = %q, want empty", stderr.String())
	}
	var response acfstypes.VersionResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode version output: %v", err)
	}

	root := t.TempDir()
	stdout.Reset()
	stderr.Reset()
	command = exec.Command(binaryPath, "stat", "--root", root, "--path", "/missing")
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err == nil {
		t.Fatal("acfs stat unexpectedly succeeded")
	}
	if stdout.Len() != 0 || stderr.Len() == 0 {
		t.Fatalf("failure output = (stdout %q, stderr %q)", stdout.String(), stderr.String())
	}

	if err := os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}
