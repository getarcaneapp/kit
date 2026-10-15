package convert

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"go.yaml.in/yaml/v4"

	"go.getarcane.app/docker/convert/types"
)

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

func Marshal(doc *types.Document, opts types.MarshalOptions) ([]byte, error) {
	if doc == nil {
		return nil, types.NewConversionError("document cannot be nil")
	}

	var services []*yaml.Node
	for _, name := range doc.ServiceOrder {
		service, ok := doc.Services[name]
		if !ok {
			continue
		}
		services = append(services, scalarNode(name), anyNode(service))
	}
	content := []*yaml.Node{scalarNode("services"), new(yaml.Node{Kind: yaml.MappingNode, Content: services})}

	if len(doc.Networks) > 0 {
		content = append(content, scalarNode("networks"), resourceNode(doc.Networks))
	}
	if len(doc.Volumes) > 0 {
		content = append(content, scalarNode("volumes"), resourceNode(doc.Volumes))
	}

	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	// Two-space indentation matches the golden files and docker compose's own
	// canonical output style.
	enc.SetIndent(2)
	if err := enc.Encode(new(yaml.Node{Kind: yaml.MappingNode, Content: content})); err != nil {
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

func resourceNode(resources map[string]map[string]any) *yaml.Node {
	var content []*yaml.Node
	for _, name := range slices.Sorted(maps.Keys(resources)) {
		content = append(content, scalarNode(name), anyNode(resources[name]))
	}
	return new(yaml.Node{Kind: yaml.MappingNode, Content: content})
}

func anyNode(value any) *yaml.Node {
	switch v := value.(type) {
	case string:
		return scalarNode(v)
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.FormatBool(v)}
	case int:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.Itoa(v)}
	case int64:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: strconv.FormatInt(v, 10)}
	case []string:
		var content []*yaml.Node
		for _, item := range v {
			content = append(content, scalarNode(item))
		}
		return &yaml.Node{Kind: yaml.SequenceNode, Content: content}
	case map[string]any:
		preferred := []string{"resources", "limits", "memory", "cpus", "soft", "hard", "driver", "options", "test"}
		var content []*yaml.Node
		for _, key := range slices.Concat(preferred, slices.DeleteFunc(slices.Sorted(maps.Keys(v)), func(name string) bool { return slices.Contains(preferred, name) })) {
			item, ok := v[key]
			if !ok {
				continue
			}
			content = append(content, scalarNode(key), anyNode(item))
		}
		return new(yaml.Node{Kind: yaml.MappingNode, Content: content})
	case types.Service:
		var content []*yaml.Node
		for _, key := range slices.Concat(serviceKeyOrder, slices.DeleteFunc(slices.Sorted(maps.Keys(v)), func(name string) bool { return slices.Contains(serviceKeyOrder, name) })) {
			if isZeroValue(v[key]) {
				continue
			}
			content = append(content, scalarNode(key), anyNode(v[key]))
		}
		return new(yaml.Node{Kind: yaml.MappingNode, Content: content})
	default:
		return &yaml.Node{Kind: yaml.ScalarNode, Value: fmt.Sprintf("%v", v)}
	}
}

// scalarNode returns a string node. The encoder quotes it only when
// the plain form would resolve to another type, such as "0.5" or "30".
func scalarNode(value string) *yaml.Node {
	node := &yaml.Node{}
	node.SetString(value)
	return node
}

func isZeroValue(value any) bool {
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
