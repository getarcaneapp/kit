package updater

import (
	"cmp"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"

	"go.getarcane.app/updater/labels"
	"go.getarcane.app/updater/refs"
)

func TestClearPendingRecord(t *testing.T) {
	latest := "1.28"
	tests := []struct {
		name        string
		appliedRef  string
		record      ImageUpdateRecord
		wantCleared bool
	}{
		{
			name:        "digest record cleared by short ref",
			appliedRef:  "nginx:1.27",
			record:      ImageUpdateRecord{ID: "digest-rec", Repository: "nginx", Tag: "1.27", HasUpdate: true, UpdateType: UpdateTypeDigest},
			wantCleared: true,
		},
		{
			name:        "digest record cleared across registry alias",
			appliedRef:  "docker.io/library/nginx:1.27",
			record:      ImageUpdateRecord{ID: "digest-rec", Repository: "nginx", Tag: "1.27", HasUpdate: true, UpdateType: UpdateTypeDigest},
			wantCleared: true,
		},
		{
			name:        "tag record kept when only old tag re-pulled",
			appliedRef:  "docker.io/library/nginx:1.27",
			record:      ImageUpdateRecord{ContainerID: "container", ID: "tag-rec", Repository: "nginx", Tag: "1.27", HasUpdate: true, UpdateType: UpdateTypeTag, LatestVersion: &latest},
			wantCleared: false,
		},
		{
			name:        "scoped tag record cleared when new tag applied",
			appliedRef:  "nginx:1.28",
			record:      ImageUpdateRecord{ContainerID: "container", ID: "tag-rec", Repository: "nginx", Tag: "1.27", HasUpdate: true, UpdateType: UpdateTypeTag, LatestVersion: &latest},
			wantCleared: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakePendingStore{records: []ImageUpdateRecord{tt.record}}
			service := newServiceForTest(t, Config{PendingStore: store})

			service.clearPendingRecord(t.Context(), "container", tt.appliedRef)

			if cleared := len(store.cleared) > 0; cleared != tt.wantCleared {
				t.Fatalf("cleared = %v (%v), want %v", cleared, store.cleared, tt.wantCleared)
			}
		})
	}
}

func TestUpdateStandaloneContainerRollsBackAndRemovesDanglingCreateOnStartFailure(t *testing.T) {
	var operations []string
	var createdImages []string
	var createdLabels []map[string]string
	recorder := &fakeEventRecorder{}
	dockerClient := newDockerClientForHandler(t, func(w http.ResponseWriter, r *http.Request) {
		path := dockerAPIPath(r.URL.Path)
		switch {
		case r.Method == http.MethodGet && path == "/images/app:2/json":
			writeDockerJSON(t, w, map[string]any{
				"Id": "sha256:new-image",
				"Config": map[string]any{
					"Labels": map[string]string{
						"org.opencontainers.image.version":  "v2.7.0-next.17",
						"org.opencontainers.image.revision": "new-revision",
						"org.opencontainers.image.source":   "https://image.example/new",
						"org.opencontainers.image.title":    "new-title",
					},
				},
			})
		case r.Method == http.MethodGet && path == "/images/sha256:old-image/json":
			writeDockerJSON(t, w, map[string]any{
				"Id": "sha256:old-image",
				"Config": map[string]any{
					"Labels": map[string]string{
						"org.opencontainers.image.version":  "v2.6.0-next.30",
						"org.opencontainers.image.revision": "old-revision",
						"org.opencontainers.image.source":   "https://image.example/old",
						"org.opencontainers.image.title":    "old-title",
					},
				},
			})
		case r.Method == http.MethodPost && path == "/containers/old-id/stop":
			operations = append(operations, "stop:old-id")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete && path == "/containers/old-id":
			operations = append(operations, "remove:old-id")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete && path == "/containers/new-id":
			operations = append(operations, "remove:new-id")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && path == "/containers/create":
			var body container.Config
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			createdImages = append(createdImages, body.Image)
			createdLabels = append(createdLabels, body.Labels)
			if len(createdImages) == 1 {
				operations = append(operations, "create:new")
				writeDockerJSON(t, w, map[string]any{"Id": "new-id", "Warnings": []string{}})
				return
			}
			operations = append(operations, "create:rollback")
			writeDockerJSON(t, w, map[string]any{"Id": "rollback-id", "Warnings": []string{}})
		case r.Method == http.MethodPost && path == "/containers/new-id/start":
			operations = append(operations, "start:new-id")
			http.Error(w, "start failed", http.StatusInternalServerError)
		case r.Method == http.MethodPost && path == "/containers/rollback-id/start":
			operations = append(operations, "start:rollback-id")
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected path: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	})
	service := newService(Config{
		DockerClientProvider: &fakeDockerClientProvider{client: dockerClient},
		EventRecorder:        recorder,
	})

	err := service.updateComposeOrStandalone(t.Context(),
		container.Summary{ID: "old-id", Names: []string{"/app"}},
		container.InspectResponse{State: &container.State{Running: true}, ID: "old-id", Name: "/app", Image: "sha256:old-image", Config: &container.Config{
			Image: "app:1",
			Labels: map[string]string{
				"org.opencontainers.image.version":  "v2.6.0-next.30",
				"org.opencontainers.image.revision": "old-revision",
				"org.opencontainers.image.source":   "https://container.example/override",
				"com.docker.compose.image":          "sha256:old-image",
				"com.example.custom":                "keep",
			},
		}},
		"app:2",
	)

	if err == nil {
		t.Fatal("updateComposeOrStandalone() error = nil, want start failure with rollback outcome")
	}
	if !strings.Contains(err.Error(), "rollback succeeded") {
		t.Fatalf("error = %q, want rollback succeeded detail", err.Error())
	}
	if len(createdImages) != 2 || createdImages[0] != "app:2" || createdImages[1] != "sha256:old-image" {
		t.Fatalf("created images = %#v, want new ref then old image ID", createdImages)
	}
	// Labels equal to the old image's were inherited, so they are dropped for the daemon to merge in the new image's.
	for i, phase := range []string{"new", "rollback"} {
		for _, key := range []string{"org.opencontainers.image.version", "org.opencontainers.image.revision", "org.opencontainers.image.title"} {
			if got, ok := createdLabels[i][key]; ok {
				t.Fatalf("%s container %s = %q, want inherited label left to the image", phase, key, got)
			}
		}
		if got := createdLabels[i]["org.opencontainers.image.source"]; got != "https://container.example/override" {
			t.Fatalf("%s container OCI source = %q, want container override", phase, got)
		}
		if got := createdLabels[i]["com.example.custom"]; got != "keep" {
			t.Fatalf("%s container custom label = %q, want keep", phase, got)
		}
	}
	if got := createdLabels[0]["com.docker.compose.image"]; got != "sha256:new-image" {
		t.Fatalf("new container Compose image = %q, want sha256:new-image", got)
	}
	if got := createdLabels[1]["com.docker.compose.image"]; got != "sha256:old-image" {
		t.Fatalf("rollback container Compose image = %q, want sha256:old-image", got)
	}
	assertOperationsInOrder(t, operations, []string{
		"stop:old-id",
		"remove:old-id",
		"create:new",
		"start:new-id",
		"remove:new-id",
		"create:rollback",
		"start:rollback-id",
	})
	var sawCleanup, sawRollback bool
	for _, event := range recorder.events {
		if event.Phase == "container_cleanup" {
			sawCleanup = true
		}
		if event.Phase == "container_rollback" {
			sawRollback = true
		}
	}
	if !sawCleanup || !sawRollback {
		t.Fatalf("events = %#v, want cleanup and rollback events", recorder.events)
	}
}

func TestUpdateStandaloneContainerRemovesCreatedContainerWhenExtraNetworkConnectTimesOut(t *testing.T) {
	var operationsMu sync.Mutex
	var operations []string
	removedNew := false
	appendOperation := func(operation string) {
		operationsMu.Lock()
		defer operationsMu.Unlock()
		operations = append(operations, operation)
	}
	newContainerRemoved := func() bool {
		operationsMu.Lock()
		defer operationsMu.Unlock()
		return removedNew
	}
	dockerClient := newDockerClientForHandler(t, func(w http.ResponseWriter, r *http.Request) {
		path := dockerAPIPath(r.URL.Path)
		switch {
		case r.Method == http.MethodPost && path == "/containers/old-id/stop":
			appendOperation("stop:old-id")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete && path == "/containers/old-id":
			appendOperation("remove:old-id")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && path == "/containers/create":
			var body container.Config
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			if body.Image == "app:2" {
				appendOperation("create:new")
				writeDockerJSON(t, w, map[string]any{"Id": "new-id", "Warnings": []string{}})
				return
			}
			if !newContainerRemoved() {
				http.Error(w, "name already in use", http.StatusConflict)
				return
			}
			appendOperation("create:rollback")
			writeDockerJSON(t, w, map[string]any{"Id": "rollback-id", "Warnings": []string{}})
		case r.Method == http.MethodPost && path == "/networks/secondary/connect":
			var body struct {
				Container string
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode network connect body: %v", err)
			}
			appendOperation("connect:secondary:" + body.Container)
			if body.Container != "new-id" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			time.Sleep(50 * time.Millisecond)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && path == "/containers/new-id":
			operationsMu.Lock()
			removedNew = true
			operations = append(operations, "remove:new-id")
			operationsMu.Unlock()
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && path == "/containers/rollback-id/start":
			appendOperation("start:rollback-id")
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected path: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	})
	service := newService(Config{
		DockerClientProvider: &fakeDockerClientProvider{client: dockerClient},
		OperationTimeout:     10 * time.Millisecond,
	})

	err := service.updateComposeOrStandalone(t.Context(),
		container.Summary{ID: "old-id", Names: []string{"/app"}},
		container.InspectResponse{
			State:  &container.State{Running: true},
			ID:     "old-id",
			Name:   "/app",
			Image:  "sha256:old-image",
			Config: &container.Config{Image: "app:1"},
			NetworkSettings: &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{
				"primary":   {},
				"secondary": {},
			}},
		},
		"app:2",
	)

	if err == nil {
		t.Fatal("updateComposeOrStandalone() error = nil, want network timeout with rollback outcome")
	}
	if !strings.Contains(err.Error(), "rollback succeeded") {
		t.Fatalf("error = %q, want rollback succeeded detail", err.Error())
	}
	operationsMu.Lock()
	gotOperations := slices.Clone(operations)
	operationsMu.Unlock()
	assertOperationsInOrder(t, gotOperations, []string{
		"create:new",
		"connect:secondary:new-id",
		"remove:new-id",
		"create:rollback",
		"start:rollback-id",
	})
}

func TestUpdateStandaloneContainerTreatsAmbiguousStartErrorAsSuccessWhenInspectRunning(t *testing.T) {
	var operations []string
	dockerClient := newDockerClientForHandler(t, func(w http.ResponseWriter, r *http.Request) {
		path := dockerAPIPath(r.URL.Path)
		switch {
		case r.Method == http.MethodPost && path == "/containers/old-id/stop":
			operations = append(operations, "stop:old-id")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete && path == "/containers/old-id":
			operations = append(operations, "remove:old-id")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && path == "/containers/create":
			operations = append(operations, "create:"+r.URL.Query().Get("name"))
			writeDockerJSON(t, w, map[string]any{"Id": "new-id", "Warnings": []string{}})
		case r.Method == http.MethodPost && path == "/containers/new-id/start":
			operations = append(operations, "start:new-id")
			closeHTTPConnection(t, w)
		case r.Method == http.MethodGet && path == "/containers/new-id/json":
			operations = append(operations, "inspect:new-id")
			writeDockerJSON(t, w, container.InspectResponse{
				ID:    "new-id",
				Name:  "/app",
				Image: "sha256:new-image",
				State: &container.State{Running: true, Status: container.StateRunning},
			})
		case r.Method == http.MethodDelete && path == "/containers/new-id":
			operations = append(operations, "remove:new-id")
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "unexpected path: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	})
	service := newService(Config{
		DockerClientProvider: &fakeDockerClientProvider{client: dockerClient},
	})

	err := service.updateComposeOrStandalone(t.Context(),
		container.Summary{ID: "old-id", Names: []string{"/app"}},
		container.InspectResponse{State: &container.State{Running: true}, ID: "old-id", Name: "/app", Image: "sha256:old-image", Config: &container.Config{Image: "app:1"}},
		"app:2",
	)
	if err != nil {
		t.Fatalf("updateComposeOrStandalone() error = %v, want nil after inspect confirms running", err)
	}
	assertOperationsInOrder(t, operations, []string{
		"start:new-id",
		"inspect:new-id",
	})
	for _, operation := range operations {
		if operation == "remove:new-id" {
			t.Fatalf("operations = %#v, did not expect removal after inspect confirms running", operations)
		}
	}
}

func TestUpdateStandaloneContainerRollsBackAmbiguousStartErrorWhenInspectNotRunning(t *testing.T) {
	var operations []string
	dockerClient := newDockerClientForHandler(t, func(w http.ResponseWriter, r *http.Request) {
		path := dockerAPIPath(r.URL.Path)
		switch {
		case r.Method == http.MethodPost && path == "/containers/old-id/stop":
			operations = append(operations, "stop:old-id")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete && path == "/containers/old-id":
			operations = append(operations, "remove:old-id")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete && path == "/containers/new-id":
			operations = append(operations, "remove:new-id")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && path == "/containers/create":
			var body container.Config
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			if body.Image == "app:2" {
				operations = append(operations, "create:new")
				writeDockerJSON(t, w, map[string]any{"Id": "new-id", "Warnings": []string{}})
				return
			}
			operations = append(operations, "create:rollback")
			writeDockerJSON(t, w, map[string]any{"Id": "rollback-id", "Warnings": []string{}})
		case r.Method == http.MethodPost && path == "/containers/new-id/start":
			operations = append(operations, "start:new-id")
			closeHTTPConnection(t, w)
		case r.Method == http.MethodGet && path == "/containers/new-id/json":
			operations = append(operations, "inspect:new-id")
			writeDockerJSON(t, w, container.InspectResponse{
				ID:    "new-id",
				Name:  "/app",
				Image: "sha256:new-image",
				State: &container.State{Running: false, Status: container.StateExited},
			})
		case r.Method == http.MethodPost && path == "/containers/rollback-id/start":
			operations = append(operations, "start:rollback-id")
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected path: "+r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	})
	service := newService(Config{
		DockerClientProvider: &fakeDockerClientProvider{client: dockerClient},
	})

	err := service.updateComposeOrStandalone(t.Context(),
		container.Summary{ID: "old-id", Names: []string{"/app"}},
		container.InspectResponse{State: &container.State{Running: true}, ID: "old-id", Name: "/app", Image: "sha256:old-image", Config: &container.Config{Image: "app:1"}},
		"app:2",
	)

	if err == nil {
		t.Fatal("updateComposeOrStandalone() error = nil, want ambiguous start failure with rollback outcome")
	}
	if !strings.Contains(err.Error(), "rollback succeeded") {
		t.Fatalf("error = %q, want rollback succeeded detail", err.Error())
	}
	assertOperationsInOrder(t, operations, []string{
		"start:new-id",
		"inspect:new-id",
		"remove:new-id",
		"create:rollback",
		"start:rollback-id",
	})
}

func TestServiceFallsBackToStandaloneWhenComposeProjectUnresolved(t *testing.T) {
	projectUpdater := &fakeProjectUpdater{projects: map[string]ComposeProject{}}
	service := newServiceForTest(t, Config{
		DockerClientProvider: &fakeDockerClientProvider{err: errors.New("no docker in test")},
		ProjectUpdater:       projectUpdater,
	})
	err := service.updateComposeOrStandalone(t.Context(), container.Summary{
		ID: "container-1",
	}, container.InspectResponse{
		Config: &container.Config{Image: "nginx:latest", Labels: map[string]string{
			"com.docker.compose.project": "app",
			"com.docker.compose.service": "web",
		}},
	}, "nginx:latest")
	// The unresolved project must route to the standalone path, which is the
	// first caller of the (failing) docker client in this setup.
	if err == nil || !strings.Contains(err.Error(), "docker connect") {
		t.Fatalf("updateComposeOrStandalone() error = %v, want standalone-path docker connect error", err)
	}
	if len(projectUpdater.updateCalls) != 0 {
		t.Fatalf("UpdateServices called %v times for unresolved project, want 0", len(projectUpdater.updateCalls))
	}
}

const tagSelectionContainerID, tagSelectionContainerName = "app", "web"

type tagSelectionScenario struct {
	name                string
	dry, force, self    bool
	current, constraint string
	// repository is the configured spelling of the current app:1.0.0 image.
	repository        string
	disabled, compose bool
	// excluded is a settings exclusion matching the container by name or ID;
	// override sets Options.IgnoreSettingsExclusions.
	excluded        string
	override        bool
	unchangedDigest bool
	wantFailure     bool
	wantSkipped     bool
}

func TestUpdateContainerTagSelection(t *testing.T) {
	const containerID, containerName = tagSelectionContainerID, tagSelectionContainerName
	for _, scenario := range []tagSelectionScenario{
		{name: "same digest still changes standalone reference"},
		{name: "dry run selects target", dry: true},
		{name: "self receives new tag", self: true},
		{name: "explicit docker hub spelling kept", repository: "docker.io/library/app"},
		{name: "custom registry spelling kept", repository: "ghcr.io/acme/app"},
		{name: "force respects constraint", force: true, constraint: "1.0.x"},
		{name: "force rejects invalid constraint", force: true, constraint: "bad", wantFailure: true},
		{name: "disabled force", force: true, disabled: true, wantSkipped: true},
		{name: "digest pin force", force: true, current: "app@sha256:" + strings.Repeat("a", 64), wantSkipped: true},
		{name: "image ID force", force: true, current: "sha256:" + strings.Repeat("a", 64), wantSkipped: true},
		{name: "compose unsupported before pull", compose: true, wantFailure: true},
		{name: "settings exclusion by name skips", excluded: containerName, wantSkipped: true},
		{name: "settings exclusion by ID skips", excluded: containerID, wantSkipped: true},
		{name: "force does not bypass settings exclusion", force: true, excluded: containerName, wantSkipped: true},
		{name: "override updates excluded name", excluded: containerName, override: true},
		{name: "override updates excluded ID", excluded: containerID, override: true},
		{name: "override previews excluded container", excluded: containerName, override: true, dry: true},
		{name: "override keeps disabled label", excluded: containerName, override: true, disabled: true, wantSkipped: true},
		{name: "override keeps digest pin", excluded: containerName, override: true, current: "app@sha256:" + strings.Repeat("a", 64), wantSkipped: true},
		{name: "override keeps image ID", excluded: containerName, override: true, current: "sha256:" + strings.Repeat("a", 64), wantSkipped: true},
		{name: "override rejects invalid constraint", excluded: containerName, override: true, constraint: "bad", wantFailure: true},
		{name: "override respects constraint", excluded: containerName, override: true, force: true, constraint: "1.0.x"},
		{name: "override keeps unchanged digest", excluded: containerName, override: true, unchangedDigest: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			runTagSelectionScenario(t, scenario)
		})
	}
}

// tagSelectionDocker serves a single container to the updater and
// records the images it recreates and every mutating request.
type tagSelectionDocker struct {
	current   string
	values    map[string]string
	created   []string
	mutations int
}

func newTagSelectionDocker(scenario tagSelectionScenario) *tagSelectionDocker {
	current := scenario.current
	if current == "" {
		current = cmp.Or(scenario.repository, "app") + ":1.0.0"
	}
	values := map[string]string{labels.LabelUpdateStrategy: "tag"}
	if scenario.unchangedDigest {
		values[labels.LabelUpdateStrategy] = "digest"
	}
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
	return &tagSelectionDocker{current: current, values: values}
}

func (d *tagSelectionDocker) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	const containerID, containerName = tagSelectionContainerID, tagSelectionContainerName
	return func(w http.ResponseWriter, r *http.Request) {
		path := dockerAPIPath(r.URL.Path)
		if r.Method != http.MethodGet {
			d.mutations++
		}
		switch {
		case path == "/containers/json":
			writeDockerJSON(t, w, []container.Summary{{ID: containerID, Names: []string{"/" + containerName}, Image: d.current, ImageID: "same", Labels: d.values}})
		case path == "/containers/"+containerID+"/json":
			writeDockerJSON(t, w, container.InspectResponse{
				State: &container.State{Running: true},
				ID:    containerID, Name: "/" + containerName, Image: "same",
				Config: &container.Config{Image: d.current, Labels: d.values}, HostConfig: &container.HostConfig{},
			})
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
			d.created = append(d.created, config.Image)
			writeDockerJSON(t, w, map[string]string{"Id": "new"})
		case strings.HasSuffix(path, "/stop") || strings.HasSuffix(path, "/start") || r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "unexpected "+path, http.StatusNotFound)
		}
	}
}

func runTagSelectionScenario(t *testing.T, scenario tagSelectionScenario) {
	t.Helper()
	docker := newTagSelectionDocker(scenario)
	dockerClient := newDockerClientForHandler(t, docker.handler(t))
	puller := &fakePuller{}
	lister := &testTagLister{tags: []string{"1.1.0", "2.0.0"}}
	self := &fakeSelfUpdater{}
	settings := &fakeSettings{}
	if scenario.excluded != "" {
		settings.excluded = []string{scenario.excluded}
	}
	cfg := Config{DockerClientProvider: &fakeDockerClientProvider{client: dockerClient}, RegistryTagLister: lister, ImagePuller: puller, SelfUpdater: self, Settings: settings}
	if scenario.self {
		cfg.SelfContainerID = tagSelectionContainerID
	}
	service := newServiceForTest(t, cfg)
	result, err := service.UpdateContainer(t.Context(), tagSelectionContainerID, Options{DryRun: scenario.dry, Force: scenario.force, IgnoreSettingsExclusions: scenario.override})
	if err != nil {
		t.Fatal(err)
	}
	if scenario.excluded != "" && (len(settings.excluded) != 1 || settings.excluded[0] != scenario.excluded) {
		t.Fatalf("settings exclusion changed: %v", settings.excluded)
	}
	if scenario.wantFailure {
		if result.Failed != 1 || docker.mutations != 0 || len(puller.pulled) != 0 {
			t.Fatalf("expected safe failure: %+v, pulls %v", result, puller.pulled)
		}
		return
	}
	if scenario.wantSkipped {
		if result.Skipped != 1 || lister.calls != 0 || docker.mutations != 0 || len(puller.pulled) != 0 {
			t.Fatalf("expected ineligible: %+v", result)
		}
		return
	}
	if scenario.unchangedDigest {
		if result.Skipped != 1 || result.Updated != 0 || docker.mutations != 0 || len(puller.pulled) != 1 {
			t.Fatalf("expected unchanged image to be left alone: %+v, pulls %v", result, puller.pulled)
		}
		return
	}
	assertTagSelectionUpdated(t, scenario, result, docker, puller, self)
}

func assertTagSelectionUpdated(t *testing.T, scenario tagSelectionScenario, result *Result, docker *tagSelectionDocker, puller *fakePuller, self *fakeSelfUpdater) {
	t.Helper()
	wantCreated := cmp.Or(scenario.repository, "app") + ":1.1.0"
	if scenario.constraint != "" {
		wantCreated = cmp.Or(scenario.repository, "app") + ":1.0.0"
	}
	want := refs.NormalizeImageUpdateRef(wantCreated)
	if scenario.dry {
		if len(result.Items) != 1 || result.Items[0].NewImage != want || !result.Items[0].UpdateAvailable || docker.mutations != 0 || len(puller.pulled) != 0 {
			t.Fatalf("bad preview: %+v", result)
		}
		return
	}
	if result.Updated != 1 || len(puller.pulled) != 1 || puller.pulled[0] != want {
		t.Fatalf("result %+v, pulls %v", result, puller.pulled)
	}
	if scenario.self {
		if len(self.targets) != 1 || self.targets[0].NewImageRef != want || docker.mutations != 0 {
			t.Fatalf("bad self targets %+v", self.targets)
		}
	} else if len(docker.created) != 1 || docker.created[0] != wantCreated {
		t.Fatalf("created %v", docker.created)
	}
}
