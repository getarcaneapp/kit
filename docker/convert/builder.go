package convert

import (
	"cmp"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/compose-spec/compose-go/v2/format"
	compose "github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/go-units"
	kit "go.getarcane.app/kit/pkg"
	"go.yaml.in/yaml/v4"

	"go.getarcane.app/docker/convert/types"
)

var serviceNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

func Build(commands []types.RunCommand, opts types.Options) (*types.Document, error) {
	services := make(map[string]types.Service)
	networks := make(map[string]map[string]any)
	volumes := make(map[string]map[string]any)
	var order []string
	var warnings []types.Warning

	if len(opts.ExistingComposeYAML) > 0 {
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

	return &types.Document{
		Services:     services,
		ServiceOrder: order,
		Networks:     networks,
		Volumes:      volumes,
		Warnings:     warnings,
	}, nil
}

func mapService(cmd types.RunCommand, networks, volumes map[string]map[string]any) (types.Service, []types.Warning, error) {
	service := types.Service{"image": cmd.Image}
	if cmd.Name != "" {
		service["container_name"] = cmd.Name
	}

	var warnings []types.Warning
	for _, flag := range cmd.Flags {
		switch flag.Name {
		case "ports", "environment", "env_file", "labels", "extra_hosts", "dns", "cap_add", "cap_drop", "devices", "group_add", "security_opt", "expose":
			appendString(service, flag.Name, flag.Value)
		case "volumes":
			appendString(service, "volumes", flag.Value)
			if err := registerVolume(volumes, flag.Value); err != nil {
				return nil, nil, err
			}
		case "network":
			if flag.Value == "host" || flag.Value == "none" || strings.HasPrefix(flag.Value, "container:") {
				service["network_mode"] = flag.Value
			} else {
				appendString(service, "networks", flag.Value)
				networks[flag.Value] = map[string]any{"external": true}
			}
		case "restart", "working_dir", "user", "platform", "pull_policy", "stop_signal", "stop_grace_period":
			service[flag.Name] = flag.Value
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
			} else {
				appendString(service, "gpus", flag.Value)
			}
		case "ignored":
			warnings = append(warnings, types.Warning{Message: "ignored unsupported Docker flag " + flag.Value})
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

func registerVolume(volumes map[string]map[string]any, value string) error {
	if strings.HasPrefix(value, "type=") {
		source := value
		for part := range strings.SplitSeq(value, ",") {
			key, val, ok := strings.Cut(part, "=")
			if ok && (key == "source" || key == "src") {
				source = val
				break
			}
		}
		if source == "" || strings.HasPrefix(source, ".") || strings.HasPrefix(source, "/") || strings.Contains(source, "$") {
			return nil
		}
		volumes[source] = map[string]any{"external": true}
		return nil
	}

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
