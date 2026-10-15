package convert

import (
	"fmt"
	"strings"

	"github.com/mattn/go-shellwords"
	kit "go.getarcane.app/kit/pkg"

	"go.getarcane.app/docker/convert/types"
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

func Parse(input string, opts types.ParseOptions) ([]types.RunCommand, error) {
	normalized := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(input, "\r\n", "\n"), "\\\n", " "))
	if normalized == "" {
		return nil, types.NewParseError("docker command must be a non-empty string")
	}

	// Parse tokenizes input line by line. A line that
	// go-shellwords rejects (for example an unterminated quote) is joined with the
	// next line and retried, so a newline inside quotes stays part of the argument.
	var statements [][]string
	pending := ""
	var pendingErr error
	for line := range strings.SplitSeq(normalized, "\n") {
		if pendingErr != nil {
			line = pending + "\n" + line
		}
		parsed, err := splitLine(line)
		if err != nil {
			pending, pendingErr = line, err
			continue
		}
		pending, pendingErr = "", nil
		statements = append(statements, parsed...)
	}
	if pendingErr != nil {
		return nil, types.NewParseError("parse command tokens: %v", pendingErr)
	}

	commands := make([]types.RunCommand, 0, len(statements))
	for _, statement := range statements {
		tokens, ok := trimCommandPrefix(statement)
		if !ok {
			return nil, types.NewParseError("expected docker or podman run/create command")
		}

		cmd, parseErr := parseRunTokens(tokens)
		if parseErr != nil {
			return nil, parseErr
		}
		commands = append(commands, cmd)
	}

	if len(commands) == 0 {
		return nil, types.NewParseError("no docker commands found")
	}

	return commands, nil
}

// splitLine splits one logical line on the shell control operators
// "&&", "||", ";", "&" and "|". go-shellwords stops at each operator and
// reports its rune position; redirections are rejected because a compose
// service has nowhere to send them.
func splitLine(line string) ([][]string, error) {
	var statements [][]string
	for {
		parser := &shellwords.Parser{ParseComment: true}
		tokens, err := parser.Parse(line)
		if err != nil {
			return nil, err
		}
		if len(tokens) > 0 {
			statements = append(statements, tokens)
		}
		if parser.Position < 0 {
			return statements, nil
		}

		rest := line[len(string([]rune(line)[:parser.Position])):]
		width := 1
		switch {
		case strings.HasPrefix(rest, "&&"), strings.HasPrefix(rest, "||"):
			width = 2
		case strings.HasPrefix(rest, "&>"), rest[0] == '<', rest[0] == '>', rest[0] >= '0' && rest[0] <= '9':
			return nil, fmt.Errorf("unsupported shell redirection near %q", rest)
		}
		line = rest[width:]
	}
}

func trimCommandPrefix(tokens []string) ([]string, bool) {
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

func parseRunTokens(tokens []string) (types.RunCommand, error) {
	var image, name, entrypoint string
	var command []string
	var flags []types.Flag
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if token == "--" {
			i++
			if i < len(tokens) {
				image = tokens[i]
				command = append(command, tokens[i+1:]...)
			}
			break
		}
		if strings.HasPrefix(token, "-") {
			if !strings.HasPrefix(token, "--") && len(token) > 2 && !strings.ContainsFunc(token[1:], func(r rune) bool {
				_, ok := boolFlags["-"+string(r)]
				return !ok
			}) {
				for _, r := range token[1:] {
					flags = append(flags, types.Flag{Name: boolFlags["-"+string(r)], Value: "true"})
				}
				continue
			}
			flag, value, consumed, err := parseFlag(token, tokens, i)
			if err != nil {
				return types.RunCommand{Name: name, Entrypoint: entrypoint, Flags: flags}, err
			}
			if flag != "" {
				if flag == "name" {
					name = value
				}
				if flag == "entrypoint" {
					entrypoint = value
				}
				flags = append(flags, types.Flag{Name: flag, Value: value})
			}
			i += consumed
			continue
		}

		image = token
		command = append(command, tokens[i+1:]...)
		break
	}

	return types.RunCommand{
		Image:      image,
		Name:       name,
		Command:    command,
		Entrypoint: entrypoint,
		Flags:      flags,
	}, kit.Ternary(image == "", types.NewParseError("no Docker image specified in command"), nil)
}

func parseFlag(token string, tokens []string, index int) (string, string, int, error) {
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
				return "", "", 0, types.NewParseError("missing value for %s flag", name)
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
			return "", "", 0, types.NewParseError("missing value for %s flag", token)
		}
		return mapped, tokens[index+1], 1, nil
	}
	if mapped, ok := boolFlags[token]; ok {
		return mapped, "true", 0, nil
	}
	return "ignored", token, 0, nil
}
