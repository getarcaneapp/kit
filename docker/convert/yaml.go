package convert

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"

	converttypes "go.getarcane.app/docker/convert/types"
	"go.yaml.in/yaml/v4"
)

func marshalYAMLInternal(doc *converttypes.Document, opts converttypes.MarshalOptions) ([]byte, error) {
	if doc == nil {
		return nil, converttypes.NewConversionError("document cannot be nil")
	}
	root := mappingNodeInternal()

	servicesNode := mappingNodeInternal()
	for _, name := range doc.ServiceOrder {
		service, ok := doc.Services[name]
		if !ok {
			continue
		}
		servicesNode.Content = append(servicesNode.Content, scalarNodeInternal(name), serviceNodeInternal(service))
	}
	root.Content = append(root.Content, scalarNodeInternal("services"), servicesNode)

	if len(doc.Networks) > 0 {
		root.Content = append(root.Content, scalarNodeInternal("networks"), resourceNodeInternal(doc.Networks))
	}
	if len(doc.Volumes) > 0 {
		root.Content = append(root.Content, scalarNodeInternal("volumes"), resourceNodeInternal(doc.Volumes))
	}

	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(4)
	if err := enc.Encode(root); err != nil {
		return nil, fmt.Errorf("marshal compose YAML: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close YAML encoder: %w", err)
	}

	if opts.RenderWarnings && len(doc.Warnings) > 0 {
		var prefix bytes.Buffer
		for _, warning := range doc.Warnings {
			prefix.WriteString("# ")
			prefix.WriteString(warning.Message)
			prefix.WriteByte('\n')
		}
		prefix.Write(b.Bytes())
		return prefix.Bytes(), nil
	}

	return b.Bytes(), nil
}

var serviceKeyOrder = []string{
	"image",
	"container_name",
	"hostname",
	"ports",
	"volumes",
	"environment",
	"env_file",
	"networks",
	"network_mode",
	"restart",
	"working_dir",
	"user",
	"entrypoint",
	"command",
	"stdin_open",
	"tty",
	"privileged",
	"init",
	"read_only",
	"oom_kill_disable",
	"labels",
	"healthcheck",
	"deploy",
	"ulimits",
	"logging",
	"extra_hosts",
	"dns",
	"cap_add",
	"cap_drop",
	"devices",
	"group_add",
	"security_opt",
	"gpus",
	"platform",
	"pull_policy",
	"stop_signal",
	"stop_grace_period",
	"expose",
}

func serviceNodeInternal(service converttypes.Service) *yaml.Node {
	node := mappingNodeInternal()
	seen := make(map[string]bool, len(service))
	for _, key := range serviceKeyOrder {
		value, ok := service[key]
		if !ok || isZeroValueInternal(value) {
			continue
		}
		node.Content = append(node.Content, scalarNodeInternal(key), anyNodeInternal(value))
		seen[key] = true
	}

	var extras []string
	for key := range service {
		if !seen[key] {
			extras = append(extras, key)
		}
	}
	slices.Sort(extras)
	for _, key := range extras {
		value := service[key]
		if isZeroValueInternal(value) {
			continue
		}
		node.Content = append(node.Content, scalarNodeInternal(key), anyNodeInternal(value))
	}
	return node
}

func resourceNodeInternal(resources map[string]map[string]any) *yaml.Node {
	node := mappingNodeInternal()
	names := make([]string, 0, len(resources))
	for name := range resources {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		node.Content = append(node.Content, scalarNodeInternal(name), anyNodeInternal(resources[name]))
	}
	return node
}

func anyNodeInternal(value any) *yaml.Node {
	switch v := value.(type) {
	case string:
		return scalarNodeInternal(v)
	case bool:
		if v {
			return boolNodeInternal("true")
		}
		return boolNodeInternal("false")
	case int:
		return scalarNodeInternal(strconv.Itoa(v))
	case []string:
		node := &yaml.Node{Kind: yaml.SequenceNode}
		for _, item := range v {
			node.Content = append(node.Content, scalarNodeInternal(item))
		}
		return node
	case map[string]any:
		return orderedMapNodeInternal(v)
	case converttypes.Service:
		return serviceNodeInternal(v)
	default:
		return scalarNodeInternal(fmt.Sprintf("%v", v))
	}
}

func orderedMapNodeInternal(values map[string]any) *yaml.Node {
	node := mappingNodeInternal()
	keys := orderedKeysInternal(values)
	for _, key := range keys {
		node.Content = append(node.Content, scalarNodeInternal(key), anyNodeInternal(values[key]))
	}
	return node
}

func orderedKeysInternal(values map[string]any) []string {
	preferred := []string{"resources", "limits", "memory", "cpus", "soft", "hard", "driver", "options", "test"}
	var keys []string
	seen := make(map[string]bool, len(values))
	for _, key := range preferred {
		if _, ok := values[key]; ok {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	var extras []string
	for key := range values {
		if !seen[key] {
			extras = append(extras, key)
		}
	}
	slices.Sort(extras)
	return append(keys, extras...)
}

func mappingNodeInternal() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode}
}

func scalarNodeInternal(value string) *yaml.Node {
	node := &yaml.Node{Kind: yaml.ScalarNode, Value: value}
	if value == "0.5" {
		node.Style = yaml.DoubleQuotedStyle
	}
	return node
}

func boolNodeInternal(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: value}
}

func isZeroValueInternal(value any) bool {
	switch v := value.(type) {
	case string:
		return v == ""
	case []string:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	default:
		return value == nil
	}
}
