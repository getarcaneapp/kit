package updater

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"go.getarcane.app/updater/internal/compose"
	updatetypes "go.getarcane.app/updater/types"
)

type recordingProjectImagesInternal struct {
	fakeProjectUpdater
	changes   map[string]updatetypes.ServiceImageChange
	projectID string
	imageErr  error
}

func (f *recordingProjectImagesInternal) UpdateServiceImages(_ context.Context, projectID string, changes map[string]updatetypes.ServiceImageChange) error {
	f.projectID, f.changes = projectID, changes
	return f.imageErr
}

func TestPreflightComposeImageInternal(t *testing.T) {
	for _, tt := range []struct {
		name      string
		adapter   ProjectUpdater
		labels    map[string]string
		selfID    string
		newRef    string
		wantError bool
	}{
		{name: "standalone", newRef: "app:2"},
		{name: "unsupported", labels: map[string]string{compose.ProjectLabelKey: "app", compose.ServiceLabelKey: "web"}, adapter: &fakeProjectUpdater{}, newRef: "app:2", wantError: true},
		{name: "missing service", labels: map[string]string{compose.ProjectLabelKey: "app"}, newRef: "app:2", wantError: true},
		{name: "self update exempt", labels: map[string]string{compose.ProjectLabelKey: "app", compose.ServiceLabelKey: "web"}, selfID: "self", newRef: "app:2"},
		{name: "digest exempt", labels: map[string]string{compose.ProjectLabelKey: "app", compose.ServiceLabelKey: "web"}, newRef: "docker.io/library/app:1"},
		{name: "unresolved", labels: map[string]string{compose.ProjectLabelKey: "app", compose.ServiceLabelKey: "web"}, adapter: &recordingProjectImagesInternal{}, newRef: "app:2", wantError: true},
		{name: "empty project ID", labels: map[string]string{compose.ProjectLabelKey: "app", compose.ServiceLabelKey: "web"}, adapter: &recordingProjectImagesInternal{fakeProjectUpdater: fakeProjectUpdater{projects: map[string]ComposeProject{"app": {}}}}, newRef: "app:2", wantError: true},
		{name: "supported", labels: map[string]string{compose.ProjectLabelKey: "app", compose.ServiceLabelKey: "web"}, adapter: &recordingProjectImagesInternal{fakeProjectUpdater: fakeProjectUpdater{projects: map[string]ComposeProject{"app": {ID: "project-app"}}}}, newRef: "app:2"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			service := newService(Config{ProjectUpdater: tt.adapter, SelfContainerID: tt.selfID})
			err := service.preflightComposeImageInternal(context.Background(), container.Summary{ID: "self"}, container.InspectResponse{Config: &container.Config{Image: "app:1", Labels: tt.labels}}, tt.newRef)
			if (err != nil) != tt.wantError {
				t.Fatalf("error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestUpdateComposeImageInternal(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "persists and verifies"
		if failed {
			name = "adapter failure"
		}
		t.Run(name, func(t *testing.T) {
			adapter := &recordingProjectImagesInternal{fakeProjectUpdater: fakeProjectUpdater{projects: map[string]ComposeProject{"app": {ID: "project-app"}}}}
			if failed {
				adapter.imageErr = errors.New("persist failed")
			}
			dockerClient := newDockerClientForHandler(t, func(w http.ResponseWriter, r *http.Request) {
				switch dockerAPIPath(r.URL.Path) {
				case "/images/app:2/json":
					writeDockerJSON(t, w, image.InspectResponse{ID: "sha256:same"})
				case "/containers/json":
					writeDockerJSON(t, w, []container.Summary{{ID: "new"}})
				case "/containers/new/json":
					writeDockerJSON(t, w, container.InspectResponse{Image: "sha256:same", Config: &container.Config{Image: "app:2"}, State: &container.State{Running: true}})
				default:
					t.Errorf("unexpected API request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			})
			service := newService(Config{ProjectUpdater: adapter, DockerClientProvider: &fakeDockerClientProvider{client: dockerClient}})
			err := service.updateComposeImageInternal(context.Background(), container.Summary{ID: "old"}, container.InspectResponse{Image: "sha256:same", Config: &container.Config{Image: "app:1", Labels: map[string]string{compose.ProjectLabelKey: "app", compose.ServiceLabelKey: "web"}}}, "app:2")
			if (err != nil) != failed {
				t.Fatalf("error = %v", err)
			}
			if adapter.projectID != "project-app" || adapter.changes["web"] != (updatetypes.ServiceImageChange{ExpectedRef: "app:1", TargetRef: "app:2"}) {
				t.Fatalf("unexpected adapter arguments %s %#v", adapter.projectID, adapter.changes)
			}
		})
	}
}

func TestVerifyComposeTargetInternal(t *testing.T) {
	for _, tt := range []struct {
		name, configuredRef, imageID string
		running, empty, wantError    bool
	}{
		{name: "same digest new tag", configuredRef: "docker.io/library/app:2", imageID: "sha256:same", running: true},
		{name: "old tag same digest", configuredRef: "app:1", imageID: "sha256:same", running: true, wantError: true},
		{name: "wrong image", configuredRef: "app:2", imageID: "sha256:other", running: true, wantError: true},
		{name: "stopped during verification", configuredRef: "app:2", imageID: "sha256:same", wantError: true},
		{name: "no replicas", empty: true, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dockerClient := newDockerClientForHandler(t, func(w http.ResponseWriter, r *http.Request) {
				switch dockerAPIPath(r.URL.Path) {
				case "/images/app:2/json":
					writeDockerJSON(t, w, image.InspectResponse{ID: "sha256:same"})
				case "/containers/json":
					if !strings.Contains(r.URL.Query().Get("filters"), "com.docker.compose.service=web") {
						t.Error("missing service filter")
					}
					if tt.empty {
						writeDockerJSON(t, w, []container.Summary{})
						return
					}
					writeDockerJSON(t, w, []container.Summary{{ID: "replica-one"}, {ID: "replica-two"}})
				case "/containers/replica-one/json":
					writeDockerJSON(t, w, container.InspectResponse{Image: "sha256:same", Config: &container.Config{Image: "app:2"}, State: &container.State{Running: true}})
				case "/containers/replica-two/json":
					writeDockerJSON(t, w, container.InspectResponse{Image: tt.imageID, Config: &container.Config{Image: tt.configuredRef}, State: &container.State{Running: tt.running}})
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.NotFound(w, r)
				}
			})
			err := verifyComposeTargetInternal(context.Background(), dockerClient, "app", "web", "app:2")
			if (err != nil) != tt.wantError {
				t.Fatalf("error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}
