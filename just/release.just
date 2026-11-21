import 'settings.just'

set working-directory := '..'

[default]
[private]
list:
    @{{ just_executable() }} --justfile {{ justfile() }} --list release

# The bump is derived from conventional commits since the module's latest tag:
# feat -> minor, any other commit -> patch; --patch, --minor, or --major forces a bump. An explicit
# version skips the bump detection, e.g. when a module moved in already tagged.
# Every module tags as <dir>/vX.Y.Z, e.g. kit/v0.5.0 or docker/convert/v0.4.0.
# Each release also points the module's kit.getarcane.app requirements at their
# latest tags, so the published go.mod matches what it was built against.
#
# Usage:
#   just release module acfs
#   just release module acfs 1.2.3
#   just release module kit --test --verbose

# Release one module: module <dir> [version] [--test] [--patch|--minor|--major] [--verbose]
[script('bash')]
module module *args:
    set -euo pipefail

    TEST=false
    YES=false
    FORCE_BUMP=""
    VERBOSE=false
    EXPLICIT_VERSION=""
    # Internal flags used by "all": --print-tag prints only the tag that would be
    # created (nothing when no release is due), --require-changes skips modules
    # without commits even when a bump flag is given.
    PRINT_TAG=false
    REQUIRE_CHANGES=false
    set -- {{ args }}
    for arg in "$@"; do
        case "$arg" in
        --test)
            TEST=true
            ;;
        --print-tag)
            PRINT_TAG=true
            ;;
        --require-changes)
            REQUIRE_CHANGES=true
            ;;
        --yes)
            YES=true
            ;;
        --patch|--minor|--major)
            if [ -n "$FORCE_BUMP" ]; then
                echo "Specify only one bump flag." >&2
                exit 1
            fi
            FORCE_BUMP="${arg#--}"
            ;;
        --verbose)
            VERBOSE=true
            ;;
        [0-9]*.[0-9]*.[0-9]*|v[0-9]*.[0-9]*.[0-9]*)
            if [ -n "$EXPLICIT_VERSION" ]; then
                echo "Specify only one explicit version." >&2
                exit 1
            fi
            EXPLICIT_VERSION="${arg#v}"
            ;;
        *)
            echo "Unknown argument: $arg" >&2
            exit 1
            ;;
        esac
    done

    if [ -n "$EXPLICIT_VERSION" ] && [ -n "$FORCE_BUMP" ]; then
        echo "An explicit version cannot be combined with a bump flag." >&2
        exit 1
    fi

    CLIFF_VERBOSE=""
    if [ "$VERBOSE" == true ]; then
        CLIFF_VERBOSE="-vv"
    fi

    # Check if git cliff is installed
    if ! command -v git-cliff &>/dev/null && ! git cliff --version &>/dev/null; then
        echo "Error: git cliff is not installed. Please install it from https://git-cliff.org/docs/installation."
        exit 1
    fi

    if [ ! -f "{{ module }}/go.mod" ]; then
        echo "Unknown module {{ module }}" >&2
        exit 1
    fi
    MODULE_DIR="{{ module }}"
    PREFIX="{{ module }}/v"
    TAG_PATTERN="^{{ module }}/v[0-9]"
    CHANGELOG_FILE="{{ module }}/CHANGELOG.md"
    CLIFF_PATH_ARGS=(--include-path "{{ module }}/**")
    PATHSPEC=(-- '{{ module }}')
    # Modules nested below this one (docker/convert under docker) release separately.
    for module in {{ modules }}; do
        module="${module#./}"
        if [[ "$module" == "{{ module }}/"* ]]; then
            CLIFF_PATH_ARGS+=(--exclude-path "$module/**")
            PATHSPEC+=(":(exclude)$module")
        fi
    done

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

    # Commits touching this module since its last tag, minus any whose
    # conventional-commit scope names a different module. A commit like
    # "fix(updater): ..." that also adds a file under docker/convert belongs to
    # the updater changelog only. Unscoped commits stay, as they are cross-cutting.
    MODULE_NAME="{{ module }}"
    SUBJECTS=""
    SKIP_ARGS=()
    while read -r hash subject; do
        scope=$(sed -nE 's/^[a-z]+\(([^)]+)\)!?:.*/\1/p' <<<"$subject")
        foreign=false
        if [ -n "$scope" ] && [ "$scope" != "$MODULE_NAME" ] && [ "$scope" != "${MODULE_NAME##*/}" ]; then
            for other in {{ modules }}; do
                other="${other#./}"
                if [ "$scope" == "$other" ] || [ "$scope" == "${other##*/}" ]; then
                    foreign=true
                    break
                fi
            done
        fi
        if [ "$foreign" == true ]; then
            SKIP_ARGS+=(--skip-commit "$hash")
        else
            SUBJECTS+="$subject"$'\n'
        fi
    done < <(git log --no-merges --format='%H %s' "${LATEST_TAG:+${LATEST_TAG}..}HEAD" "${PATHSPEC[@]}")

    # Determine the release type
    DETECTED_TYPE=""
    if [ -z "$LATEST_TAG" ]; then
        DETECTED_TYPE="minor"
    elif echo "$SUBJECTS" | grep -Eiq '^feat(\([^)]+\))?: '; then
        DETECTED_TYPE="minor"
    elif [ -n "$SUBJECTS" ]; then
        DETECTED_TYPE="patch"
    fi
    if [ -n "$EXPLICIT_VERSION" ]; then
        RELEASE_TYPE="explicit"
    elif [ -n "$FORCE_BUMP" ] && { [ -n "$DETECTED_TYPE" ] || [ "$REQUIRE_CHANGES" != true ]; }; then
        RELEASE_TYPE="$FORCE_BUMP"
    elif [ -n "$DETECTED_TYPE" ]; then
        RELEASE_TYPE="$DETECTED_TYPE"
    else
        if [ "$PRINT_TAG" != true ]; then
            echo "No commits found for {{ module }} since the latest release (${LATEST_TAG}). No new release will be created."
        fi
        exit 0
    fi

    if [ "$RELEASE_TYPE" == "explicit" ]; then
        NEW_VERSION="$EXPLICIT_VERSION"
    else
        VERSION="${LATEST_TAG#"${PREFIX}"}"
        VERSION="${VERSION:-0.0.0}"
        NEW_VERSION=$(increment_version "$VERSION" "$RELEASE_TYPE")
    fi
    TAG="${PREFIX}${NEW_VERSION}"

    if git rev-parse -q --verify "refs/tags/${TAG}" >/dev/null; then
        echo "Error: tag ${TAG} already exists." >&2
        exit 1
    fi
    if [ "$PRINT_TAG" == true ]; then
        echo "$TAG"
        exit 0
    fi
    echo "Performing $RELEASE_TYPE release..."

    # Internal requirements that lag behind the required module's latest tag.
    DEP_BUMPS=()
    for dep in $("{{ just_executable() }}" --justfile "{{ justfile() }}" release _internal-deps "$MODULE_NAME"); do
        dep_prefix="$dep/v"
        dep_tag=$(git tag -l "${dep_prefix}[0-9]*" --sort=-v:refname | head -n1)
        dep_version="v${dep_tag#"$dep_prefix"}"
        dep_path="kit.getarcane.app$([ "$dep" == "kit" ] || echo "/$dep")"
        required=$(cd "$MODULE_DIR" && go mod edit -json | jq -r --arg path "$dep_path" '.Require[] | select(.Path == $path) | .Version')
        if [ -n "$dep_tag" ] && [ "$required" != "$dep_version" ] &&
            [ "$(printf '%s\n%s\n' "$required" "$dep_version" | sort -V | tail -n1)" == "$dep_version" ]; then
            DEP_BUMPS+=("$dep_path@$dep_version")
            echo "Requirement $dep_path: $required -> $dep_version"
        fi
    done

    if [ "$TEST" == true ]; then
        echo "Test mode enabled: no files will be modified, no commits/tags/pushes/releases will be created."
    elif [ "$YES" != true ]; then
        # Confirm release creation
        read -p "This will create a new $RELEASE_TYPE release with tag $TAG. Do you want to proceed? (y/n) " CONFIRM
        if [[ "$CONFIRM" != "y" ]]; then
            echo "Release process canceled."
            exit 1
        fi
    fi

    CLIFF_ARGS=(--github-token "$(gh auth token)" --tag "$TAG" --tag-pattern "$TAG_PATTERN" "${CLIFF_PATH_ARGS[@]}" ${SKIP_ARGS[@]+"${SKIP_ARGS[@]}"} --unreleased)

    if [ "$TEST" == true ]; then
        echo "Generating changelog preview (no file write)..."
        CHANGELOG=$(git cliff $CLIFF_VERBOSE "${CLIFF_ARGS[@]}")
        echo "----- BEGIN CHANGELOG PREVIEW -----"
        echo "$CHANGELOG"
        echo "----- END CHANGELOG PREVIEW -----"
        if [ ${#DEP_BUMPS[@]} -gt 0 ]; then
            echo "Would update $MODULE_DIR/go.mod: ${DEP_BUMPS[*]}"
        fi
        echo "Would commit: release({{ module }}): $NEW_VERSION"
        echo "Would tag and push: $TAG"
        echo "Would publish GitHub release $TAG"
        echo "Test mode complete. No changes were written."
        exit 0
    fi

    if [ ${#DEP_BUMPS[@]} -gt 0 ]; then
        # The release commit stages go.mod and go.sum whole, so refuse to sweep in unrelated edits.
        if ! git diff --quiet HEAD -- "$MODULE_DIR/go.mod" "$MODULE_DIR/go.sum"; then
            echo "Error: $MODULE_DIR/go.mod or go.sum has uncommitted changes; commit or stash them first." >&2
            exit 1
        fi
        echo "Updating internal requirements..."
        (cd "$MODULE_DIR" && GOWORK=off go get "${DEP_BUMPS[@]}")
        git add "$MODULE_DIR/go.mod" "$MODULE_DIR/go.sum"
    fi

    # Generate changelog
    echo "Generating changelog..."
    touch "$CHANGELOG_FILE"
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

    # Publish the release on GitHub
    echo "Publishing GitHub release..."
    gh release create "$TAG" --title "$TAG" --notes "$CHANGELOG"

    echo "Release process complete. New version: $NEW_VERSION"

# A bump flag only changes the level for the released modules; modules without
# commits are never released.
#
# Usage:
#   just release all --test
#   just release all --minor

# Release, in dependency order, every module with commits since its latest tag
[script('bash')]
all *args:
    set -euo pipefail

    TEST=false
    YES=false
    set -- {{ args }}
    for arg in "$@"; do
        case "$arg" in
        --test)
            TEST=true
            ;;
        --yes)
            YES=true
            ;;
        [0-9]*.[0-9]*.[0-9]*|v[0-9]*.[0-9]*.[0-9]*)
            echo "Use a bump flag with all; explicit versions are only supported for a single module." >&2
            exit 1
            ;;
        esac
    done

    # Order modules so each is released after the modules it requires.
    remaining=()
    for module in {{ modules }}; do
        module="${module#./}"
        remaining+=("$module")
    done
    ordered=" "
    ORDER=()
    while [ ${#remaining[@]} -gt 0 ]; do
        next=()
        for module in "${remaining[@]}"; do
            ready=true
            for dep in $("{{ just_executable() }}" --justfile "{{ justfile() }}" release _internal-deps "$module"); do
                if [[ "$ordered" != *" $dep "* ]]; then
                    ready=false
                fi
            done
            if [ "$ready" == true ]; then
                ORDER+=("$module")
                ordered+="$module "
            else
                next+=("$module")
            fi
        done
        if [ ${#next[@]} -eq ${#remaining[@]} ]; then
            echo "Dependency cycle among: ${next[*]}" >&2
            exit 1
        fi
        remaining=(${next[@]+"${next[@]}"})
    done

    PLAN=()
    echo "Checking modules for commits since their latest release..."
    for module in "${ORDER[@]}"; do
        tag=$("{{ just_executable() }}" --justfile "{{ justfile() }}" release module "$module" --print-tag --require-changes "$@")
        if [ -n "$tag" ]; then
            PLAN+=("$module")
            echo "  $module -> $tag"
        fi
    done
    if [ ${#PLAN[@]} -eq 0 ]; then
        echo "No module has commits since its latest release. Nothing to release."
        exit 0
    fi

    if [ "$TEST" != true ] && [ "$YES" != true ]; then
        read -p "Release the ${#PLAN[@]} module(s) above in this order? (y/n) " CONFIRM
        if [[ "$CONFIRM" != "y" ]]; then
            echo "Release process canceled."
            exit 1
        fi
    fi
    for module in "${PLAN[@]}"; do
        echo "Releasing $module..."
        "{{ just_executable() }}" --justfile "{{ justfile() }}" release module "$module" --yes --require-changes "$@"
    done

# Module names of the kit.getarcane.app modules in this repo that a module requires
[script('bash')]
_internal-deps module:
    set -euo pipefail
    (cd "{{ module }}" && go mod edit -json) |
        jq -r '.Require[]? | .Path | if . == "kit.getarcane.app" then "kit" elif startswith("kit.getarcane.app/") then ltrimstr("kit.getarcane.app/") else empty end' |
        while read -r dep; do
            if [ -f "$dep/go.mod" ]; then echo "$dep"; fi
        done

# Validate the acfs GoReleaser configuration
check:
    cd acfs && goreleaser check

# Build an acfs snapshot release locally
snapshot:
    cd acfs && goreleaser release --snapshot --clean
