package updater

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"go.getarcane.app/updater/labels"
)

func TestUpdateContainerTagSelectionInternal(t *testing.T) {
	for _, scenario := range []struct {
		name                string
		dry, force, self    bool
		current, constraint string
		disabled, compose   bool
		wantFailure         bool
	}{
		{name: "same digest still changes standalone reference"},
		{name: "dry run selects target", dry: true},
		{name: "self receives new tag", self: true},
		{name: "force respects constraint", force: true, constraint: "1.0.x"},
		{name: "force rejects invalid constraint", force: true, constraint: "bad", wantFailure: true},
		{name: "disabled force", force: true, disabled: true},
		{name: "digest pin force", force: true, current: "app@sha256:" + strings.Repeat("a", 64)},
		{name: "image ID force", force: true, current: "sha256:" + strings.Repeat("a", 64)},
		{name: "compose unsupported before pull", compose: true, wantFailure: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			current := scenario.current
			if current == "" {
				current = "app:1.0.0"
			}
			values := map[string]string{labels.LabelUpdateStrategy: "tag"}
			if scenario.constraint != "" {
				values[labels.LabelUpdateConstraint] = scenario.constraint
			}
			if scenario.disabled {
				values[labels.LabelUpdater] = "false"
			}
			if scenario.compose {
				values["com.docker.compose.project"] = "project"
				values["com.docker.compose.service"] = "web"
			}
			var created []string
			mutations := 0
			dockerClient := newDockerClientForHandler(t, func(w http.ResponseWriter, r *http.Request) {
				path := dockerAPIPath(r.URL.Path)
				if r.Method != http.MethodGet {
					mutations++
				}
				switch {
				case path == "/containers/json":
					writeDockerJSON(t, w, []container.Summary{{ID: "app", Names: []string{"/app"}, Image: current, ImageID: "same", Labels: values}})
				case path == "/containers/app/json":
					writeDockerJSON(t, w, container.InspectResponse{ID: "app", Name: "/app", Image: "same", Config: &container.Config{Image: current, Labels: values}, HostConfig: &container.HostConfig{}})
				case strings.HasPrefix(path, "/images/"):
					writeDockerJSON(t, w, map[string]any{"Id": "same", "Config": map[string]any{}})
				case path == "/info":
					writeDockerJSON(t, w, map[string]any{})
				case path == "/version":
					writeDockerJSON(t, w, map[string]string{"ApiVersion": "1.41"})
				case path == "/containers/create":
					var config container.Config
					if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
						t.Error(err)
					}
					created = append(created, config.Image)
					writeDockerJSON(t, w, map[string]string{"Id": "new"})
				case strings.HasSuffix(path, "/stop") || strings.HasSuffix(path, "/start") || r.Method == http.MethodDelete:
					w.WriteHeader(http.StatusNoContent)
				default:
					http.Error(w, "unexpected "+path, http.StatusNotFound)
				}
			})
			puller := &fakePuller{}
			lister := &testTagLister{tags: []string{"1.1.0", "2.0.0"}}
			self := &fakeSelfUpdater{}
			cfg := Config{DockerClientProvider: &fakeDockerClientProvider{client: dockerClient}, RegistryTagLister: lister, ImagePuller: puller, SelfUpdater: self}
			if scenario.self {
				cfg.SelfContainerID = "app"
			}
			service := newServiceForTest(t, cfg)
			result, err := service.UpdateContainer(t.Context(), "app", Options{DryRun: scenario.dry, Force: scenario.force})
			if err != nil {
				t.Fatal(err)
			}
			if scenario.wantFailure {
				if result.Failed != 1 || mutations != 0 || len(puller.pulled) != 0 {
					t.Fatalf("expected safe failure: %+v, pulls %v", result, puller.pulled)
				}
				return
			}
			if scenario.disabled || scenario.current != "" {
				if result.Skipped != 1 || lister.calls != 0 || mutations != 0 || len(puller.pulled) != 0 {
					t.Fatalf("expected ineligible: %+v", result)
				}
				return
			}
			want := "docker.io/library/app:1.1.0"
			if scenario.constraint != "" {
				want = "docker.io/library/app:1.0.0"
			}
			if scenario.dry {
				if len(result.Items) != 1 || result.Items[0].NewImage != want || !result.Items[0].UpdateAvailable || mutations != 0 || len(puller.pulled) != 0 {
					t.Fatalf("bad preview: %+v", result)
				}
				return
			}
			if result.Updated != 1 || len(puller.pulled) != 1 || puller.pulled[0] != want {
				t.Fatalf("result %+v, pulls %v", result, puller.pulled)
			}
			if scenario.self {
				if len(self.targets) != 1 || self.targets[0].NewImageRef != want || mutations != 0 {
					t.Fatalf("bad self targets %+v", self.targets)
				}
			} else if len(created) != 1 || created[0] != want {
				t.Fatalf("created %v", created)
			}
		})
	}
}

func TestMemoryPendingStoreScopesSharedImageInternal(t *testing.T) {
	latest := "1.1.0"
	first := ImageUpdateRecord{ID: "shared", ContainerID: "first", Repository: "app", Tag: "1.0.0", LatestVersion: &latest, HasUpdate: true, UpdateType: UpdateTypeTag}
	second := first
	second.ContainerID = "second"
	store := NewMemoryPendingStore(first, second)
	if err := store.ClearImageUpdateRecord(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	records, err := store.PendingImageUpdates(t.Context())
	if err != nil || len(records) != 1 || records[0].ContainerID != "second" {
		t.Fatalf("records %+v, err %v", records, err)
	}
}
