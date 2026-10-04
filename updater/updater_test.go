package updater

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/opencontainers/go-digest"
)

func TestNewRejectsInvalidConfig(t *testing.T) {
	_, err := New(Config{OperationTimeout: -time.Second})
	if err == nil {
		t.Fatal("New() error = nil, want a ConfigError for a negative OperationTimeout")
	}

	var configErr *ConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("New() error = %T, want *ConfigError", err)
	}
	if configErr.Field != "OperationTimeout" {
		t.Fatalf("ConfigError.Field = %q, want OperationTimeout", configErr.Field)
	}
}

// Close must shut down only the Docker client New created; a caller-supplied
// provider belongs to the caller.
func TestCloseOnlyClosesOwnedDockerClient(t *testing.T) {
	owned, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if owned.ownedDockerClient == nil {
		t.Fatal("New() with a zero Config did not create its own Docker client")
	}
	if err = owned.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err = owned.Close(); err != nil {
		t.Fatalf("second Close() error = %v, want nil", err)
	}

	borrowed, err := New(Config{DockerClientProvider: &fakeDockerClientProvider{}})
	if err != nil {
		t.Fatalf("New() with a custom provider error = %v", err)
	}
	if borrowed.ownedDockerClient != nil {
		t.Fatal("New() claimed ownership of a caller-supplied Docker client provider")
	}
	if err = borrowed.Close(); err != nil {
		t.Fatalf("Close() with a custom provider error = %v", err)
	}
}

func TestNewAppliesGenericDockerDefaults(t *testing.T) {
	service := newServiceForTest(t, Config{})
	if _, ok := service.config.DockerClientProvider.(*DockerClient); !ok {
		t.Fatalf("DockerClientProvider = %T, want built-in provider", service.config.DockerClientProvider)
	}
	if _, ok := service.config.ImagePuller.(defaultImagePuller); !ok {
		t.Fatalf("ImagePuller = %T, want built-in puller", service.config.ImagePuller)
	}
	if _, ok := service.config.PendingStore.(*memoryPendingStore); !ok {
		t.Fatalf("PendingStore = %T, want built-in memory store", service.config.PendingStore)
	}
	if _, ok := service.config.RegistryDigestResolver.(defaultRegistryDigestResolver); !ok {
		t.Fatalf("RegistryDigestResolver = %T, want built-in resolver", service.config.RegistryDigestResolver)
	}
	if _, ok := service.config.ProjectUpdater.(dockerComposeProjectUpdater); !ok {
		t.Fatalf("ProjectUpdater = %T, want built-in compose updater", service.config.ProjectUpdater)
	}
}

func TestNewKeepsCustomDockerAdapters(t *testing.T) {
	puller := &fakePuller{}
	store := &fakePendingStore{}
	service := newServiceForTest(t, Config{
		DockerClientProvider:   &fakeDockerClientProvider{err: errors.New("custom")},
		ImagePuller:            puller,
		PendingStore:           store,
		RegistryDigestResolver: fakeDigestResolver{},
	})
	if _, ok := service.config.DockerClientProvider.(*fakeDockerClientProvider); !ok {
		t.Fatalf("DockerClientProvider = %T, want custom provider", service.config.DockerClientProvider)
	}
	if service.config.ImagePuller != puller {
		t.Fatalf("ImagePuller was replaced")
	}
	if service.config.PendingStore != store {
		t.Fatalf("PendingStore was replaced")
	}
	if _, ok := service.config.RegistryDigestResolver.(fakeDigestResolver); !ok {
		t.Fatalf("RegistryDigestResolver = %T, want custom resolver", service.config.RegistryDigestResolver)
	}
}

// The zero Config gives a service backed entirely by the local Docker
// environment.
func ExampleNew() {
	service, err := New(Config{})
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if closeErr := service.Close(); closeErr != nil {
			log.Print(closeErr)
		}
	}()

	result, err := service.UpdateContainer(context.Background(), "my-container-id", Options{})
	if err != nil {
		log.Print(err)
		return
	}
	fmt.Printf("updated %d of %d containers in %s\n", result.Updated, result.Checked, result.Duration())
}

// newServiceForTest builds a fully defaulted Service the way callers
// do, failing the test if the config is rejected.
func newServiceForTest(t *testing.T, config Config) *Service {
	t.Helper()
	service, err := New(config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := service.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})
	return service
}

type fakeDockerClientProvider struct {
	client *client.Client
	err    error
	calls  int
}

func (f *fakeDockerClientProvider) DockerClient(ctx context.Context) (*client.Client, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	f.calls++
	return f.client, f.err
}

type fakePendingStore struct {
	records []ImageUpdateRecord
	cleared []string
}

func (f *fakePendingStore) PendingImageUpdates(ctx context.Context) ([]ImageUpdateRecord, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return f.records, nil
}

func (f *fakePendingStore) ClearImageUpdateRecord(ctx context.Context, record ImageUpdateRecord) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	f.cleared = append(f.cleared, record.ID)
	return nil
}

type fakeRunRecorder struct {
	results []ResourceResult
}

func (f *fakeRunRecorder) RecordUpdateRun(ctx context.Context, result ResourceResult) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	f.results = append(f.results, result)
	return nil
}

type fakePuller struct {
	pulled []string
	err    error
	after  func(imageRef string)
}

func (f *fakePuller) PullImage(ctx context.Context, imageRef string, progress io.Writer) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	f.pulled = append(f.pulled, imageRef)
	if progress != nil {
		if _, err := io.WriteString(progress, imageRef); err != nil {
			return err
		}
	}
	if f.err != nil {
		return f.err
	}
	if f.after != nil {
		f.after(imageRef)
	}
	return nil
}

// fakeSettings excludes the listed container names or IDs the way a host's
// settings would. Its list never changes: the engine only reads it.
type fakeSettings struct {
	excluded []string
}

func (f *fakeSettings) ExcludedContainers(ctx context.Context) ([]string, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return f.excluded, nil
}

type fakeDigestResolver struct{}

func (fakeDigestResolver) ImageDigest(ctx context.Context, imageRef string) (string, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	if strings.TrimSpace(imageRef) == "" {
		return "", errors.New("image ref is required")
	}
	return digest.FromString(imageRef).String(), nil
}

type countingDigestResolver struct {
	digest string
	err    error
	calls  int
}

func (r *countingDigestResolver) ImageDigest(ctx context.Context, imageRef string) (string, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	r.calls++
	if r.err != nil {
		return "", r.err
	}
	return r.digest, nil
}

type fakeProjectUpdater struct {
	projects    map[string]ComposeProject
	updateCalls []string
	err         error
	delay       time.Duration
}

func (f *fakeProjectUpdater) ProjectByComposeName(ctx context.Context, composeName string) (ComposeProject, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return ComposeProject{}, err
		}
	}
	if project, ok := f.projects[composeName]; ok {
		return project, nil
	}
	return ComposeProject{}, errors.New("project not found")
}

func (f *fakeProjectUpdater) UpdateServices(ctx context.Context, projectID string, services []string) error {
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	f.updateCalls = append(f.updateCalls, projectID+":"+strings.Join(services, ","))
	return f.err
}

type fakeSelfUpdater struct {
	targets []SelfUpdateTarget
}

func (f *fakeSelfUpdater) TriggerSelfUpdate(ctx context.Context, target SelfUpdateTarget) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	f.targets = append(f.targets, target)
	return nil
}

type fakeEventRecorder struct {
	events []Event
}

func (f *fakeEventRecorder) RecordEvent(ctx context.Context, event Event) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	f.events = append(f.events, event)
	return nil
}

type captureLogHandler struct {
	records []slog.Record
}

func (h *captureLogHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (h *captureLogHandler) Handle(_ context.Context, record slog.Record) error {
	h.records = append(h.records, record.Clone())
	return nil
}

func (h *captureLogHandler) WithAttrs([]slog.Attr) slog.Handler {
	return h
}

func (h *captureLogHandler) WithGroup(string) slog.Handler {
	return h
}

type recordingSelfUpdater struct {
	operations *[]string
	targets    []SelfUpdateTarget
}

func (r *recordingSelfUpdater) TriggerSelfUpdate(ctx context.Context, target SelfUpdateTarget) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	*r.operations = append(*r.operations, "self-update:"+target.ContainerID)
	r.targets = append(r.targets, target)
	return nil
}

func newDockerClientForHandler(t *testing.T, handler http.HandlerFunc) *client.Client {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	dockerClient, err := client.New(client.WithHost(server.URL), client.WithAPIVersion("1.41"))
	if err != nil {
		t.Fatalf("new docker client: %v", err)
	}
	return dockerClient
}

func dockerAPIPath(path string) string {
	trimmed := strings.TrimPrefix(path, "/")
	version, rest, ok := strings.Cut(trimmed, "/")
	if ok && strings.HasPrefix(version, "v") {
		return "/" + rest
	}
	return path
}

func writeDockerJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("write json response: %v", err)
	}
}

func closeHTTPConnection(t *testing.T, w http.ResponseWriter) {
	t.Helper()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		t.Fatal("response writer does not support hijacking")
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		t.Fatalf("hijack connection: %v", err)
	}
	_ = conn.Close()
}
