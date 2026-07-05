package convert

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	converttypes "go.getarcane.app/docker/convert/types"
	"go.yaml.in/yaml/v4"
)

var serviceNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)

func buildDocumentInternal(commands []converttypes.RunCommand, opts converttypes.Options) (*converttypes.Document, error) {
	doc := &converttypes.Document{
		Services: make(map[string]converttypes.Service),
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

func mapServiceInternal(cmd converttypes.RunCommand, doc *converttypes.Document) (converttypes.Service, error) {
	service := converttypes.Service{"image": cmd.Image}
	if cmd.Name != "" {
		service["container_name"] = cmd.Name
	}

	for _, flag := range cmd.Flags {
		switch flag.Name {
		case "ports":
			appendStringInternal(service, "ports", flag.Value)
		case "volumes":
			appendStringInternal(service, "volumes", flag.Value)
			registerVolumeInternal(doc, flag.Value)
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
			service["entrypoint"] = flag.Value
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
				return nil, converttypes.NewConversionError("invalid log option %q", flag.Value)
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
			doc.Warnings = append(doc.Warnings, converttypes.Warning{Message: "ignored unsupported Docker flag " + flag.Value})
		}
	}

	if len(cmd.Command) > 0 {
		service["command"] = joinCommandInternal(cmd.Command)
	}

	return service, nil
}

func joinCommandInternal(args []string) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, quoteCommandArgInternal(arg))
	}
	return strings.Join(parts, " ")
}

func quoteCommandArgInternal(arg string) string {
	if arg == "" {
		return "''"
	}
	if !strings.ContainsAny(arg, " \t\n;") {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
}

func appendStringInternal(service converttypes.Service, key, value string) {
	if value == "" {
		return
	}
	values, _ := service[key].([]string)
	values = append(values, value)
	service[key] = values
}

func ensureMapInternal(service converttypes.Service, key string) map[string]any {
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

func setResourceLimitInternal(service converttypes.Service, key, value string) {
	deploy := ensureMapInternal(service, "deploy")
	resources := ensureNestedMapInternal(deploy, "resources")
	limits := ensureNestedMapInternal(resources, "limits")
	limits[key] = value
}

func setUlimitInternal(service converttypes.Service, value string) error {
	name, limit, ok := strings.Cut(value, "=")
	if !ok || name == "" || limit == "" {
		return converttypes.NewConversionError("invalid ulimit %q", value)
	}
	softText, hardText, ok := strings.Cut(limit, ":")
	if !ok {
		softText = limit
		hardText = limit
	}
	soft, err := strconv.Atoi(softText)
	if err != nil {
		return converttypes.NewConversionError("invalid ulimit soft value %q: %v", softText, err)
	}
	hard, err := strconv.Atoi(hardText)
	if err != nil {
		return converttypes.NewConversionError("invalid ulimit hard value %q: %v", hardText, err)
	}
	ulimits := ensureMapInternal(service, "ulimits")
	ulimits[name] = map[string]any{"soft": soft, "hard": hard}
	return nil
}

func registerVolumeInternal(doc *converttypes.Document, value string) {
	source := value
	if strings.HasPrefix(value, "type=") {
		for part := range strings.SplitSeq(value, ",") {
			key, val, ok := strings.Cut(part, "=")
			if ok && (key == "source" || key == "src") {
				source = val
				break
			}
		}
	} else if before, _, ok := strings.Cut(value, ":"); ok {
		source = before
	}

	if source == "" || strings.HasPrefix(source, ".") || strings.HasPrefix(source, "/") || strings.Contains(source, "$") {
		return
	}
	doc.Volumes[source] = map[string]any{"external": true}
}

func serviceNameInternal(cmd converttypes.RunCommand) string {
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

func uniqueServiceNameInternal(name string, services map[string]converttypes.Service) string {
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

func envFileInternal(commands []converttypes.RunCommand) []byte {
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

func serviceResultsInternal(doc *converttypes.Document) []converttypes.ServiceResult {
	results := make([]converttypes.ServiceResult, 0, len(doc.ServiceOrder))
	for _, name := range doc.ServiceOrder {
		service := doc.Services[name]
		image, _ := service["image"].(string)
		results = append(results, converttypes.ServiceResult{Name: name, Image: image})
	}
	return results
}

func mergeExistingComposeInternal(doc *converttypes.Document, yamlData []byte) error {
	var existing map[string]any
	if err := yaml.Unmarshal(yamlData, &existing); err != nil {
		return converttypes.NewConversionError("parse existing compose YAML: %v", err)
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
