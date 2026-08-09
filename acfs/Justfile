set working-directory := './'

_default:
    @just --list

[group('quality')]
format:
    gofmt -s -w .

[group('quality')]
vet:
    go vet ./...

[group('quality')]
lint:
    golangci-lint run ./...

[group('test')]
test:
    go test ./...

[group('test')]
test-race:
    go test -race ./...

[group('test')]
benchmark:
    go test -run '^$' -bench 'Benchmark(List|ReadTo|WriteFrom)' -benchmem .

[group('release')]
release-check:
    goreleaser check

[group('release')]
snapshot:
    goreleaser release --snapshot --clean

# Create and push a new version tag.
#
# Usage:
#   just release          # auto-increments patch from latest tag
#   just release 1.2.3    # explicit version
[group('release')]
release version="":
    #!/usr/bin/env bash
    set -euo pipefail

    if [ -z "{{ version }}" ]; then
        LATEST_TAG=$(git tag -l 'v*' --sort=-v:refname | head -n1 || echo "")
        if [ -z "$LATEST_TAG" ]; then
            echo "No existing tag found; defaulting to v0.0.1"
            NEW_VERSION="0.0.1"
        else
            VERSION="${LATEST_TAG#v}"
            IFS='.' read -r MAJOR MINOR PATCH <<< "$VERSION"
            NEW_PATCH=$((PATCH + 1))
            NEW_VERSION="${MAJOR}.${MINOR}.${NEW_PATCH}"
        fi
    else
        NEW_VERSION="{{ version }}"
    fi

    echo "==> Tagging v${NEW_VERSION}"
    git tag "v${NEW_VERSION}" -m "v${NEW_VERSION}"
    git push origin "v${NEW_VERSION}"
    echo "==> Pushed v${NEW_VERSION}"
