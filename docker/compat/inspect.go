package compat

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"

	containertypes "github.com/moby/moby/api/types/container"
	networktypes "github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

// WrapDockerAPIClientForInspectCompatibility wraps a client so create, inspect,
// and list calls apply the daemon compatibility shims transparently.
func WrapDockerAPIClientForInspectCompatibility(apiClient client.APIClient) client.APIClient {
	if _, ok := apiClient.(*compatibilityClient); ok {
		return apiClient
	}
	return &compatibilityClient{APIClient: apiClient}
}

type compatibilityClient struct {
	client.APIClient
}

func (c *compatibilityClient) ContainerCreate(ctx context.Context, options client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
	return ContainerCreateWithCompatibility(ctx, c.APIClient, options)
}

func (c *compatibilityClient) ContainerInspect(ctx context.Context, containerID string, options client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	return ContainerInspectWithCompatibility(ctx, c.APIClient, containerID, options)
}

func (c *compatibilityClient) NetworkInspect(ctx context.Context, networkID string, options client.NetworkInspectOptions) (client.NetworkInspectResult, error) {
	return NetworkInspectWithCompatibility(ctx, c.APIClient, networkID, options)
}

func (c *compatibilityClient) NetworkList(ctx context.Context, options client.NetworkListOptions) (client.NetworkListResult, error) {
	return NetworkListWithCompatibility(ctx, c.APIClient, options)
}

// ContainerInspectWithCompatibility retries a failed typed decode against the
// raw daemon JSON, stripping CIDR suffixes that newer Moby address types reject.
func ContainerInspectWithCompatibility(ctx context.Context, apiClient client.APIClient, containerID string, options client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	result, err := apiClient.ContainerInspect(ctx, containerID, options)
	query := url.Values{}
	if options.Size {
		query.Set("size", "1")
	}
	if repaired, raw, ok := repairInternal[containertypes.InspectResponse](ctx, apiClient, err, "/containers/"+strings.TrimSpace(containerID)+"/json", query, repairContainerInspectInternal); ok {
		return client.ContainerInspectResult{Container: repaired, Raw: raw}, nil
	}
	return result, err
}

// NetworkInspectWithCompatibility retries a failed typed decode against the raw
// daemon JSON, stripping CIDR suffixes that newer Moby address types reject.
func NetworkInspectWithCompatibility(ctx context.Context, apiClient client.APIClient, networkID string, options client.NetworkInspectOptions) (client.NetworkInspectResult, error) {
	result, err := apiClient.NetworkInspect(ctx, networkID, options)
	query := url.Values{}
	if options.Verbose {
		query.Set("verbose", "true")
	}
	if scope := strings.TrimSpace(options.Scope); scope != "" {
		query.Set("scope", scope)
	}
	if repaired, raw, ok := repairInternal[networktypes.Inspect](ctx, apiClient, err, "/networks/"+strings.TrimSpace(networkID), query, repairNetworkInternal); ok {
		return client.NetworkInspectResult{Network: repaired, Raw: raw}, nil
	}
	return result, err
}

// NetworkListWithCompatibility retries a failed typed decode against the raw
// daemon JSON, stripping CIDR suffixes that newer Moby address types reject.
func NetworkListWithCompatibility(ctx context.Context, apiClient client.APIClient, options client.NetworkListOptions) (client.NetworkListResult, error) {
	result, err := apiClient.NetworkList(ctx, options)
	query := url.Values{}
	if len(options.Filters) > 0 {
		filtersJSON, marshalErr := json.Marshal(options.Filters)
		if marshalErr != nil {
			return result, err
		}
		query.Set("filters", string(filtersJSON))
	}
	repairAll := func(items []any) bool {
		changed := false
		for _, item := range items {
			if network, ok := item.(map[string]any); ok {
				changed = repairNetworkInternal(network) || changed
			}
		}
		return changed
	}
	if repaired, _, ok := repairInternal[[]networktypes.Summary](ctx, apiClient, err, "/networks", query, repairAll); ok {
		return client.NetworkListResult{Items: repaired}, nil
	}
	return result, err
}

// repairInternal handles a ParseAddr/ParsePrefix decode failure by fetching the
// raw daemon JSON, applying repair to its generic form P, and decoding the
// result as T. It reports false whenever the original error should stand.
func repairInternal[T, P any](ctx context.Context, apiClient client.APIClient, err error, resource string, query url.Values, repair func(P) bool) (T, []byte, bool) {
	var repaired T
	if err == nil || !strings.Contains(err.Error(), "ParseAddr(") && !strings.Contains(err.Error(), "ParsePrefix(") {
		return repaired, nil, false
	}
	raw, err := fetchInternal(ctx, apiClient, resource, query)
	if err != nil {
		return repaired, nil, false
	}
	var payload P
	if json.Unmarshal(raw, &payload) != nil || !repair(payload) {
		return repaired, nil, false
	}
	normalized, err := json.Marshal(payload)
	if err != nil || json.Unmarshal(normalized, &repaired) != nil {
		return repaired, nil, false
	}
	return repaired, normalized, true
}

// fetchInternal performs a raw GET through the client's dialer, which keeps the
// client's TLS and socket transport without its typed decoding.
func fetchInternal(ctx context.Context, apiClient client.APIClient, resource string, query url.Values) ([]byte, error) {
	dialer := apiClient.Dialer()
	if dialer == nil {
		return nil, errors.New("docker api client does not expose a dialer")
	}
	versionPrefix := ""
	if version := strings.TrimSpace(strings.TrimPrefix(apiClient.ClientVersion(), "v")); version != "" {
		versionPrefix = "/v" + version
	}
	reqURL := &url.URL{Scheme: "http", Host: client.DummyHost, Path: path.Join("/", versionPrefix, resource), RawQuery: query.Encode()}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), http.NoBody)
	if err != nil {
		return nil, err
	}
	conn, err := dialer(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	err = req.Write(conn)
	if err != nil {
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("docker api GET %s failed with %s: %s", resource, resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// repairContainerInspectInternal strips CIDR suffixes from every address in a
// container inspect payload, filling prefix lengths the suffix carried.
func repairContainerInspectInternal(payload map[string]any) bool {
	settings, ok := payload["NetworkSettings"].(map[string]any)
	if !ok {
		return false
	}
	endpoint := func(obj map[string]any) bool {
		changed := rewriteFieldsInternal(obj, false, "Gateway", "IPv6Gateway")
		changed = fillPrefixLenInternal(obj, "IPAddress", "IPPrefixLen") || changed
		return fillPrefixLenInternal(obj, "GlobalIPv6Address", "GlobalIPv6PrefixLen") || changed
	}
	changed := endpoint(settings)
	networks, _ := settings["Networks"].(map[string]any)
	for _, value := range networks {
		network, isMap := value.(map[string]any)
		if !isMap {
			continue
		}
		changed = endpoint(network) || changed
		if ipam, hasIPAM := network["IPAMConfig"].(map[string]any); hasIPAM {
			changed = rewriteFieldsInternal(ipam, false, "IPv4Address", "IPv6Address", "LinkLocalIPs") || changed
		}
	}
	return changed
}

// repairNetworkInternal strips CIDR suffixes from IPAM gateways and auxiliary
// addresses and canonicalizes container endpoint prefixes in a network payload.
func repairNetworkInternal(payload map[string]any) bool {
	changed := false
	if ipam, ok := payload["IPAM"].(map[string]any); ok {
		configs, _ := ipam["Config"].([]any)
		for _, value := range configs {
			if config, isMap := value.(map[string]any); isMap {
				changed = rewriteFieldsInternal(config, false, "Gateway", "AuxiliaryAddresses", "AuxAddress") || changed
			}
		}
	}
	containers, _ := payload["Containers"].(map[string]any)
	for _, value := range containers {
		if endpoint, ok := value.(map[string]any); ok {
			changed = rewriteFieldsInternal(endpoint, true, "IPv4Address", "IPv6Address") || changed
		}
	}
	return changed
}

// rewriteFieldsInternal normalizes the address strings under keys, whether the
// value is a string, a slice, or a map of strings, and reports any change.
func rewriteFieldsInternal(obj map[string]any, keepPrefix bool, keys ...string) bool {
	changed := false
	for _, key := range keys {
		switch value := obj[key].(type) {
		case string:
			if normalized, ok := normalizeIPInternal(value, keepPrefix); ok {
				obj[key] = normalized
				changed = true
			}
		case []any:
			for i, item := range value {
				if raw, ok := item.(string); ok {
					if normalized, rewritten := normalizeIPInternal(raw, keepPrefix); rewritten {
						value[i] = normalized
						changed = true
					}
				}
			}
		case map[string]any:
			for name, item := range value {
				if raw, ok := item.(string); ok {
					if normalized, rewritten := normalizeIPInternal(raw, keepPrefix); rewritten {
						value[name] = normalized
						changed = true
					}
				}
			}
		}
	}
	return changed
}

// fillPrefixLenInternal strips a CIDR suffix from obj[addrKey] and records its
// length under lenKey when the payload left the length unset.
func fillPrefixLenInternal(obj map[string]any, addrKey, lenKey string) bool {
	raw, ok := obj[addrKey].(string)
	trimmed := strings.TrimSpace(raw)
	if !ok || trimmed == "" {
		return false
	}
	if _, err := netip.ParseAddr(trimmed); err == nil {
		obj[addrKey] = trimmed
		return raw != trimmed
	}
	prefix, err := netip.ParsePrefix(trimmed)
	if err != nil {
		return false
	}
	changed := false
	if addr := prefix.Addr().String(); raw != addr {
		obj[addrKey] = addr
		changed = true
	}
	missing := false
	switch current := obj[lenKey].(type) {
	case nil:
		missing = true
	case float64:
		missing = current == 0
	case int:
		missing = current == 0
	case string:
		parsed, parseErr := strconv.Atoi(strings.TrimSpace(current))
		missing = parseErr != nil || parsed == 0
	}
	if missing {
		obj[lenKey] = prefix.Bits()
		changed = true
	}
	return changed
}

// normalizeIPInternal trims raw and either canonicalizes it as a prefix or
// reduces it to its bare address, reporting whether the result differs.
func normalizeIPInternal(raw string, keepPrefix bool) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	normalized := trimmed
	if _, addrErr := netip.ParseAddr(trimmed); addrErr != nil || keepPrefix {
		prefix, err := netip.ParsePrefix(trimmed)
		if err != nil {
			return "", false
		}
		normalized = prefix.Addr().String()
		if keepPrefix {
			normalized = prefix.String()
		}
	}
	return normalized, normalized != raw
}
