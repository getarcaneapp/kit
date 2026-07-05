package convert_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.getarcane.app/docker/convert"
	converttypes "go.getarcane.app/docker/convert/types"
)

func TestConvertGoldenCases(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		golden      string
		envFile     string
		serviceName string
	}{
		{
			name: "basic command",
			input: "docker run --name web -p 8080:80 -v data:/data -e FOO=bar --restart unless-stopped " +
				"-w /srv/app -u 1000:1000 --entrypoint /entrypoint.sh -it --privileged " +
				"--label com.example.role=frontend --health-cmd 'curl -f http://localhost || exit 1' " +
				"-m 512m --cpus 0.5 nginx:1.27-alpine nginx -g 'daemon off;'",
			golden:      "basic.golden.yaml",
			envFile:     "FOO=bar\n",
			serviceName: "web",
		},
		{
			name:        "multi command",
			input:       "docker run --name web nginx:alpine; podman create --name db -e POSTGRES_PASSWORD=secret postgres:16",
			golden:      "multi.golden.yaml",
			envFile:     "POSTGRES_PASSWORD=secret\n",
			serviceName: "web",
		},
		{
			name:        "env file and ulimit",
			input:       "docker container run --name worker --env-file .env --ulimit nofile=1024:2048 alpine:3.20 sleep 30",
			golden:      "env-ulimit.golden.yaml",
			serviceName: "worker",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := convert.Convert(tt.input, converttypes.Options{})
			if err != nil {
				t.Fatalf("Convert returned error: %v", err)
			}

			if len(result.Services) == 0 {
				t.Fatal("expected at least one service")
			}
			if got := result.Services[0].Name; got != tt.serviceName {
				t.Fatalf("first service name = %q, want %q", got, tt.serviceName)
			}
			if string(result.EnvFile) != tt.envFile {
				t.Fatalf("EnvFile = %q, want %q", result.EnvFile, tt.envFile)
			}
			if result.Project == nil {
				t.Fatal("expected compose-go project")
			}

			want := readGolden(t, tt.golden)
			if got := string(result.YAML); got != want {
				t.Fatalf("YAML mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}

func TestConvertRejectsMissingImage(t *testing.T) {
	_, err := convert.Convert("docker run --name web", converttypes.Options{})
	if err == nil {
		t.Fatal("expected missing image error")
	}
	if !errors.Is(err, converttypes.ErrParse) {
		t.Fatalf("error should wrap ErrParse, got %T: %v", err, err)
	}
}

func TestConvertDoesNotExecuteShellExpansion(t *testing.T) {
	result, err := convert.Convert("docker run --name safe -e TOKEN=$(echo leaked) alpine:3.20 sh -c 'echo `whoami`'", converttypes.Options{})
	if err != nil {
		t.Fatalf("Convert returned error: %v", err)
	}

	yaml := string(result.YAML)
	for _, want := range []string{"TOKEN=$(echo leaked)", "echo `whoami`"} {
		if !strings.Contains(yaml, want) {
			t.Fatalf("YAML should preserve %q without shell expansion:\n%s", want, yaml)
		}
	}
}

func TestBuildDocumentMergesExistingCompose(t *testing.T) {
	commands, err := convert.ParseCommands("docker run --name web nginx:alpine", converttypes.ParseOptions{})
	if err != nil {
		t.Fatalf("ParseCommands returned error: %v", err)
	}

	doc, err := convert.BuildDocument(commands, converttypes.Options{ExistingComposeYAML: []byte("services:\n  api:\n    image: caddy:2\n")})
	if err != nil {
		t.Fatalf("BuildDocument returned error: %v", err)
	}

	out, err := convert.MarshalYAML(doc, converttypes.MarshalOptions{})
	if err != nil {
		t.Fatalf("MarshalYAML returned error: %v", err)
	}

	yaml := string(out)
	for _, want := range []string{"api:", "image: caddy:2", "web:", "image: nginx:alpine"} {
		if !strings.Contains(yaml, want) {
			t.Fatalf("merged YAML missing %q:\n%s", want, yaml)
		}
	}
}

func readGolden(t *testing.T, name string) string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	return string(b)
}
