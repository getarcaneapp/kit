package convert

import (
	"strings"

	"github.com/mattn/go-shellwords"
	converttypes "go.getarcane.app/docker/convert/types"
)

var valueFlags = map[string]string{
	"--name":           "name",
	"--hostname":       "hostname",
	"--publish":        "ports",
	"--port":           "ports",
	"-p":               "ports",
	"--volume":         "volumes",
	"-v":               "volumes",
	"--mount":          "volumes",
	"--env":            "environment",
	"-e":               "environment",
	"--env-file":       "env_file",
	"--network":        "network",
	"--restart":        "restart",
	"--workdir":        "working_dir",
	"-w":               "working_dir",
	"--user":           "user",
	"-u":               "user",
	"--entrypoint":     "entrypoint",
	"--health-cmd":     "healthcheck",
	"--memory":         "memory",
	"-m":               "memory",
	"--cpus":           "cpus",
	"--label":          "labels",
	"-l":               "labels",
	"--ulimit":         "ulimits",
	"--log-driver":     "logging.driver",
	"--log-opt":        "logging.options",
	"--add-host":       "extra_hosts",
	"--dns":            "dns",
	"--gpus":           "gpus",
	"--platform":       "platform",
	"--ip":             "ipv4_address",
	"--network-alias":  "aliases",
	"--pull":           "pull_policy",
	"--stop-signal":    "stop_signal",
	"--stop-timeout":   "stop_grace_period",
	"--cap-add":        "cap_add",
	"--cap-drop":       "cap_drop",
	"--device":         "devices",
	"--group-add":      "group_add",
	"--security-opt":   "security_opt",
	"--add-hosts":      "extra_hosts",
	"--expose":         "expose",
	"--container-name": "name",
}

var boolFlags = map[string]string{
	"--detach":           "detach",
	"-d":                 "detach",
	"--interactive":      "interactive",
	"-i":                 "interactive",
	"--tty":              "tty",
	"-t":                 "tty",
	"--rm":               "rm",
	"--privileged":       "privileged",
	"--init":             "init",
	"--read-only":        "read_only",
	"--oom-kill-disable": "oom_kill_disable",
}

func parseCommandsInternal(input string, opts converttypes.ParseOptions) ([]converttypes.RunCommand, error) {
	normalized := normalizeInputInternal(input)
	if strings.TrimSpace(normalized) == "" {
		return nil, converttypes.NewParseError("docker command must be a non-empty string")
	}

	parts := splitCommandsInternal(normalized)
	commands := make([]converttypes.RunCommand, 0, len(parts))
	for _, part := range parts {
		tokens, err := shellTokensInternal(part)
		if err != nil {
			return nil, converttypes.NewParseError("parse command tokens: %v", err)
		}
		if len(tokens) == 0 {
			continue
		}

		tokens, ok := trimCommandPrefixInternal(tokens)
		if !ok {
			return nil, converttypes.NewParseError("expected docker or podman run/create command")
		}

		cmd, err := parseRunTokensInternal(tokens)
		if err != nil {
			return nil, err
		}
		commands = append(commands, cmd)
	}

	if len(commands) == 0 {
		return nil, converttypes.NewParseError("no docker commands found")
	}

	return commands, nil
}

func normalizeInputInternal(input string) string {
	lines := strings.Split(input, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = stripCommentInternal(line)
		out = append(out, line)
	}

	joined := strings.Join(out, "\n")
	joined = strings.ReplaceAll(joined, "\\\r\n", " ")
	joined = strings.ReplaceAll(joined, "\\\n", " ")
	return strings.TrimSpace(joined)
}

func stripCommentInternal(line string) string {
	var b strings.Builder
	var quote rune
	escaped := false
	for _, r := range line {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			b.WriteRune(r)
			escaped = true
			continue
		}
		if quote == 0 && (r == '\'' || r == '"') {
			quote = r
			b.WriteRune(r)
			continue
		}
		if quote != 0 && r == quote {
			quote = 0
			b.WriteRune(r)
			continue
		}
		if quote == 0 && r == '#' {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

func splitCommandsInternal(input string) []string {
	var parts []string
	var b strings.Builder
	var quote rune
	escaped := false
	for _, r := range input {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			b.WriteRune(r)
			escaped = true
			continue
		}
		if quote == 0 && (r == '\'' || r == '"') {
			quote = r
			b.WriteRune(r)
			continue
		}
		if quote != 0 && r == quote {
			quote = 0
			b.WriteRune(r)
			continue
		}
		if quote == 0 && r == ';' {
			if part := strings.TrimSpace(b.String()); part != "" {
				parts = append(parts, part)
			}
			b.Reset()
			continue
		}
		b.WriteRune(r)
	}
	if part := strings.TrimSpace(b.String()); part != "" {
		parts = append(parts, part)
	}
	return parts
}

func shellTokensInternal(command string) ([]string, error) {
	parser := shellwords.NewParser()
	parser.ParseEnv = false
	parser.ParseBacktick = false
	return parser.Parse(command)
}

func trimCommandPrefixInternal(tokens []string) ([]string, bool) {
	if len(tokens) < 2 || (tokens[0] != "docker" && tokens[0] != "podman") {
		return nil, false
	}
	switch {
	case tokens[1] == "run" || tokens[1] == "create":
		return tokens[2:], true
	case len(tokens) >= 3 && tokens[1] == "container" && tokens[2] == "run":
		return tokens[3:], true
	case len(tokens) >= 3 && tokens[1] == "service" && tokens[2] == "create":
		return tokens[3:], true
	default:
		return nil, false
	}
}

func parseRunTokensInternal(tokens []string) (converttypes.RunCommand, error) {
	var cmd converttypes.RunCommand
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if token == "--" {
			i++
			if i < len(tokens) {
				cmd.Image = tokens[i]
				cmd.Command = append(cmd.Command, tokens[i+1:]...)
			}
			break
		}
		if strings.HasPrefix(token, "-") {
			if flags, ok := parseShortClusterInternal(token); ok {
				cmd.Flags = append(cmd.Flags, flags...)
				continue
			}
			flag, value, consumed, err := parseFlagInternal(token, tokens, i)
			if err != nil {
				return cmd, err
			}
			if flag != "" {
				if flag == "name" {
					cmd.Name = value
				}
				if flag == "entrypoint" {
					cmd.Entrypoint = value
				}
				cmd.Flags = append(cmd.Flags, converttypes.Flag{Name: flag, Value: value})
			}
			i += consumed
			continue
		}

		cmd.Image = token
		cmd.Command = append(cmd.Command, tokens[i+1:]...)
		break
	}

	if cmd.Image == "" {
		return cmd, converttypes.NewParseError("no Docker image specified in command")
	}

	return cmd, nil
}

func parseFlagInternal(token string, tokens []string, index int) (string, string, int, error) {
	if strings.HasPrefix(token, "--") {
		name, value, hasValue := strings.Cut(token, "=")
		if mapped, ok := boolFlags[name]; ok {
			return mapped, "true", 0, nil
		}
		if mapped, ok := valueFlags[name]; ok {
			if hasValue {
				return mapped, value, 0, nil
			}
			if index+1 >= len(tokens) {
				return "", "", 0, converttypes.NewParseError("missing value for %s flag", name)
			}
			return mapped, tokens[index+1], 1, nil
		}
		if !hasValue && index+1 < len(tokens) && !strings.HasPrefix(tokens[index+1], "-") {
			return "ignored", token + "=" + tokens[index+1], 1, nil
		}
		return "ignored", token, 0, nil
	}

	if mapped, ok := valueFlags[token[:2]]; ok && len(token) > 2 {
		return mapped, token[2:], 0, nil
	}
	if mapped, ok := valueFlags[token]; ok {
		if index+1 >= len(tokens) {
			return "", "", 0, converttypes.NewParseError("missing value for %s flag", token)
		}
		return mapped, tokens[index+1], 1, nil
	}
	if mapped, ok := boolFlags[token]; ok {
		return mapped, "true", 0, nil
	}
	return "ignored", token, 0, nil
}

func parseShortClusterInternal(token string) ([]converttypes.Flag, bool) {
	if !strings.HasPrefix(token, "-") || strings.HasPrefix(token, "--") || len(token) <= 2 {
		return nil, false
	}
	var flags []converttypes.Flag
	for _, r := range token[1:] {
		mapped, ok := boolFlags["-"+string(r)]
		if !ok {
			return nil, false
		}
		flags = append(flags, converttypes.Flag{Name: mapped, Value: "true"})
	}
	return flags, len(flags) > 0
}
