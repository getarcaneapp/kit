set working-directory := './'

modules := '. ./acfs ./builds'

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

[group('test')]
benchmark-acfs:
    cd acfs && go test -run '^$' -bench 'Benchmark(List|ReadTo|WriteFrom)' -benchmem .

[group('release')]
release-check:
    cd acfs && goreleaser check

[group('release')]
snapshot:
    cd acfs && goreleaser release --snapshot --clean

# Create a new release for a module (use --test to dry-run without writing anything).
# The bump is derived from conventional commits since the module's latest tag:
# feat -> minor, fix -> patch, --major forces a major release. An explicit
# version skips the bump detection, e.g. when a module moved in already tagged.
# Nested modules tag as <module>/vX.Y.Z, the root kit module as vX.Y.Z.
#
# Usage:
#   just release acfs
#   just release acfs 1.2.3
#   just release kit --test --verbose
[group('release')]
release module *args:
    #!/usr/bin/env bash
    set -euo pipefail

    TEST=false
    FORCE_MAJOR=false
    VERBOSE=false
    EXPLICIT_VERSION=""
    set -- {{ args }}
    for arg in "$@"; do
        case "$arg" in
        --test)
            TEST=true
            ;;
        --major)
            FORCE_MAJOR=true
            ;;
        --verbose)
            VERBOSE=true
            ;;
        [0-9]*.[0-9]*.[0-9]*|v[0-9]*.[0-9]*.[0-9]*)
            EXPLICIT_VERSION="${arg#v}"
            ;;
        *)
            echo "Unknown argument: $arg" >&2
            exit 1
            ;;
        esac
    done

    CLIFF_VERBOSE=""
    if [ "$VERBOSE" == true ]; then
        CLIFF_VERBOSE="-vv"
    fi

    # Check if git cliff is installed
    if ! command -v git-cliff &>/dev/null && ! git cliff --version &>/dev/null; then
        echo "Error: git cliff is not installed. Please install it from https://git-cliff.org/docs/installation."
        exit 1
    fi

    case "{{ module }}" in
        kit|.)
            PREFIX="v"
            TAG_PATTERN="^v[0-9]"
            CHANGELOG_FILE="CHANGELOG.md"
            CLIFF_PATH_ARGS=(--exclude-path "acfs/**")
            PATHSPEC=(-- . ':(exclude)acfs')
            ;;
        *)
            if [ ! -f "{{ module }}/go.mod" ]; then
                echo "Unknown module {{ module }}" >&2
                exit 1
            fi
            PREFIX="{{ module }}/v"
            TAG_PATTERN="^{{ module }}/v[0-9]"
            CHANGELOG_FILE="{{ module }}/CHANGELOG.md"
            CLIFF_PATH_ARGS=(--include-path "{{ module }}/**")
            PATHSPEC=(-- '{{ module }}')
            ;;
    esac

    # Function to increment the version
    increment_version() {
        local version=$1
        local part=$2

        IFS='.' read -r -a parts <<<"$version"
        if [ "$part" == "major" ]; then
            parts[0]=$((parts[0] + 1))
            parts[1]=0
            parts[2]=0
        elif [ "$part" == "minor" ]; then
            parts[1]=$((parts[1] + 1))
            parts[2]=0
        elif [ "$part" == "patch" ]; then
            parts[2]=$((parts[2] + 1))
        fi
        echo "${parts[0]}.${parts[1]}.${parts[2]}"
    }

    # Get the module's latest version tag, ignoring non-version tags
    LATEST_TAG=$(git tag -l "${PREFIX}[0-9]*" --sort=-v:refname | head -n1 || echo "")

    # Determine the release type
    if [ -n "$EXPLICIT_VERSION" ]; then
        RELEASE_TYPE="explicit"
    elif [ "$FORCE_MAJOR" == true ]; then
        RELEASE_TYPE="major"
    elif [ -z "$LATEST_TAG" ]; then
        RELEASE_TYPE="minor"
    else
        # Look only at commit subjects touching this module since its last tag
        SUBJECTS=$(git log --no-merges --format=%s "${LATEST_TAG}..HEAD" "${PATHSPEC[@]}")

        if echo "$SUBJECTS" | grep -Eiq '^feat(\([^)]+\))?: '; then
            RELEASE_TYPE="minor"
        elif echo "$SUBJECTS" | grep -Eiq '^fix(\([^)]+\))?: '; then
            RELEASE_TYPE="patch"
        else
            echo "No 'fix' or 'feat' commits found for {{ module }} since the latest release (${LATEST_TAG}). No new release will be created."
            echo "Commits since ${LATEST_TAG}:"
            git log --oneline --no-merges "${LATEST_TAG}..HEAD" "${PATHSPEC[@]}" || true
            exit 0
        fi
    fi

    if [ "$RELEASE_TYPE" == "explicit" ]; then
        echo "Performing explicit release..."
        NEW_VERSION="$EXPLICIT_VERSION"
    else
        VERSION="${LATEST_TAG#"${PREFIX}"}"
        VERSION="${VERSION:-0.0.0}"

        echo "Performing $RELEASE_TYPE release..."
        NEW_VERSION=$(increment_version "$VERSION" "$RELEASE_TYPE")
    fi
    TAG="${PREFIX}${NEW_VERSION}"

    if git rev-parse -q --verify "refs/tags/${TAG}" >/dev/null; then
        echo "Error: tag ${TAG} already exists." >&2
        exit 1
    fi

    if [ "$TEST" == true ]; then
        echo "Test mode enabled: no files will be modified, no commits/tags/pushes/releases will be created."
    else
        # Confirm release creation
        read -p "This will create a new $RELEASE_TYPE release with tag $TAG. Do you want to proceed? (y/n) " CONFIRM
        if [[ "$CONFIRM" != "y" ]]; then
            echo "Release process canceled."
            exit 1
        fi
    fi

    CLIFF_ARGS=(--github-token "$(gh auth token)" --tag "$TAG" --tag-pattern "$TAG_PATTERN" "${CLIFF_PATH_ARGS[@]}" --unreleased)

    if [ "$TEST" == true ]; then
        echo "Generating changelog preview (no file write)..."
        CHANGELOG=$(git cliff $CLIFF_VERBOSE "${CLIFF_ARGS[@]}")
        echo "----- BEGIN CHANGELOG PREVIEW -----"
        echo "$CHANGELOG"
        echo "----- END CHANGELOG PREVIEW -----"
        echo "Would commit: release({{ module }}): $NEW_VERSION"
        echo "Would tag and push: $TAG"
        echo "Would create GitHub draft release $TAG"
        echo "Test mode complete. No changes were written."
        exit 0
    fi

    # Generate changelog
    echo "Generating changelog..."
    git cliff $CLIFF_VERBOSE "${CLIFF_ARGS[@]}" --prepend "$CHANGELOG_FILE"
    git add "$CHANGELOG_FILE"

    # Commit the changes with the new version
    git commit -m "release({{ module }}): $NEW_VERSION"

    # Create and push the Git tag in two steps to ensure the release workflow
    # triggers on the tag push
    git tag -a "$TAG" -m "$NEW_VERSION"
    git push
    git push origin "$TAG"

    # Extract the changelog content for the latest release
    echo "Extracting changelog content for version $NEW_VERSION..."
    CHANGELOG=$(awk '/^## / { if (found) exit; found=1; next } found' "$CHANGELOG_FILE")

    if [ -z "$CHANGELOG" ]; then
        echo "Error: Could not extract changelog for version $NEW_VERSION."
        exit 1
    fi

    # Create the draft release on GitHub
    echo "Creating GitHub draft release..."
    gh release create "$TAG" --title "$TAG" --notes "$CHANGELOG" --draft

    echo "Release process complete. New version: $NEW_VERSION"
