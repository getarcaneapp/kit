<div align="center">

# Arcane Converter

Docker run command to Docker Compose conversion for Go applications.

<a href="https://pkg.go.dev/go.getarcane.app/docker/convert"><img src="https://pkg.go.dev/badge/go.getarcane.app/docker/convert.svg" alt="Go Reference"></a>
<a href="https://github.com/getarcaneapp/docker/blob/main/LICENSE"><img src="https://img.shields.io/badge/license-BSD--3--Clause-blue.svg" alt="License"></a>

</div>

Arcane Docker Convert is the standalone Go module behind Arcane's Docker command conversion flow. It parses
`docker run`, `docker container run`, `docker service create`, `docker create`, and matching `podman` commands into
Docker Compose YAML.

The module can be used directly in non-Arcane Go applications. It handles command parsing, Compose document
construction, deterministic YAML rendering, compose-go validation, generated service metadata, and `.env` file content
extraction for inline environment variables.

## How it works

Using Arcane Docker Convert is a small conversion flow:

1. Pass a Docker or Podman command string to `convert.Convert`.
2. The parser removes shell comments, joins line continuations, splits semicolon-separated commands, and tokenizes
   arguments without expanding environment variables or backticks.
3. Supported Docker flags are mapped to Docker Compose service fields such as `ports`, `volumes`, `environment`,
   `env_file`, `network_mode`, `restart`, `deploy.resources.limits`, `ulimits`, `logging`, and container capability
   fields.
4. Service names are taken from `--name` or derived from the image name, sanitized, and made unique when multiple
   commands produce the same name.
5. Named volumes and external networks are registered when the command references them.
6. Existing Compose YAML can be merged before converted services are added by passing
   `types.Options.ExistingComposeYAML`.
7. The generated YAML is rendered with stable key ordering, optionally prefixed with conversion warnings, then loaded
   through compose-go to validate that it is a usable Compose project.
8. The result returns the YAML, compose-go project, converted service summaries, generated `.env` content, and warnings.

The converter does not execute shell commands, pull images, inspect Docker state, resolve environment files, create
containers, or guarantee that every Docker CLI flag has an exact Compose equivalent. Unsupported flags are ignored and
reported as warnings.

## Getting started

```sh
go get go.getarcane.app/docker/convert@latest
```

```go
package main

import (
	"fmt"
	"log"

	"go.getarcane.app/docker/convert"
	converttypes "go.getarcane.app/docker/convert/types"
)

func main() {
	result, err := convert.Convert(
		"docker run --name web -p 8080:80 -v data:/data -e FOO=bar nginx:1.27-alpine",
		converttypes.Options{},
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Print(string(result.YAML))
}
```

For more control, use the individual stages:

```go
commands, err := convert.ParseCommands(input, converttypes.ParseOptions{})
if err != nil {
	return err
}

doc, err := convert.BuildDocument(commands, converttypes.Options{
	ExistingComposeYAML: existingComposeYAML,
})
if err != nil {
	return err
}

yamlData, err := convert.MarshalYAML(doc, converttypes.MarshalOptions{
	RenderWarnings: true,
})
```

## Supported inputs

The parser accepts these command forms:

- `docker run`
- `docker container run`
- `docker create`
- `docker service create`
- `podman run`
- `podman create`

Multiple commands can be provided in one string when separated by semicolons. Shell line continuations are joined before
parsing, and comments outside quoted strings are removed.

## Package layout

- `convert`: public conversion API, parser, document builder, YAML renderer, and compose-go validation.
- `types`: stable public DTOs for options, results, parsed commands, warnings, service documents, and typed conversion
  errors.
- `testdata`: golden Compose YAML fixtures used by package tests.

## Development

```sh
go test ./...
```

From the repository root:

```sh
just release convert v1.2.3
```

## License

Arcane Docker Convert is released under the BSD 3-Clause License.
