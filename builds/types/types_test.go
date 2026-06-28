package types

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestBuildRequestJSONShape(t *testing.T) {
	req := BuildRequest{
		ContextDir:       "/workspace/app",
		DockerfileInline: "FROM scratch",
		Tags:             []string{"example/app:latest"},
		BuildArgs:        map[string]string{"VERSION": "1"},
		Platforms:        []string{"linux/amd64"},
		Push:             true,
		Provider:         "depot",
	}

	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal BuildRequest: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal BuildRequest JSON: %v", err)
	}

	for _, key := range []string{"contextDir", "dockerfileInline", "tags", "buildArgs", "platforms", "push", "provider"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("expected JSON key %q in %s", key, string(raw))
		}
	}
}

func TestBuildErrorsAreTypedAndUnwrap(t *testing.T) {
	baseErr := errors.New("exporter missing")
	err := &BuildKitImageExporterError{ProviderName: "depot", Err: baseErr}

	if !errors.Is(err, baseErr) {
		t.Fatalf("expected BuildKitImageExporterError to unwrap wrapped error")
	}
	if err.Error() == "" {
		t.Fatalf("expected non-empty error text")
	}
}
