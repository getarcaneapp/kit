package convert

import (
	"cmp"
	"encoding/csv"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/compose-spec/compose-go/v2/format"
	compose "github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/go-units"
	"go.yaml.in/yaml/v4"
	"kit.getarcane.app"

	"kit.getarcane.app/docker/convert/types"
)

var serviceNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

func Build(commands []types.RunCommand, opts types.Options) (*types.Document, error) {
	services := make(map[string]types.Service)
	networks := make(map[string]map[string]any)
	volumes := make(map[string]map[string]any)
	var order []string
	var warnings []types.Warning

	var existing map[string]any
	if err := yaml.Unmarshal(opts.ExistingComposeYAML, &existing); err != nil {
		return nil, types.NewConversionError("parse existing compose YAML: %v", err)
	}
	if existingServices, ok := existing["services"].(map[string]any); ok {
		for _, name := range slices.Sorted(maps.Keys(existingServices)) {
			if svc, isMap := existingServices[name].(map[string]any); isMap {
				services[name] = svc
				order = append(order, name)
			}
		}
	}

	for _, cmd := range commands {
		image, _, _ := strings.Cut(cmd.Image[strings.LastIndex(cmd.Image, "/")+1:], ":")
		image, _, _ = strings.Cut(image, "@")
		base := cmp.Or(
			strings.ToLower(strings.Trim(serviceNameSanitizer.ReplaceAllString(strings.TrimSpace(cmp.Or(cmd.Name, image)), "-"), "-_.")),
			kit.Ternary(cmd.Name == "", "app", ""),
		)
		name := base
		for i := 2; slices.Contains(order, name); i++ {
			name = fmt.Sprintf("%s-%d", base, i)
		}

		service, serviceWarnings, err := mapService(cmd, networks, volumes)
		if err != nil {
			return nil, err
		}
		services[name] = service
		order = append(order, name)
		warnings = append(warnings, serviceWarnings...)
	}

	// Definitions from the existing file win over the external stubs added for
	// converted commands.
	existingNetworks, _ := existing["networks"].(map[string]any)
	for name, definition := range existingNetworks {
		networks[name], _ = definition.(map[string]any)
	}
	existingVolumes, _ := existing["volumes"].(map[string]any)
	for name, definition := range existingVolumes {
		volumes[name], _ = definition.(map[string]any)
	}
	maps.DeleteFunc(existing, func(key string, _ any) bool {
		return key == "services" || key == "networks" || key == "volumes"
	})

	return &types.Document{
		Services:     services,
		ServiceOrder: order,
		Networks:     networks,
		Volumes:      volumes,
		Sections:     existing,
		Warnings:     warnings,
	}, nil
}

func mapService(cmd types.RunCommand, networks, volumes map[string]map[string]any) (types.Service, []types.Warning, error) {
	service := types.Service{"image": cmd.Image}
	if cmd.Name != "" {
		service["container_name"] = cmd.Name
	}

	var warnings []types.Warning
	endpoint := map[string]any{}
	for _, flag := range cmd.Flags {
		switch flag.Name {
		case "ports", "environment", "env_file", "labels", "extra_hosts", "dns", "cap_add", "cap_drop", "devices", "group_add", "security_opt", "expose":
			appendString(service, flag.Name, flag.Value)
		case "volumes":
			items, _ := service["volumes"].([]any)
			service["volumes"] = append(items, flag.Value)
			if err := registerVolume(volumes, flag.Value); err != nil {
				return nil, nil, err
			}
		case "mount":
			volume, mountWarnings, err := mountVolume(flag.Value)
			if err != nil {
				return nil, nil, err
			}
			items, _ := service["volumes"].([]any)
			service["volumes"] = append(items, volume)
			warnings = append(warnings, mountWarnings...)
			if source, _ := volume["source"].(string); volume["type"] == "volume" && source != "" && !strings.Contains(source, "$") {
				volumes[source] = map[string]any{"external": true}
			}
		case "network":
			if flag.Value == "host" || flag.Value == "none" || strings.HasPrefix(flag.Value, "container:") {
				service["network_mode"] = flag.Value
			} else {
				appendString(service, "networks", flag.Value)
				networks[flag.Value] = map[string]any{"external": true}
			}
		case "ipv4_address":
			endpoint["ipv4_address"] = flag.Value
		case "aliases":
			aliases, _ := endpoint["aliases"].([]string)
			endpoint["aliases"] = append(aliases, flag.Value)
		case "hostname", "restart", "working_dir", "user", "platform", "pull_policy", "stop_signal":
			service[flag.Name] = flag.Value
		case "stop_grace_period":
			// docker run --stop-timeout is whole seconds; compose needs a duration.
			_, err := strconv.ParseUint(flag.Value, 10, 64)
			service["stop_grace_period"] = kit.Ternary(err == nil, flag.Value+"s", flag.Value)
		case "entrypoint":
			// docker run --entrypoint takes a single executable, not a command line.
			if flag.Value != "" {
				service["entrypoint"] = []string{flag.Value}
			}
		case "healthcheck":
			service["healthcheck"] = map[string]any{"test": flag.Value}
		case "memory", "cpus":
			ensureNestedMap(ensureNestedMap(ensureNestedMap(service, "deploy"), "resources"), "limits")[flag.Name] = flag.Value
		case "ulimits":
			ulimit, err := units.ParseUlimit(flag.Value)
			if err != nil {
				return nil, nil, types.NewConversionError("invalid ulimit %q: %v", flag.Value, err)
			}
			ensureNestedMap(service, "ulimits")[ulimit.Name] = map[string]any{"soft": ulimit.Soft, "hard": ulimit.Hard}
		case "logging.driver":
			ensureNestedMap(service, "logging")["driver"] = flag.Value
		case "logging.options":
			key, value, ok := strings.Cut(flag.Value, "=")
			if !ok {
				return nil, nil, types.NewConversionError("invalid log option %q", flag.Value)
			}
			ensureNestedMap(ensureNestedMap(service, "logging"), "options")[key] = value
		case "interactive":
			service["stdin_open"] = true
		case "tty", "privileged", "init", "read_only", "oom_kill_disable":
			service[flag.Name] = true
		case "gpus":
			if flag.Value == "all" {
				service["gpus"] = "all"
				continue
			}
			request, err := gpuRequest(flag.Value)
			if err != nil {
				return nil, nil, err
			}
			items, _ := service["gpus"].([]any)
			service["gpus"] = append(items, request)
		case "detach", "name":
		case "ignored":
			warnings = append(warnings, types.Warning{Message: "ignored unsupported Docker flag " + flag.Value})
		default:
			warnings = append(warnings, types.Warning{Message: "ignored Docker flag without a compose mapping: " + flag.Name})
		}
	}

	// docker run applies --ip and --network-alias to the user-defined network.
	if len(endpoint) > 0 {
		names, _ := service["networks"].([]string)
		if len(names) == 0 {
			warnings = append(warnings, types.Warning{Message: "ignored --ip/--network-alias without a user-defined --network"})
		} else {
			networkMap := make(map[string]any, len(names))
			for _, network := range names {
				networkMap[network] = map[string]any{}
			}
			networkMap[names[0]] = endpoint
			service["networks"] = networkMap
		}
	}

	if len(cmd.Command) > 0 {
		service["command"] = slices.Clone(cmd.Command)
	}

	return service, warnings, nil
}

func appendString(service types.Service, key, value string) {
	if value == "" {
		return
	}
	values, _ := service[key].([]string)
	service[key] = append(values, value)
}

func ensureNestedMap(parent map[string]any, key string) map[string]any {
	if existing, ok := parent[key].(map[string]any); ok {
		return existing
	}
	next := make(map[string]any)
	parent[key] = next
	return next
}

// mountVolume converts a docker run --mount value into a compose long-syntax
// volume mapping.
func mountVolume(value string) (map[string]any, []types.Warning, error) {
	volume := map[string]any{"type": "volume"}
	var warnings []types.Warning
	enabled := func(key, raw string, hasValue bool) (bool, error) {
		if !hasValue {
			return true, nil
		}
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return false, types.NewConversionError("invalid boolean %q for mount option %s", raw, key)
		}
		return parsed, nil
	}
	for part := range strings.SplitSeq(value, ",") {
		key, val, hasValue := strings.Cut(strings.TrimSpace(part), "=")
		switch key {
		case "":
		case "type":
			volume["type"] = val
		case "source", "src":
			volume["source"] = val
		case "target", "destination", "dst":
			volume["target"] = val
		case "readonly", "ro":
			readOnly, err := enabled(key, val, hasValue)
			if err != nil {
				return nil, nil, err
			}
			volume["read_only"] = readOnly
		case "consistency":
			volume["consistency"] = val
		case "bind-propagation":
			ensureNestedMap(volume, "bind")["propagation"] = val
		case "volume-nocopy":
			nocopy, err := enabled(key, val, hasValue)
			if err != nil {
				return nil, nil, err
			}
			ensureNestedMap(volume, "volume")["nocopy"] = nocopy
		case "volume-subpath":
			ensureNestedMap(volume, "volume")["subpath"] = val
		case "tmpfs-size":
			ensureNestedMap(volume, "tmpfs")["size"] = val
		case "tmpfs-mode":
			mode, err := strconv.ParseUint(val, 8, 32)
			if err != nil {
				return nil, nil, types.NewConversionError("invalid tmpfs-mode %q: %v", val, err)
			}
			ensureNestedMap(volume, "tmpfs")["mode"] = int64(mode)
		default:
			warnings = append(warnings, types.Warning{Message: "ignored unsupported mount option " + key})
		}
	}
	if target, _ := volume["target"].(string); target == "" {
		return nil, nil, types.NewConversionError("mount %q has no target", value)
	}
	return volume, warnings, nil
}

// gpuRequest converts a docker run --gpus value, a CSV of count, device,
// driver and capabilities, into a compose device request mapping.
func gpuRequest(value string) (map[string]any, error) {
	fields, err := csv.NewReader(strings.NewReader(value)).Read()
	if err != nil {
		return nil, types.NewConversionError("invalid gpus %q: %v", value, err)
	}
	request := map[string]any{"capabilities": []string{"gpu"}}
	for _, field := range fields {
		key, val, hasKey := strings.Cut(field, "=")
		switch {
		case !hasKey || key == "count":
			count := kit.Ternary(hasKey, val, field)
			if count == "all" {
				request["count"] = "all"
				continue
			}
			parsed, err := strconv.ParseInt(count, 10, 64)
			if err != nil {
				return nil, types.NewConversionError("invalid gpu count %q", count)
			}
			request["count"] = parsed
		case key == "device":
			request["device_ids"] = strings.Split(val, ",")
		case key == "driver":
			request["driver"] = val
		case key == "capabilities":
			request["capabilities"] = strings.Split(val, ",")
		default:
			return nil, types.NewConversionError("unsupported gpus option %q", key)
		}
	}
	if request["count"] != nil && request["device_ids"] != nil {
		return nil, types.NewConversionError("gpus %q sets both count and device", value)
	}
	return request, nil
}

func registerVolume(volumes map[string]map[string]any, value string) error {
	volume, err := format.ParseVolume(value)
	if err != nil {
		return types.NewConversionError("invalid volume %q: %v", value, err)
	}
	if volume.Type != compose.VolumeTypeVolume || volume.Source == "" || strings.Contains(volume.Source, "$") {
		return nil
	}
	volumes[volume.Source] = map[string]any{"external": true}
	return nil
}
