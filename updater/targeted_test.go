package updater

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
	"go.getarcane.app/updater/internal/compose"
	"go.getarcane.app/updater/labels"
	updatetypes "go.getarcane.app/updater/types"
)

type targetedDockerInternal struct {
	mu         sync.Mutex
	containers []container.InspectResponse
	created    map[string]string
	mutations  int
}

func newTargetedContainerInternal(id, constraint string) container.InspectResponse {
	return container.InspectResponse{ID: id, Name: "/" + id, Image: "sha256:shared", State: &container.State{Running: true}, Config: &container.Config{Image: "app:1.0.0", Labels: map[string]string{labels.LabelUpdateStrategy: "tag", labels.LabelUpdateConstraint: constraint}}}
}

func targetedPendingInternal(id, target string) ImageUpdateRecord {
	return ImageUpdateRecord{ID: "sha256:shared", ContainerID: id, Repository: "app", Tag: "1.0.0", LatestVersion: &target, HasUpdate: true, UpdateType: UpdateTypeTag}
}

func (f *targetedDockerInternal) handlerInternal(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		path := dockerAPIPath(r.URL.Path)
		switch {
		case r.Method == http.MethodGet && path == "/containers/json":
			var summaries []container.Summary
			for _, cnt := range f.containers {
				filter := r.URL.Query().Get("filters")
				if filter != "" && !strings.Contains(filter, compose.ServiceLabelKey+"="+cnt.Config.Labels[compose.ServiceLabelKey]) {
					continue
				}
				state := container.StateExited
				if cnt.State != nil && cnt.State.Running {
					state = container.StateRunning
				}
				summaries = append(summaries, container.Summary{ID: cnt.ID, Names: []string{cnt.Name}, Image: cnt.Config.Image, ImageID: cnt.Image, Labels: cnt.Config.Labels, State: state})
			}
			writeDockerJSON(t, w, summaries)
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json"):
			id := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json")
			for _, cnt := range f.containers {
				if cnt.ID == id {
					writeDockerJSON(t, w, cnt)
					return
				}
			}
			http.NotFound(w, r)
		case r.Method == http.MethodGet && strings.HasPrefix(path, "/images/"):
			// Every target is already cached and has the exact same image ID as the old tag.
			writeDockerJSON(t, w, image.InspectResponse{ID: "sha256:shared"})
		case r.Method == http.MethodPost && path == "/containers/create":
			var config container.Config
			if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
				t.Error(err)
				http.Error(w, "decode", 400)
				return
			}
			f.mutations++
			f.created[r.URL.Query().Get("name")] = config.Image
			writeDockerJSON(t, w, map[string]any{"Id": "created-" + r.URL.Query().Get("name")})
		case r.Method == http.MethodPost || r.Method == http.MethodDelete:
			f.mutations++
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request %s %s", r.Method, path)
			http.NotFound(w, r)
		}
	}
}

func TestApplyPendingTargetedSharedImageInternal(t *testing.T) {
	for _, tt := range []struct {
		name, secondTarget, secondConstraint string
		wantPulls                            int
		dryRun                               bool
	}{
		{name: "separate version ranges", secondTarget: "2.1.0", secondConstraint: "2.x", wantPulls: 2},
		{name: "deduplicate shared target", secondTarget: "1.2.0", secondConstraint: "1.x", wantPulls: 1},
		{name: "dry run", secondTarget: "2.1.0", secondConstraint: "2.x", dryRun: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := &targetedDockerInternal{containers: []container.InspectResponse{newTargetedContainerInternal("one", "1.x"), newTargetedContainerInternal("two", tt.secondConstraint)}, created: map[string]string{}}
			dockerClient := newDockerClientForHandler(t, fixture.handlerInternal(t))
			store := NewMemoryPendingStore(targetedPendingInternal("one", "1.2.0"), targetedPendingInternal("two", tt.secondTarget))
			puller := &fakePuller{}
			service := newService(Config{DockerClientProvider: &fakeDockerClientProvider{client: dockerClient}, PendingStore: store, ImagePuller: puller})
			result, err := service.ApplyPending(context.Background(), Options{DryRun: tt.dryRun})
			if err != nil || result.Failed != 0 {
				t.Fatalf("ApplyPending = %#v, %v", result, err)
			}
			if len(puller.pulled) != tt.wantPulls {
				t.Fatalf("pulls = %#v", puller.pulled)
			}
			pending, err := store.PendingImageUpdates(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if tt.dryRun {
				if fixture.mutations != 0 || len(pending) != 2 || len(result.Items) != 2 {
					t.Fatalf("dryrun mutated state or lost records: mutations %d pending %d result %#v", fixture.mutations, len(pending), result)
				}
				return
			}
			if len(pending) != 0 || len(fixture.created) != 2 || fixture.created["one"] != "docker.io/library/app:1.2.0" || fixture.created["two"] != "docker.io/library/app:"+tt.secondTarget {
				t.Fatalf("pending=%d created=%#v result=%#v", len(pending), fixture.created, result)
			}
		})
	}
}

func TestApplyPendingTargetedRetainsRejectedInternal(t *testing.T) {
	for _, tt := range []struct {
		name      string
		change    func(*container.InspectResponse)
		pullError bool
		wantPulls int
	}{
		{name: "disabled", change: func(c *container.InspectResponse) { c.Config.Labels[labels.LabelUpdater] = "false" }},
		{name: "digest pin", change: func(c *container.InspectResponse) { c.Config.Image = "app@sha256:" + strings.Repeat("a", 64) }},
		{name: "image ID", change: func(c *container.InspectResponse) { c.Config.Image = "sha256:" + strings.Repeat("a", 64) }},
		{name: "changed reference", change: func(c *container.InspectResponse) { c.Config.Image = "app:1.1.0" }},
		{name: "changed constraint", change: func(c *container.InspectResponse) { c.Config.Labels[labels.LabelUpdateConstraint] = "<1.1.0" }},
		{name: "changed strategy", change: func(c *container.InspectResponse) { c.Config.Labels[labels.LabelUpdateStrategy] = "digest" }},
		{name: "unsupported compose", change: func(c *container.InspectResponse) {
			c.Config.Labels[compose.ProjectLabelKey] = "app"
			c.Config.Labels[compose.ServiceLabelKey] = "web"
		}},
		{name: "pull failure", pullError: true, wantPulls: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cnt := newTargetedContainerInternal("one", "1.x")
			if tt.change != nil {
				tt.change(&cnt)
			}
			fixture := &targetedDockerInternal{containers: []container.InspectResponse{cnt}, created: map[string]string{}}
			dockerClient := newDockerClientForHandler(t, fixture.handlerInternal(t))
			store := NewMemoryPendingStore(targetedPendingInternal("one", "1.2.0"))
			puller := &fakePuller{}
			if tt.pullError {
				puller.err = errors.New("registry unavailable")
			}
			service := newService(Config{DockerClientProvider: &fakeDockerClientProvider{client: dockerClient}, PendingStore: store, ImagePuller: puller})
			result, err := service.ApplyPending(context.Background(), Options{Force: true})
			if err != nil {
				t.Fatal(err)
			}
			pending, err := store.PendingImageUpdates(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if result.Failed != 1 || len(pending) != 1 || len(puller.pulled) != tt.wantPulls || fixture.mutations != 0 {
				t.Fatalf("result=%#v pending=%d pulls=%#v mutations=%d", result, len(pending), puller.pulled, fixture.mutations)
			}
		})
	}
}

type targetedComposeAdapterInternal struct {
	fakeProjectUpdater
	fixture *targetedDockerInternal
	calls   int
	changes map[string]updatetypes.ServiceImageChange
	fail    bool
}

func (f *targetedComposeAdapterInternal) UpdateServiceImages(_ context.Context, projectID string, changes map[string]updatetypes.ServiceImageChange) error {
	f.calls++
	f.changes = changes
	if f.fail {
		return errors.New("persist failed")
	}
	f.fixture.mu.Lock()
	defer f.fixture.mu.Unlock()
	for i := range f.fixture.containers {
		cnt := &f.fixture.containers[i]
		if change, ok := changes[cnt.Config.Labels[compose.ServiceLabelKey]]; ok {
			cnt.Config.Image = change.TargetRef
		}
	}
	return nil
}

func TestApplyPendingTargetedComposeGroupsInternal(t *testing.T) {
	for _, tt := range []struct {
		name           string
		conflict, fail bool
	}{
		{name: "group service changes"}, {name: "conflicting service targets", conflict: true}, {name: "adapter failure retained", fail: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := &targetedDockerInternal{containers: []container.InspectResponse{newTargetedContainerInternal("one", "1.x"), newTargetedContainerInternal("two", "1.x")}, created: map[string]string{}}
			for i := range fixture.containers {
				fixture.containers[i].Config.Labels[compose.ProjectLabelKey] = "app"
				fixture.containers[i].Config.Labels[compose.ServiceLabelKey] = fixture.containers[i].ID
			}
			if tt.conflict {
				fixture.containers[1].Config.Labels[compose.ServiceLabelKey] = "one"
			}
			dockerClient := newDockerClientForHandler(t, fixture.handlerInternal(t))
			adapter := &targetedComposeAdapterInternal{fakeProjectUpdater: fakeProjectUpdater{projects: map[string]ComposeProject{"app": {ID: "project-app"}}}, fixture: fixture, fail: tt.fail}
			store := NewMemoryPendingStore(targetedPendingInternal("one", "1.2.0"), targetedPendingInternal("two", "1.3.0"))
			puller := &fakePuller{}
			service := newService(Config{DockerClientProvider: &fakeDockerClientProvider{client: dockerClient}, PendingStore: store, ImagePuller: puller, ProjectUpdater: adapter})
			result, err := service.ApplyPending(context.Background(), Options{})
			pending, storeErr := store.PendingImageUpdates(context.Background())
			if storeErr != nil {
				t.Fatal(storeErr)
			}
			if tt.conflict {
				if err == nil || len(puller.pulled) != 0 || adapter.calls != 0 || len(pending) != 2 {
					t.Fatalf("conflict not blocked: result=%#v err=%v pulls=%#v calls=%d pending=%d", result, err, puller.pulled, adapter.calls, len(pending))
				}
				return
			}
			if err != nil || adapter.calls != 1 || len(adapter.changes) != 2 {
				t.Fatalf("group dispatch result=%#v err=%v calls=%d changes=%#v", result, err, adapter.calls, adapter.changes)
			}
			if tt.fail {
				if len(pending) != 2 || result.Failed != 2 {
					t.Fatalf("failed records lost result=%#v pending=%d", result, len(pending))
				}
				return
			}
			if len(pending) != 0 || result.Updated != 2 || fixture.mutations != 0 {
				t.Fatalf("result=%#v pending=%d mutations=%d", result, len(pending), fixture.mutations)
			}
			if adapter.changes["one"].ExpectedRef != "app:1.0.0" || adapter.changes["one"].TargetRef != "docker.io/library/app:1.2.0" {
				t.Fatalf("wrong persistence changes %#v", adapter.changes)
			}
		})
	}
}

func TestApplyPendingTargetedScopedClearingInternal(t *testing.T) {
	fixture := &targetedDockerInternal{containers: []container.InspectResponse{newTargetedContainerInternal("one", "1.x"), newTargetedContainerInternal("two", "<1.1.0")}, created: map[string]string{}}
	// One record is already applied. Its successful verification must not clear
	// the rejected record for a different container using the same image ID.
	fixture.containers[0].Config.Image = "app:1.2.0"
	dockerClient := newDockerClientForHandler(t, fixture.handlerInternal(t))
	store := NewMemoryPendingStore(targetedPendingInternal("one", "1.2.0"), targetedPendingInternal("two", "1.2.0"), targetedPendingInternal("missing", "1.2.0"))
	puller := &fakePuller{}
	service := newService(Config{DockerClientProvider: &fakeDockerClientProvider{client: dockerClient}, PendingStore: store, ImagePuller: puller})
	result, err := service.ApplyPending(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingImageUpdates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 2 || result.Failed != 2 || len(puller.pulled) != 1 {
		t.Fatalf("result=%#v pending=%#v pulls=%#v", result, pending, puller.pulled)
	}
	for _, record := range pending {
		if record.ContainerID == "one" {
			t.Fatal("verified record remains pending")
		}
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.mutations != 0 {
		t.Fatalf("already configured target recreated: mutations=%d", fixture.mutations)
	}
}

type selectiveTargetPullerInternal struct{ failedRef string }

func (p selectiveTargetPullerInternal) PullImage(_ context.Context, ref string, _ io.Writer) error {
	if ref == p.failedRef {
		return errors.New("target unavailable")
	}
	return nil
}

func TestApplyPendingTargetedFailedComposeDependencyRetainedInternal(t *testing.T) {
	fixture := &targetedDockerInternal{containers: []container.InspectResponse{newTargetedContainerInternal("one", "1.x"), newTargetedContainerInternal("two", "1.x")}, created: map[string]string{}}
	for i := range fixture.containers {
		fixture.containers[i].Config.Labels[compose.ProjectLabelKey] = "app"
		fixture.containers[i].Config.Labels[compose.ServiceLabelKey] = fixture.containers[i].ID
	}
	fixture.containers[1].Config.Labels[labels.LabelDependsOn] = "one"
	dockerClient := newDockerClientForHandler(t, fixture.handlerInternal(t))
	adapter := &targetedComposeAdapterInternal{fakeProjectUpdater: fakeProjectUpdater{projects: map[string]ComposeProject{"app": {ID: "project-app"}}}, fixture: fixture}
	store := NewMemoryPendingStore(targetedPendingInternal("one", "1.2.0"), targetedPendingInternal("two", "1.3.0"))
	service := newService(Config{DockerClientProvider: &fakeDockerClientProvider{client: dockerClient}, PendingStore: store, ImagePuller: selectiveTargetPullerInternal{failedRef: "docker.io/library/app:1.3.0"}, ProjectUpdater: adapter})
	result, err := service.ApplyPending(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingImageUpdates(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ContainerID != "two" {
		t.Fatalf("failed update record cleared by dependency restart: pending=%#v results=%#v", pending, result)
	}
	if adapter.calls != 1 || adapter.changes["two"].TargetRef != "app:1.0.0" {
		t.Fatalf("dependency changed image: changes=%#v calls=%d", adapter.changes, adapter.calls)
	}
	var failed, restarted bool
	for _, item := range result.Items {
		if item.ResourceID == "two" {
			failed = failed || item.Status == StatusFailed
			restarted = restarted || item.Status == StatusRestarted
		}
	}
	if !failed || !restarted {
		t.Fatalf("missing independent failure/restart outcomes: %#v", result)
	}
}

func TestApplyPendingTargetedExcludedContainersInternal(t *testing.T) {
	for _, name := range []string{"stopped", "docker-proxy"} {
		t.Run(name, func(t *testing.T) {
			cnt := newTargetedContainerInternal(name, "1.x")
			if name == "stopped" {
				cnt.State.Running = false
			}
			fixture := &targetedDockerInternal{containers: []container.InspectResponse{cnt}, created: map[string]string{}}
			server := httptest.NewServer(fixture.handlerInternal(t))
			defer server.Close()
			dockerClient, err := client.New(client.WithHost("tcp://docker-proxy:2375"), client.WithAPIVersion("1.41"), client.WithDialContext(func(ctx context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			}))
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := dockerClient.Close(); err != nil {
					t.Error(err)
				}
			}()
			store := NewMemoryPendingStore(targetedPendingInternal(name, "1.2.0"))
			puller := &fakePuller{}
			service := newService(Config{DockerClientProvider: &fakeDockerClientProvider{client: dockerClient}, PendingStore: store, ImagePuller: puller})
			result, err := service.ApplyPending(context.Background(), Options{Force: true})
			if err != nil {
				t.Fatal(err)
			}
			pending, err := store.PendingImageUpdates(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if len(pending) != 1 || len(puller.pulled) != 0 || fixture.mutations != 0 || result.Updated != 0 {
				t.Fatalf("excluded container mutated: pending=%#v pulls=%#v mutations=%d result=%#v", pending, puller.pulled, fixture.mutations, result)
			}
		})
	}
}

func TestApplyPendingTargetedSatisfiedDependencyClearedInternal(t *testing.T) {
	fixture := &targetedDockerInternal{containers: []container.InspectResponse{newTargetedContainerInternal("one", "1.x"), newTargetedContainerInternal("two", "1.x")}, created: map[string]string{}}
	fixture.containers[1].Config.Image = "app:1.3.0"
	fixture.containers[1].Config.Labels[labels.LabelDependsOn] = "one"
	dockerClient := newDockerClientForHandler(t, fixture.handlerInternal(t))
	store := NewMemoryPendingStore(targetedPendingInternal("one", "1.2.0"), targetedPendingInternal("two", "1.3.0"))
	service := newService(Config{DockerClientProvider: &fakeDockerClientProvider{client: dockerClient}, PendingStore: store, ImagePuller: &fakePuller{}})
	result, err := service.ApplyPending(t.Context(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingImageUpdates(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var satisfied, restarted bool
	for _, item := range result.Items {
		if item.ResourceID == "two" {
			satisfied = satisfied || item.Status == StatusUpToDate
			restarted = restarted || item.Status == StatusRestarted
		}
	}
	if len(pending) != 0 || !satisfied || !restarted {
		t.Fatalf("pending=%+v result=%+v", pending, result)
	}
}

func TestApplyPendingAutomaticTagPolicyInternal(t *testing.T) {
	for _, strategy := range []string{"", "auto", "digest"} {
		t.Run(strategy, func(t *testing.T) {
			cnt := newTargetedContainerInternal("one", "")
			cnt.Config.Labels = map[string]string{}
			if strategy != "" {
				cnt.Config.Labels[labels.LabelUpdateStrategy] = strategy
			}
			fixture := &targetedDockerInternal{containers: []container.InspectResponse{cnt}, created: map[string]string{}}
			dockerClient := newDockerClientForHandler(t, fixture.handlerInternal(t))
			store := NewMemoryPendingStore(targetedPendingInternal("one", "1.2.0"))
			puller := &fakePuller{}
			service := newService(Config{DockerClientProvider: &fakeDockerClientProvider{client: dockerClient}, PendingStore: store, ImagePuller: puller})
			result, err := service.ApplyPending(t.Context(), Options{})
			if err != nil {
				t.Fatal(err)
			}
			pending, err := store.PendingImageUpdates(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if strategy == "digest" {
				if result.Failed == 0 || len(puller.pulled) != 0 || len(pending) != 1 || fixture.mutations != 0 {
					t.Fatalf("digest override was not respected: %+v", result)
				}
				return
			}
			if result.Failed != 0 || len(puller.pulled) != 1 || len(pending) != 0 || fixture.created["one"] != "docker.io/library/app:1.2.0" {
				t.Fatalf("automatic update failed: result %+v, created %v, pending %v", result, fixture.created, pending)
			}
		})
	}
}
