package updater

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
)

func TestDockerComposeProjectUpdaterEndsFlagsBeforeServices(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake docker binary is a shell script")
	}
	binDir := t.TempDir()
	argsFile := filepath.Join(binDir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\n"
	if err := os.WriteFile(filepath.Join(binDir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	dockerClient := newDockerClientForHandler(t, func(w http.ResponseWriter, r *http.Request) {
		writeDockerJSON(t, w, []container.Summary{{ID: "web", Labels: map[string]string{"com.docker.compose.project": "app"}}})
	})

	updater := NewDockerComposeProjectUpdater(&fakeDockerClientProvider{client: dockerClient})
	if err := updater.UpdateServices(t.Context(), "app", []string{"--build"}); err != nil {
		t.Fatalf("UpdateServices() error = %v", err)
	}

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Fields(string(raw))
	if len(args) < 2 || args[len(args)-2] != "--" || args[len(args)-1] != "--build" {
		t.Fatalf("docker args = %q, want \"--\" before the service names", args)
	}
}
