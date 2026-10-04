package compat

import (
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"testing"

	containertypes "github.com/moby/moby/api/types/container"
	networktypes "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

const networkListJSON = `[{"Name":"test-net","Id":"net123","Created":"2026-03-11T00:00:00Z","Scope":"local","Driver":"bridge",
	"IPAM":{"Driver":"default","Config":[{"Subnet":"fdd0:0:0:c::/64","Gateway":"fdd0:0:0:c::1/64","AuxiliaryAddresses":{"router":"fdd0:0:0:c::2/64"}}]}}]`

const containerInspectJSON = `{"Id":"abc123","Name":"/app","Config":{"Image":"test:latest"},"HostConfig":{"NetworkMode":"bridge"},
	"NetworkSettings":{"Networks":{"bridge":{"IPv6Gateway":"fdd0:0:0:c::1/64"}}}}`

func newTestClient(t *testing.T, handler http.HandlerFunc) *client.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	dockerClient, err := client.New(client.WithHost("tcp://"+strings.TrimPrefix(server.URL, "http://")), client.WithAPIVersion("1.41"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dockerClient.Close() })
	return dockerClient
}

func repairJSON[T any](t *testing.T, raw string, repair func(map[string]any) bool) T {
	t.Helper()
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	if !repair(payload) {
		t.Fatal("repair reported no change")
	}
	normalized, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var typed T
	err = json.Unmarshal(normalized, &typed)
	if err != nil {
		t.Fatal(err)
	}
	return typed
}

func TestRepairContainerInspect(t *testing.T) {
	t.Parallel()

	inspect := repairJSON[containertypes.InspectResponse](t, `{"Id":"abc123","Name":"/app","Config":{"Image":"test:latest"},"HostConfig":{"NetworkMode":"bridge"},
		"NetworkSettings":{"Gateway":"172.18.0.1/16","IPAddress":"172.18.0.20/16","IPPrefixLen":0,
		"Networks":{"bridge":{"IPAMConfig":{"IPv4Address":"172.18.0.50/16","IPv6Address":"fdd0:0:0:c::10/64","LinkLocalIPs":["169.254.10.10/16","fe80::10/64"]},
		"Gateway":"172.18.0.1/16","IPAddress":"172.18.0.20/16","IPPrefixLen":0,
		"IPv6Gateway":"fdd0:0:0:c::1/64","GlobalIPv6Address":"fdd0:0:0:c::10/64","GlobalIPv6PrefixLen":0}}}}`, repairContainerInspectInternal)

	endpoint := inspect.NetworkSettings.Networks["bridge"]
	if endpoint == nil || endpoint.IPAMConfig == nil {
		t.Fatal("bridge endpoint or its IPAM config missing after repair")
	}
	if endpoint.Gateway != netip.MustParseAddr("172.18.0.1") || endpoint.IPAddress != netip.MustParseAddr("172.18.0.20") || endpoint.IPPrefixLen != 16 {
		t.Errorf("IPv4 endpoint = %+v", endpoint)
	}
	if endpoint.IPv6Gateway != netip.MustParseAddr("fdd0:0:0:c::1") || endpoint.GlobalIPv6Address != netip.MustParseAddr("fdd0:0:0:c::10") || endpoint.GlobalIPv6PrefixLen != 64 {
		t.Errorf("IPv6 endpoint = %+v", endpoint)
	}
	if endpoint.IPAMConfig.IPv4Address != netip.MustParseAddr("172.18.0.50") || endpoint.IPAMConfig.IPv6Address != netip.MustParseAddr("fdd0:0:0:c::10") {
		t.Errorf("IPAM config = %+v", endpoint.IPAMConfig)
	}
	if !slices.Equal(endpoint.IPAMConfig.LinkLocalIPs, []netip.Addr{netip.MustParseAddr("169.254.10.10"), netip.MustParseAddr("fe80::10")}) {
		t.Errorf("LinkLocalIPs = %v", endpoint.IPAMConfig.LinkLocalIPs)
	}

	payload := map[string]any{}
	if err := json.Unmarshal([]byte(`{"NetworkSettings":{"Gateway":"172.18.0.1/16","IPAddress":"172.18.0.20/16","IPPrefixLen":0,"Networks":{}}}`), &payload); err != nil {
		t.Fatal(err)
	}
	settings, _ := payload["NetworkSettings"].(map[string]any)
	if !repairContainerInspectInternal(payload) || settings["Gateway"] != "172.18.0.1" || settings["IPAddress"] != "172.18.0.20" || settings["IPPrefixLen"] != 16 {
		t.Errorf("top-level network settings = %v", settings)
	}
}

func TestRepairNetwork(t *testing.T) {
	t.Parallel()

	inspect := repairJSON[networktypes.Inspect](t, `{"Name":"test-net","Id":"net123","Created":"2026-03-11T00:00:00Z","Scope":"local","Driver":"bridge","EnableIPv6":true,
		"IPAM":{"Driver":"default","Config":[{"Subnet":"fdd0:0:0:c::/64","Gateway":"fdd0:0:0:c::1/64","AuxiliaryAddresses":{"router":"fdd0:0:0:c::2/64"}}]},
		"Containers":{"abc123":{"Name":"app","EndpointID":"ep1","IPv4Address":" 172.18.0.20/16 ","IPv6Address":" fdd0:0:0:c::10/64 "}}}`, repairNetworkInternal)

	if len(inspect.IPAM.Config) != 1 || inspect.IPAM.Config[0].Subnet != netip.MustParsePrefix("fdd0:0:0:c::/64") || inspect.IPAM.Config[0].Gateway != netip.MustParseAddr("fdd0:0:0:c::1") {
		t.Errorf("IPAM config = %+v", inspect.IPAM.Config)
	}
	if inspect.IPAM.Config[0].AuxAddress["router"] != netip.MustParseAddr("fdd0:0:0:c::2") {
		t.Errorf("AuxAddress = %v", inspect.IPAM.Config[0].AuxAddress)
	}
	endpoint := inspect.Containers["abc123"]
	if endpoint.IPv4Address != netip.MustParsePrefix("172.18.0.20/16") || endpoint.IPv6Address != netip.MustParsePrefix("fdd0:0:0:c::10/64") {
		t.Errorf("container endpoint = %+v", endpoint)
	}
}

func TestNormalizeIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw        string
		keepPrefix bool
		want       string
		changed    bool
	}{
		{raw: " 172.18.0.1 ", want: "172.18.0.1", changed: true},
		{raw: "172.18.0.1", want: "172.18.0.1"},
		{raw: "172.18.0.1/16", want: "172.18.0.1", changed: true},
		{raw: " 172.18.0.20/16 ", keepPrefix: true, want: "172.18.0.20/16", changed: true},
		{raw: "172.18.0.20/16", keepPrefix: true, want: "172.18.0.20/16"},
		{raw: "not-an-ip"},
		{raw: ""},
	}
	for _, tt := range tests {
		got, changed := normalizeIPInternal(tt.raw, tt.keepPrefix)
		if changed != tt.changed || (changed && got != tt.want) {
			t.Errorf("normalizeIPInternal(%q, %t) = (%q, %t), want (%q, %t)", tt.raw, tt.keepPrefix, got, changed, tt.want, tt.changed)
		}
	}
}

func TestFillPrefixLen(t *testing.T) {
	t.Parallel()

	obj := map[string]any{"IPAddress": " 172.18.0.20 ", "IPPrefixLen": 24}
	if !fillPrefixLenInternal(obj, "IPAddress", "IPPrefixLen") || obj["IPAddress"] != "172.18.0.20" || obj["IPPrefixLen"] != 24 {
		t.Errorf("whitespace trim: %v", obj)
	}
	obj = map[string]any{"IPAddress": "172.18.0.20/16", "IPPrefixLen": float64(0)}
	if !fillPrefixLenInternal(obj, "IPAddress", "IPPrefixLen") || obj["IPAddress"] != "172.18.0.20" || obj["IPPrefixLen"] != 16 {
		t.Errorf("prefix fill: %v", obj)
	}
	obj = map[string]any{"IPAddress": "172.18.0.20/16", "IPPrefixLen": 24}
	if !fillPrefixLenInternal(obj, "IPAddress", "IPPrefixLen") || obj["IPPrefixLen"] != 24 {
		t.Errorf("existing prefix length must be kept: %v", obj)
	}
}

func TestContainerInspectWithCompatibility(t *testing.T) {
	t.Parallel()

	dockerClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.41/containers/test-container/json" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"Id":"abc123","Name":"/app","Config":{"Image":"test:latest"},"HostConfig":{"NetworkMode":"bridge"},
			"NetworkSettings":{"Networks":{"bridge":{"IPAMConfig":{"IPv6Address":"fdd0:0:0:c::10/64"},"IPv6Gateway":"fdd0:0:0:c::1/64","GlobalIPv6Address":"fdd0:0:0:c::10/64","GlobalIPv6PrefixLen":0}}}}`))
	})

	result, err := ContainerInspectWithCompatibility(t.Context(), dockerClient, "test-container", client.ContainerInspectOptions{})
	if err != nil || result.Container.ID != "abc123" {
		t.Fatalf("result = %+v, err = %v", result.Container.ID, err)
	}
	endpoint := result.Container.NetworkSettings.Networks["bridge"]
	if endpoint == nil || endpoint.IPv6Gateway != netip.MustParseAddr("fdd0:0:0:c::1") || endpoint.GlobalIPv6Address != netip.MustParseAddr("fdd0:0:0:c::10") || endpoint.GlobalIPv6PrefixLen != 64 {
		t.Errorf("endpoint = %+v", endpoint)
	}
}

func TestContainerInspectWithCompatibility_TLSRemote(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(containerInspectJSON))
	}))
	defer server.Close()
	dockerClient, err := client.New(
		client.WithHTTPClient(server.Client()),
		client.WithHost("tcp://"+strings.TrimPrefix(server.URL, "https://")),
		client.WithScheme("https"),
		client.WithAPIVersion("1.41"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = dockerClient.Close() }()

	result, err := ContainerInspectWithCompatibility(t.Context(), dockerClient, "test-container", client.ContainerInspectOptions{})
	if err != nil || result.Container.NetworkSettings.Networks["bridge"].IPv6Gateway != netip.MustParseAddr("fdd0:0:0:c::1") {
		t.Errorf("result = %+v, err = %v", result.Container.NetworkSettings, err)
	}
}

func TestNetworkInspectWithCompatibility(t *testing.T) {
	t.Parallel()

	dockerClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.41/networks/test-net" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"Name":"test-net","Id":"net123","Created":"2026-03-11T00:00:00Z","Scope":"local","Driver":"bridge",
			"IPAM":{"Driver":"default","Config":[{"Subnet":"fdd0:0:0:c::/64","Gateway":"fdd0:0:0:c::1/64"}]},"Containers":{}}`))
	})

	result, err := NetworkInspectWithCompatibility(t.Context(), dockerClient, "test-net", client.NetworkInspectOptions{})
	if err != nil || len(result.Network.IPAM.Config) != 1 || result.Network.IPAM.Config[0].Gateway != netip.MustParseAddr("fdd0:0:0:c::1") {
		t.Errorf("result = %+v, err = %v", result.Network.IPAM, err)
	}
}

func TestNetworkInspectWithCompatibility_LeavesInvalidValuesFailing(t *testing.T) {
	t.Parallel()

	dockerClient := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"Name":"test-net","Id":"net123","Created":"2026-03-11T00:00:00Z","Scope":"local","Driver":"bridge",
			"IPAM":{"Driver":"default","Config":[{"Subnet":"fdd0:0:0:c::/64","Gateway":"definitely-not-an-ip"}]},"Containers":{}}`))
	})
	_, err := NetworkInspectWithCompatibility(t.Context(), dockerClient, "test-net", client.NetworkInspectOptions{})
	if err == nil || !strings.Contains(err.Error(), `ParseAddr("definitely-not-an-ip")`) {
		t.Errorf("err = %v, want the original parse error", err)
	}
}

func TestNetworkListWithCompatibility(t *testing.T) {
	t.Parallel()

	calls := 0
	dockerClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1.41/networks" || r.URL.Query().Get("filters") != `{"name":{"test-net":true}}` {
			t.Errorf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(networkListJSON))
	})

	result, err := NetworkListWithCompatibility(t.Context(), dockerClient, client.NetworkListOptions{Filters: make(client.Filters).Add("name", "test-net")})
	if err != nil || len(result.Items) != 1 || len(result.Items[0].IPAM.Config) != 1 {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if result.Items[0].IPAM.Config[0].Gateway != netip.MustParseAddr("fdd0:0:0:c::1") || result.Items[0].IPAM.Config[0].AuxAddress["router"] != netip.MustParseAddr("fdd0:0:0:c::2") {
		t.Errorf("IPAM config = %+v", result.Items[0].IPAM.Config[0])
	}
	if calls != 2 {
		t.Errorf("calls = %d, want typed attempt plus raw retry", calls)
	}
}

func TestNetworkListWithCompatibility_SuccessSkipsFallback(t *testing.T) {
	t.Parallel()

	calls := 0
	dockerClient := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`[{"Name":"test-net","Id":"net123","Created":"2026-03-11T00:00:00Z","Scope":"local","Driver":"bridge",
			"IPAM":{"Driver":"default","Config":[{"Subnet":"fdd0:0:0:c::/64","Gateway":"fdd0:0:0:c::1"}]}}]`))
	})
	result, err := NetworkListWithCompatibility(t.Context(), dockerClient, client.NetworkListOptions{})
	if err != nil || len(result.Items) != 1 || result.Items[0].IPAM.Config[0].Gateway != netip.MustParseAddr("fdd0:0:0:c::1") || calls != 1 {
		t.Errorf("result = %+v, err = %v, calls = %d", result, err, calls)
	}
}

func TestNetworkListWithCompatibility_LeavesInvalidValuesFailing(t *testing.T) {
	t.Parallel()

	calls := 0
	dockerClient := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`[{"Name":"test-net","Id":"net123","Created":"2026-03-11T00:00:00Z","Scope":"local","Driver":"bridge",
			"IPAM":{"Driver":"default","Config":[{"Subnet":"fdd0:0:0:c::/64","Gateway":"definitely-not-an-ip"}]}}]`))
	})
	_, err := NetworkListWithCompatibility(t.Context(), dockerClient, client.NetworkListOptions{})
	if err == nil || !strings.Contains(err.Error(), `ParseAddr("definitely-not-an-ip")`) || calls != 2 {
		t.Errorf("err = %v, calls = %d; want the original parse error after one retry", err, calls)
	}
}

func TestWrapDockerAPIClientForInspectCompatibility(t *testing.T) {
	t.Parallel()

	calls := 0
	dockerClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch {
		case strings.HasSuffix(r.URL.Path, "/networks"):
			_, _ = w.Write([]byte(networkListJSON))
		default:
			_, _ = w.Write([]byte(containerInspectJSON))
		}
	})

	wrapped := WrapDockerAPIClientForInspectCompatibility(dockerClient)
	if WrapDockerAPIClientForInspectCompatibility(wrapped) != wrapped {
		t.Error("wrapping twice must return the same wrapper")
	}
	inspect, err := wrapped.ContainerInspect(t.Context(), "test-container", client.ContainerInspectOptions{})
	if err != nil || inspect.Container.NetworkSettings.Networks["bridge"].IPv6Gateway != netip.MustParseAddr("fdd0:0:0:c::1") {
		t.Errorf("inspect = %+v, err = %v", inspect.Container.NetworkSettings, err)
	}
	list, err := wrapped.NetworkList(t.Context(), client.NetworkListOptions{})
	if err != nil || len(list.Items) != 1 || list.Items[0].IPAM.Config[0].Gateway != netip.MustParseAddr("fdd0:0:0:c::1") {
		t.Errorf("list = %+v, err = %v", list, err)
	}
	if calls != 4 {
		t.Errorf("calls = %d, want two typed attempts and two raw retries", calls)
	}
}

func TestWrapDockerAPIClientForInspectCompatibility_ContainerCreateLegacyNetworks(t *testing.T) {
	t.Parallel()

	type connectRequest struct {
		Container      string                         `json:"Container"`
		EndpointConfig *networktypes.EndpointSettings `json:"EndpointConfig"`
	}
	var createPayload struct {
		NetworkingConfig networktypes.NetworkingConfig `json:"NetworkingConfig"`
	}
	connectPayloads := map[string]connectRequest{}

	dockerClient := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/containers/create"):
			if err := json.Unmarshal(body, &createPayload); err != nil {
				t.Error(err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Id":"new-container-id","Warnings":[]}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/connect"):
			name := strings.TrimSuffix(r.URL.Path[strings.LastIndex(r.URL.Path, "/networks/")+len("/networks/"):], "/connect")
			var payload connectRequest
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Error(err)
			}
			connectPayloads[name] = payload
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})

	result, err := WrapDockerAPIClientForInspectCompatibility(dockerClient).ContainerCreate(t.Context(), client.ContainerCreateOptions{
		Config:     &containertypes.Config{Image: "nginx:alpine"},
		HostConfig: &containertypes.HostConfig{NetworkMode: "synobridge"},
		NetworkingConfig: &networktypes.NetworkingConfig{EndpointsConfig: map[string]*networktypes.EndpointSettings{
			"synobridge":                  {Aliases: []string{"app"}},
			"nginx-proxy-manager_zbridge": {Aliases: []string{"proxy"}},
		}},
		Name: "test-app",
	})
	if err != nil || result.ID != "new-container-id" {
		t.Fatalf("result = %+v, err = %v", result, err)
	}
	if len(createPayload.NetworkingConfig.EndpointsConfig) != 1 || !slices.Equal(createPayload.NetworkingConfig.EndpointsConfig["synobridge"].Aliases, []string{"app"}) {
		t.Errorf("create payload endpoints = %v, want only synobridge", createPayload.NetworkingConfig.EndpointsConfig)
	}
	connected := connectPayloads["nginx-proxy-manager_zbridge"]
	if len(connectPayloads) != 1 || connected.Container != "new-container-id" || connected.EndpointConfig == nil || !slices.Equal(connected.EndpointConfig.Aliases, []string{"proxy"}) {
		t.Errorf("connect payloads = %+v, want one attach for the proxy network", connectPayloads)
	}
}
