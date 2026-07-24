package docker

import (
	"strings"
	"testing"
)

func TestRenderJSONMessageStream(t *testing.T) {
	t.Run("renders docker CLI text for stream messages", func(t *testing.T) {
		stream := strings.NewReader(
			`{"status":"Pulling fs layer","id":"layer1"}` + "\n" +
				`{"stream":"Successfully tagged demo:latest\n"}` + "\n")
		var out strings.Builder

		if err := RenderJSONMessageStream(stream, &out); err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		// Assert on rendered content, not jsonmessage's exact formatting, which
		// is not part of the moby library's public contract.
		for _, want := range []string{"Pulling fs layer", "Successfully tagged demo:latest"} {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("expected output to contain %q, got %q", want, out.String())
			}
		}
	})

	t.Run("returns structured errorDetail from daemon", func(t *testing.T) {
		stream := strings.NewReader(`{"errorDetail":{"code":401,"message":"unauthorized"}}` + "\n")
		err := RenderJSONMessageStream(stream, nil)
		if err == nil || !strings.Contains(err.Error(), "unauthorized") {
			t.Fatalf("expected unauthorized error, got %v", err)
		}
	})
}
