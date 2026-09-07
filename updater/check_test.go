package updater

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"go.getarcane.app/updater/labels"
	"go.getarcane.app/updater/types"
)

type testTagLister struct {
	tags  []string
	err   error
	calls int
}

func (l *testTagLister) ListTags(ctx context.Context, _ string) ([]string, error) {
	l.calls++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return l.tags, l.err
}

func TestCheckTagLabelDefaultsAndOverrides(t *testing.T) {
	values := map[string]string{labels.LabelUpdateStrategy: " tag ", labels.LabelUpdateConstraint: " 3.x ", labels.LabelUpdateTagPattern: ` (?P<version>.*)-alpine `}
	want := types.Policy{Strategy: "tag", Constraint: "3.x", TagPattern: `(?P<version>.*)-alpine`}
	merged := mergeLabelPolicyDefaults(LabelPolicy{IsAgentFunc: func(map[string]string) bool { return true }})
	if got := merged.TagPolicy(values); got != want {
		t.Fatalf("TagPolicy = %+v, want %+v", got, want)
	}
	override := types.Policy{Strategy: "tag", Constraint: "4.x"}
	merged = mergeLabelPolicyDefaults(LabelPolicy{TagPolicyFunc: func(map[string]string) types.Policy { return override }})
	if got := merged.TagPolicy(values); got != override {
		t.Fatalf("override lost: %+v", got)
	}
	if !merged.IsUpdateDisabled(map[string]string{labels.LabelUpdater: "off"}) {
		t.Fatal("default disabled policy lost")
	}
	if got := DefaultLabelPolicy().TagPolicy(nil); got != (types.Policy{}) {
		t.Fatalf("unlabeled policy = %+v", got)
	}
}

func TestCheckImageTagCandidateWithoutDockerOrMutation(t *testing.T) {
	lister := &testTagLister{tags: []string{"3.1.10", "4.0.0"}}
	provider := &fakeDockerClientProvider{err: errors.New("unexpected Docker call")}
	resolver := &countingDigestResolver{}
	puller := &fakePuller{}
	store := &fakePendingStore{}
	service := newServiceForTest(t, Config{RegistryTagLister: lister, DockerClientProvider: provider, RegistryDigestResolver: resolver, ImagePuller: puller, PendingStore: store})
	result, err := service.CheckImageUpdate(t.Context(), types.CheckRequest{ImageRef: "example/app:3.1.9", Policy: types.Policy{Strategy: "tag"}})
	if err != nil {
		t.Fatal(err)
	}
	if !result.UpdateAvailable || result.UpdateType != string(UpdateTypeTag) || result.TargetRef != "docker.io/example/app:3.1.10" || result.CurrentVersion != "3.1.9" || result.TargetVersion != "3.1.10" {
		t.Fatalf("unexpected candidate: %+v", result)
	}
	if provider.calls != 0 || resolver.calls != 0 || len(puller.pulled) != 0 || len(store.cleared) != 0 || len(store.records) != 0 {
		t.Fatal("tag discovery performed unrelated operations")
	}
}

func TestCheckImageDigestFallback(t *testing.T) {
	for _, strategy := range []string{"", "digest", "tag"} {
		t.Run(strategy, func(t *testing.T) {
			local := "sha256:" + strings.Repeat("a", 64)
			remote := "sha256:" + strings.Repeat("b", 64)
			resolver := &countingDigestResolver{digest: remote}
			provider := &fakeDockerClientProvider{err: errors.New("unexpected Docker call")}
			lister := &testTagLister{tags: []string{"3.1.9", "3.0.0"}}
			service := newServiceForTest(t, Config{RegistryTagLister: lister, RegistryDigestResolver: resolver, DockerClientProvider: provider})
			request := types.CheckRequest{ImageRef: "example/app:3.1.9", CurrentDigest: local, Policy: types.Policy{Strategy: strategy}}
			result, err := service.CheckImageUpdate(t.Context(), request)
			if err != nil {
				t.Fatal(err)
			}
			if !result.UpdateAvailable || result.UpdateType != string(UpdateTypeDigest) || result.CurrentDigest != local || result.TargetDigest != remote || result.TargetRef != result.CurrentRef {
				t.Fatalf("unexpected digest result: %+v", result)
			}
			if provider.calls != 0 {
				t.Fatal("explicit digest consulted Docker")
			}
			resolver.digest = local
			result, err = service.CheckImageUpdate(t.Context(), request)
			if err != nil || result.UpdateAvailable {
				t.Fatalf("matching digest result: %+v, %v", result, err)
			}
		})
	}
}

func TestCheckImageErrorsAndImmutableReferences(t *testing.T) {
	t.Run("immutable bypasses discovery", func(t *testing.T) {
		lister := &testTagLister{err: errors.New("unexpected registry call")}
		resolver := &countingDigestResolver{}
		provider := &fakeDockerClientProvider{err: errors.New("unexpected Docker call")}
		service := newServiceForTest(t, Config{RegistryTagLister: lister, RegistryDigestResolver: resolver, DockerClientProvider: provider})
		for _, ref := range []string{"example/app@sha256:" + strings.Repeat("a", 64), "sha256:" + strings.Repeat("b", 64), strings.Repeat("c", 64)} {
			result, err := service.CheckImageUpdate(t.Context(), types.CheckRequest{ImageRef: ref, Policy: types.Policy{Strategy: "tag", Constraint: "invalid"}})
			if err != nil || result.UpdateAvailable || result.Reason == "" {
				t.Fatalf("immutable result %+v, %v", result, err)
			}
		}
		if lister.calls != 0 || resolver.calls != 0 || provider.calls != 0 {
			t.Fatal("immutable reference performed network operations")
		}
	})
	for _, policy := range []types.Policy{{Strategy: "invalid"}, {Strategy: "tag", Constraint: "invalid"}, {Strategy: "tag", TagPattern: "["}} {
		t.Run(policy.Strategy+policy.Constraint+policy.TagPattern, func(t *testing.T) {
			lister := &testTagLister{}
			service := newServiceForTest(t, Config{RegistryTagLister: lister})
			if _, err := service.CheckImageUpdate(t.Context(), types.CheckRequest{ImageRef: "example/app:3.1.9", Policy: policy}); err == nil {
				t.Fatal("expected policy error")
			}
			if lister.calls != 0 {
				t.Fatal("invalid policy reached registry")
			}
		})
	}
	t.Run("listing failure", func(t *testing.T) {
		sentinel := errors.New("registry rate limit")
		service := newServiceForTest(t, Config{RegistryTagLister: &testTagLister{err: sentinel}})
		result, err := service.CheckImageUpdate(t.Context(), types.CheckRequest{ImageRef: "example/app:3.1.9", Policy: types.Policy{Strategy: "tag"}})
		if !errors.Is(err, sentinel) || result.UpdateAvailable {
			t.Fatalf("result %+v, error %v", result, err)
		}
	})
	t.Run("cancelled check", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		lister := &testTagLister{}
		service := newServiceForTest(t, Config{RegistryTagLister: lister})
		_, err := service.CheckImageUpdate(ctx, types.CheckRequest{ImageRef: "example/app:3.1.9", Policy: types.Policy{Strategy: "tag"}})
		if !errors.Is(err, context.Canceled) || lister.calls != 0 {
			t.Fatalf("error = %v, listing calls = %d", err, lister.calls)
		}
	})
	t.Run("invalid current digest", func(t *testing.T) {
		resolver := &countingDigestResolver{}
		service := newServiceForTest(t, Config{RegistryDigestResolver: resolver})
		_, err := service.CheckImageUpdate(t.Context(), types.CheckRequest{ImageRef: "example/app:3.1.9", CurrentDigest: "not-a-digest"})
		if err == nil || resolver.calls != 0 {
			t.Fatalf("error = %v, resolver calls = %d", err, resolver.calls)
		}
	})
	t.Run("resolver failure", func(t *testing.T) {
		sentinel := errors.New("registry authentication failed")
		service := newServiceForTest(t, Config{RegistryDigestResolver: &countingDigestResolver{err: sentinel}})
		_, err := service.CheckImageUpdate(t.Context(), types.CheckRequest{ImageRef: "example/app:3.1.9", CurrentDigest: "sha256:" + strings.Repeat("a", 64)})
		if !errors.Is(err, sentinel) {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestCheckContainerUsesRunningImageDigest(t *testing.T) {
	local := "sha256:" + strings.Repeat("a", 64)
	remote := "sha256:" + strings.Repeat("b", 64)
	dockerClient := newDockerClientForHandler(t, func(w http.ResponseWriter, r *http.Request) {
		switch dockerAPIPath(r.URL.Path) {
		case "/containers/target/json":
			writeDockerJSON(t, w, map[string]any{"Id": "target", "Image": "running-image", "Config": map[string]any{"Image": "example/app:latest"}})
		case "/images/running-image/json":
			writeDockerJSON(t, w, map[string]any{"Id": "running-image", "RepoDigests": []string{"example/app@" + local}})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	service := newServiceForTest(t, Config{DockerClientProvider: &fakeDockerClientProvider{client: dockerClient}, RegistryDigestResolver: &countingDigestResolver{digest: remote}})
	result, err := service.CheckContainerUpdate(t.Context(), "target")
	if err != nil {
		t.Fatal(err)
	}
	if !result.UpdateAvailable || result.ContainerID != "target" || result.CurrentDigest != local || result.TargetDigest != remote {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCheckContainerCandidatesAreScoped(t *testing.T) {
	dockerClient := newDockerClientForHandler(t, func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSuffix(strings.TrimPrefix(dockerAPIPath(r.URL.Path), "/containers/"), "/json")
		constraint := "3.x"
		if id == "second" {
			constraint = "4.x"
		}
		containerLabels := map[string]string{labels.LabelUpdateStrategy: "tag", labels.LabelUpdateConstraint: constraint}
		if id == "disabled" {
			containerLabels[labels.LabelUpdater] = "off"
		}
		writeDockerJSON(t, w, map[string]any{"Id": id, "Image": "shared-image", "Config": map[string]any{"Image": "example/app:3.1.9", "Labels": containerLabels}})
	})
	lister := &testTagLister{tags: []string{"3.1.10", "4.0.0"}}
	service := newServiceForTest(t, Config{DockerClientProvider: &fakeDockerClientProvider{client: dockerClient}, RegistryTagLister: lister})
	for id, target := range map[string]string{"first": "3.1.10", "second": "4.0.0"} {
		result, err := service.CheckContainerUpdate(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		if result.ContainerID != id || !result.UpdateAvailable || result.TargetRef != "docker.io/example/app:"+target {
			t.Fatalf("unexpected result: %+v", result)
		}
	}
	result, err := service.CheckContainerUpdate(t.Context(), "disabled")
	if err != nil || result.Reason == "" || result.UpdateAvailable {
		t.Fatalf("disabled result %+v, %v", result, err)
	}
	if lister.calls != 2 {
		t.Fatalf("listing calls = %d, want 2", lister.calls)
	}
}
