package compat

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

type fakeClient struct {
	client.APIClient
	createResult       client.ContainerCreateResult
	createErr          error
	connectErrs        map[string]error
	removeErr          error
	createCalls        []client.ContainerCreateOptions
	connectCalls       []string
	removeCalls        []client.ContainerRemoveOptions
	clientVersionCalls int
	serverVersionCalls int
}

func (f *fakeClient) ClientVersion() string {
	f.clientVersionCalls++
	return ""
}

func (f *fakeClient) ServerVersion(context.Context, client.ServerVersionOptions) (client.ServerVersionResult, error) {
	f.serverVersionCalls++
	return client.ServerVersionResult{}, nil
}

func (f *fakeClient) ContainerCreate(_ context.Context, options client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
	f.createCalls = append(f.createCalls, options)
	return f.createResult, f.createErr
}

func (f *fakeClient) NetworkConnect(_ context.Context, name string, _ client.NetworkConnectOptions) (client.NetworkConnectResult, error) {
	f.connectCalls = append(f.connectCalls, name)
	return client.NetworkConnectResult{}, f.connectErrs[name]
}

func (f *fakeClient) ContainerRemove(_ context.Context, _ string, options client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	f.removeCalls = append(f.removeCalls, options)
	return client.ContainerRemoveResult{}, f.removeErr
}

func mustMAC(t *testing.T, addr string) network.HardwareAddr {
	t.Helper()
	parsed, err := net.ParseMAC(addr)
	if err != nil {
		t.Fatal(err)
	}
	return network.HardwareAddr(parsed)
}

func TestIsDockerAPIVersionAtLeast(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, current, minimum string
		want                   bool
	}{
		{name: "equal", current: "1.44", minimum: "1.44", want: true},
		{name: "greater minor", current: "1.45", minimum: "1.44", want: true},
		{name: "lesser minor", current: "1.43", minimum: "1.44"},
		{name: "patch still greater", current: "1.44.1", minimum: "1.44", want: true},
		{name: "podman api", current: "1.41", minimum: "1.44"},
		{name: "trims v prefix", current: "v1.44", minimum: "1.44", want: true},
		{name: "invalid current", current: "invalid", minimum: "1.44"},
		{name: "invalid minimum", current: "1.44", minimum: "invalid"},
		{name: "single segment", current: "2", minimum: "1.44"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsDockerAPIVersionAtLeast(tt.current, tt.minimum); got != tt.want {
				t.Errorf("IsDockerAPIVersionAtLeast(%q, %q) = %t, want %t", tt.current, tt.minimum, got, tt.want)
			}
		})
	}
}

func TestSanitizeContainerCreateEndpointSettingsForDockerAPI(t *testing.T) {
	t.Parallel()

	input := map[string]*network.EndpointSettings{
		"bridge": {
			Aliases:    []string{"svc", "svc-1"},
			IPAddress:  netip.MustParseAddr("172.17.0.2"),
			MacAddress: mustMAC(t, "02:42:ac:11:00:02"),
		},
		"custom": {MacAddress: mustMAC(t, "02:42:ac:11:00:03"), IPAddress: netip.MustParseAddr("10.0.0.10")},
		"nilled": nil,
	}

	out := SanitizeContainerCreateEndpointSettingsForDockerAPI(input, "1.43")
	if len(out) != 3 || out["nilled"] != nil {
		t.Fatalf("sanitized = %v, want three entries with nil preserved", out)
	}
	if out["bridge"].MacAddress.String() != "" || out["custom"].MacAddress.String() != "" {
		t.Error("MAC addresses must be stripped below API 1.44")
	}
	if out["bridge"].IPAddress != netip.MustParseAddr("172.17.0.2") || !slices.Equal(out["bridge"].Aliases, []string{"svc", "svc-1"}) {
		t.Error("other endpoint fields must be preserved")
	}
	out["bridge"].Aliases[0] = "changed"
	if input["bridge"].MacAddress.String() != "02:42:ac:11:00:02" || input["bridge"].Aliases[0] != "svc" {
		t.Error("input endpoints must not be modified")
	}

	if kept := SanitizeContainerCreateEndpointSettingsForDockerAPI(input, "1.44"); kept["bridge"].MacAddress.String() != "02:42:ac:11:00:02" {
		t.Error("MAC addresses must be kept at API 1.44")
	}
	if SanitizeContainerCreateEndpointSettingsForDockerAPI(nil, "1.44") != nil || SanitizeContainerCreateEndpointSettingsForDockerAPI(map[string]*network.EndpointSettings{}, "1.44") != nil {
		t.Error("empty input must yield nil")
	}
}

func TestPrepareContainerCreateOptionsForDockerAPI(t *testing.T) {
	t.Parallel()

	options := client.ContainerCreateOptions{
		HostConfig: &container.HostConfig{NetworkMode: "synobridge"},
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			"synobridge":                  {Aliases: []string{"app"}},
			"nginx-proxy-manager_zbridge": {Aliases: []string{"proxy"}},
		}},
	}

	t.Run("splits legacy multi-network create into primary plus extras", func(t *testing.T) {
		t.Parallel()
		adjusted, extras := PrepareContainerCreateOptionsForDockerAPI(options, "1.43")
		if len(adjusted.NetworkingConfig.EndpointsConfig) != 1 || !slices.Equal(adjusted.NetworkingConfig.EndpointsConfig["synobridge"].Aliases, []string{"app"}) {
			t.Fatalf("adjusted endpoints = %v, want only synobridge", adjusted.NetworkingConfig.EndpointsConfig)
		}
		if len(extras) != 1 || !slices.Equal(extras["nginx-proxy-manager_zbridge"].Aliases, []string{"proxy"}) {
			t.Fatalf("extras = %v, want only the proxy network", extras)
		}
		adjusted.NetworkingConfig.EndpointsConfig["synobridge"].Aliases[0] = "changed"
		extras["nginx-proxy-manager_zbridge"].Aliases[0] = "changed-too"
		if options.NetworkingConfig.EndpointsConfig["synobridge"].Aliases[0] != "app" || options.NetworkingConfig.EndpointsConfig["nginx-proxy-manager_zbridge"].Aliases[0] != "proxy" {
			t.Error("original endpoints must not be modified")
		}
	})

	t.Run("leaves create options unchanged on newer daemon apis", func(t *testing.T) {
		t.Parallel()
		adjusted, extras := PrepareContainerCreateOptionsForDockerAPI(options, "1.44")
		if len(adjusted.NetworkingConfig.EndpointsConfig) != 2 || extras != nil {
			t.Errorf("adjusted = %v, extras = %v; want untouched options", adjusted.NetworkingConfig.EndpointsConfig, extras)
		}
	})

	t.Run("uses first network by name when mode is unset", func(t *testing.T) {
		t.Parallel()
		adjusted, extras := PrepareContainerCreateOptionsForDockerAPI(client.ContainerCreateOptions{
			NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{"znet": {}, "anet": {}}},
		}, "1.43")
		if adjusted.HostConfig == nil || adjusted.HostConfig.NetworkMode != "anet" {
			t.Fatalf("host config = %+v, want NetworkMode anet", adjusted.HostConfig)
		}
		if _, ok := adjusted.NetworkingConfig.EndpointsConfig["anet"]; !ok || len(adjusted.NetworkingConfig.EndpointsConfig) != 1 {
			t.Errorf("adjusted endpoints = %v, want only anet", adjusted.NetworkingConfig.EndpointsConfig)
		}
		if _, ok := extras["znet"]; !ok || len(extras) != 1 {
			t.Errorf("extras = %v, want only znet", extras)
		}
	})

	t.Run("leaves create options unchanged when named network mode is missing from endpoints", func(t *testing.T) {
		t.Parallel()
		missing := client.ContainerCreateOptions{
			HostConfig:       &container.HostConfig{NetworkMode: "mynetwork"},
			NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{"anet": {}, "znet": {}}},
		}
		adjusted, extras := PrepareContainerCreateOptionsForDockerAPI(missing, "1.43")
		if adjusted.HostConfig.NetworkMode != "mynetwork" || len(adjusted.NetworkingConfig.EndpointsConfig) != 2 || extras != nil {
			t.Errorf("options were split despite an unknown named network mode")
		}
	})
}

func TestContainerCreateWithCompatibilityForAPIVersion(t *testing.T) {
	t.Parallel()

	t.Run("uses provided api version without re-detecting it", func(t *testing.T) {
		t.Parallel()
		fake := &fakeClient{createResult: client.ContainerCreateResult{ID: "created"}}
		result, err := ContainerCreateWithCompatibilityForAPIVersion(t.Context(), fake, client.ContainerCreateOptions{Config: &container.Config{Image: "nginx:latest"}}, "1.44")
		if err != nil || result.ID != "created" {
			t.Fatalf("result = %+v, err = %v", result, err)
		}
		if fake.clientVersionCalls != 0 || fake.serverVersionCalls != 0 || len(fake.createCalls) != 1 {
			t.Errorf("version detection ran or create count wrong: %+v", fake)
		}
	})

	t.Run("removes created container when a later network attach fails", func(t *testing.T) {
		t.Parallel()
		fake := &fakeClient{createResult: client.ContainerCreateResult{ID: "created-container"}, connectErrs: map[string]error{"cnet": errors.New("boom")}}
		_, err := ContainerCreateWithCompatibilityForAPIVersion(t.Context(), fake, client.ContainerCreateOptions{
			HostConfig:       &container.HostConfig{NetworkMode: "anet"},
			NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{"anet": {}, "bnet": {}, "cnet": {}, "dnet": {}}},
		}, "1.43")
		if err == nil || !strings.Contains(err.Error(), "connect network cnet") {
			t.Fatalf("err = %v, want connect network cnet", err)
		}
		if !slices.Equal(fake.connectCalls, []string{"bnet", "cnet"}) {
			t.Errorf("connect calls = %v, want bnet then cnet and stop", fake.connectCalls)
		}
		if len(fake.removeCalls) != 1 || !fake.removeCalls[0].Force {
			t.Errorf("remove calls = %+v, want one forced removal", fake.removeCalls)
		}
	})

	t.Run("propagates network attach error when rollback remove also fails", func(t *testing.T) {
		t.Parallel()
		connectErr, removeErr := errors.New("connect-boom"), errors.New("remove-boom")
		fake := &fakeClient{
			createResult: client.ContainerCreateResult{ID: "created-container"},
			connectErrs:  map[string]error{"bnet": connectErr},
			removeErr:    removeErr,
		}
		_, err := ContainerCreateWithCompatibilityForAPIVersion(t.Context(), fake, client.ContainerCreateOptions{
			HostConfig:       &container.HostConfig{NetworkMode: "anet"},
			NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{"anet": {}, "bnet": {}}},
		}, "1.43")
		if !errors.Is(err, connectErr) || errors.Is(err, removeErr) {
			t.Errorf("err = %v, want the connect error only", err)
		}
		if len(fake.removeCalls) != 1 {
			t.Errorf("remove calls = %d, want 1", len(fake.removeCalls))
		}
	})
}
