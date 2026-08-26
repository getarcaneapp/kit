set working-directory := './'

modules := '. ./acfs'

_default:
    @just --list

[group('quality')]
format:
    #!/usr/bin/env bash
    set -euo pipefail
    for module in {{ modules }}; do
        gofmt -s -w "$module"
    done

[group('quality')]
vet:
    #!/usr/bin/env bash
    set -euo pipefail
    for module in {{ modules }}; do
        (cd "$module" && go vet ./...)
    done

[group('quality')]
_build-golangci-lint:
    golangci-lint custom

[group('quality')]
lint: _build-golangci-lint
    #!/usr/bin/env bash
    set -euo pipefail
    root="$(pwd)"
    for module in {{ modules }}; do
        (cd "$module" && "$root/.bin/golangci-lint-custom" run -c "$root/.github/.golangci.yml" ./...)
    done

[group('test')]
test:
    #!/usr/bin/env bash
    set -euo pipefail
    for module in {{ modules }}; do
        (cd "$module" && go test ./...)
    done

[group('test')]
test-race:
    #!/usr/bin/env bash
    set -euo pipefail
    for module in {{ modules }}; do
        (cd "$module" && go test -race ./...)
    done
