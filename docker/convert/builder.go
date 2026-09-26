package convert

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/compose-spec/compose-go/v2/format"
	compose "github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/go-units"
	"go.getarcane.app/docker/convert/types"
	"go.yaml.in/yaml/v4"
)

var serviceNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

func Build(commands []types.RunCommand, opts types.Options) (*types.Document, error) {
	doc := &types.Document{
		Services: make(map[string]types.Service),
		Networks: make(map[string]map[string]any),
		Volumes:  make(map[string]map[string]any),
	}

	if len(opts.ExistingComposeYAML) > 0 {
		if err := mergeExistingComposeInternal(doc, opts.ExistingComposeYAML); err != nil {
			return nil, err
		}
	}

	for _, cmd := range commands {
		name := serviceNameInternal(cmd)
		name = uniqueServiceNameInternal(name, doc.Services)
		service, err := mapServiceInternal(cmd, doc)
		if err != nil {
			return nil, err
		}
		doc.Services[name] = service
		doc.ServiceOrder = append(doc.ServiceOrder, name)
	}

	return doc, nil
}

func mapServiceInternal(cmd types.RunCommand, doc *types.Document) (types.Service, error) {
	service := types.Service{"image": cmd.Image}
	if cmd.Name != "" {
		service["container_name"] = cmd.Name
	}

	for _, flag := range cmd.Flags {
		switch flag.Name {
		case "ports":
			appendStringInternal(service, "ports", flag.Value)
		case "volumes":
			appendStringInternal(service, "volumes", flag.Value)
			if err := registerVolumeInternal(doc, flag.Value); err != nil {
				return nil, err
			}
		case "environment":
			appendStringInternal(service, "environment", flag.Value)
		case "env_file":
			appendStringInternal(service, "env_file", flag.Value)
		case "network":
			if flag.Value == "host" || flag.Value == "none" || strings.HasPrefix(flag.Value, "container:") {
				service["network_mode"] = flag.Value
			} else {
				appendStringInternal(service, "networks", flag.Value)
				doc.Networks[flag.Value] = map[string]any{"external": true}
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
		case "memory":
			setResourceLimitInternal(service, "memory", flag.Value)
		case "cpus":
			setResourceLimitInternal(service, "cpus", flag.Value)
		case "labels", "extra_hosts", "dns", "cap_add", "cap_drop", "devices", "group_add", "security_opt", "expose":
			appendStringInternal(service, flag.Name, flag.Value)
		case "ulimits":
			if err := setUlimitInternal(service, flag.Value); err != nil {
				return nil, err
			}
		case "logging.driver":
			logging := ensureMapInternal(service, "logging")
			logging["driver"] = flag.Value
		case "logging.options":
			key, value, ok := strings.Cut(flag.Value, "=")
			if !ok {
				return nil, types.NewConversionError("invalid log option %q", flag.Value)
			}
			logging := ensureMapInternal(service, "logging")
			options := ensureNestedMapInternal(logging, "options")
			options[key] = value
		case "interactive":
			service["stdin_open"] = true
		case "tty", "privileged", "init", "read_only", "oom_kill_disable":
			service[flag.Name] = true
		case "gpus":
			if flag.Value == "all" {
				service["gpus"] = "all"
			} else {
				appendStringInternal(service, "gpus", flag.Value)
			}
		case "ignored":
			doc.Warnings = append(doc.Warnings, types.Warning{Message: "ignored unsupported Docker flag " + flag.Value})
		}
	}

	if len(cmd.Command) > 0 {
		service["command"] = slices.Clone(cmd.Command)
	}

	return service, nil
}

func appendStringInternal(service types.Service, key, value string) {
	if value == "" {
		return
	}
	values, _ := service[key].([]string)
	values = append(values, value)
	service[key] = values
}

func ensureMapInternal(service types.Service, key string) map[string]any {
	if existing, ok := service[key].(map[string]any); ok {
		return existing
	}
	next := make(map[string]any)
	service[key] = next
	return next
}

func ensureNestedMapInternal(parent map[string]any, key string) map[string]any {
	if existing, ok := parent[key].(map[string]any); ok {
		return existing
	}
	next := make(map[string]any)
	parent[key] = next
	return next
}

func setResourceLimitInternal(service types.Service, key, value string) {
	deploy := ensureMapInternal(service, "deploy")
	resources := ensureNestedMapInternal(deploy, "resources")
	limits := ensureNestedMapInternal(resources, "limits")
	limits[key] = value
}

func setUlimitInternal(service types.Service, value string) error {
	ulimit, err := units.ParseUlimit(value)
	if err != nil {
		return types.NewConversionError("invalid ulimit %q: %v", value, err)
	}
	ulimits := ensureMapInternal(service, "ulimits")
	ulimits[ulimit.Name] = map[string]any{"soft": ulimit.Soft, "hard": ulimit.Hard}
	return nil
}

func registerVolumeInternal(doc *types.Document, value string) error {
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
		doc.Volumes[source] = map[string]any{"external": true}
		return nil
	}

	volume, err := format.ParseVolume(value)
	if err != nil {
		return types.NewConversionError("invalid volume %q: %v", value, err)
	}
	if volume.Type != compose.VolumeTypeVolume || volume.Source == "" || strings.Contains(volume.Source, "$") {
		return nil
	}
	doc.Volumes[volume.Source] = map[string]any{"external": true}
	return nil
}

func serviceNameInternal(cmd types.RunCommand) string {
	if cmd.Name != "" {
		return sanitizeServiceNameInternal(cmd.Name)
	}
	image := cmd.Image
	if slash := strings.LastIndex(image, "/"); slash >= 0 {
		image = image[slash+1:]
	}
	if before, _, ok := strings.Cut(image, ":"); ok {
		image = before
	}
	if before, _, ok := strings.Cut(image, "@"); ok {
		image = before
	}
	name := sanitizeServiceNameInternal(image)
	if name == "" {
		return "app"
	}
	return name
}

func sanitizeServiceNameInternal(name string) string {
	name = serviceNameSanitizer.ReplaceAllString(strings.TrimSpace(name), "-")
	name = strings.Trim(name, "-_.")
	return strings.ToLower(name)
}

func uniqueServiceNameInternal(name string, services map[string]types.Service) string {
	if _, ok := services[name]; !ok {
		return name
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s-%d", name, i)
		if _, ok := services[candidate]; !ok {
			return candidate
		}
	}
}

func envFileInternal(commands []types.RunCommand) []byte {
	var lines []string
	for _, cmd := range commands {
		for _, flag := range cmd.Flags {
			if flag.Name == "environment" && strings.Contains(flag.Value, "=") {
				lines = append(lines, flag.Value)
			}
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

func serviceResultsInternal(doc *types.Document) []types.ServiceResult {
	results := make([]types.ServiceResult, 0, len(doc.ServiceOrder))
	for _, name := range doc.ServiceOrder {
		service := doc.Services[name]
		image, _ := service["image"].(string)
		results = append(results, types.ServiceResult{Name: name, Image: image})
	}
	return results
}

func mergeExistingComposeInternal(doc *types.Document, yamlData []byte) error {
	var existing map[string]any
	if err := yaml.Unmarshal(yamlData, &existing); err != nil {
		return types.NewConversionError("parse existing compose YAML: %v", err)
	}

	if services, ok := existing["services"].(map[string]any); ok {
		names := make([]string, 0, len(services))
		for name := range services {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			if svc, ok := services[name].(map[string]any); ok {
				doc.Services[name] = svc
				doc.ServiceOrder = append(doc.ServiceOrder, name)
			}
		}
	}
	return nil
}
