import 'settings.just'

set working-directory := '..'

[default]
[private]
list:
    @{{ just_executable() }} --justfile {{ justfile() }} --list deps

# Local replacements stay unchanged; indirect versions may move to satisfy direct updates.

# Direct dependencies in every module, then sync the workspace (requires jq). Add --all to include indirect ones.
[arg('all', long, value='true')]
[arg('patch', long, value='true')]
[script('bash')]
update patch='false' all='false':
    set -euo pipefail
    version="{{ if patch == 'true' { 'patch' } else { 'upgrade' } }}"
    command -v jq >/dev/null || { echo 'jq is required.' >&2; exit 1; }
    for module in {{ modules }}; do
        (
            cd "$module"
            export GOWORK=off
            echo "Updating {{ if all == 'true' { 'all' } else { 'direct' } }} dependencies in $module..."
            direct=$(go mod edit -json | jq -r --argjson all {{ all }} '
                [.Replace[]? | select(.New.Version == null) | .Old.Path] as $local
                | .Require[]?
                | select($all or .Indirect != true)
                | select(.Path as $path | $local | index($path) | not)
                | .Path')
            dependencies=()
            while IFS= read -r dependency; do
                if [ -n "$dependency" ]; then
                    dependencies+=("$dependency@$version")
                fi
            done <<<"$direct"
            if [ "${#dependencies[@]}" -gt 0 ]; then
                go get "${dependencies[@]}"
            fi
        )
    done
    go work sync
    for module in {{ modules }}; do
        echo "Tidying $module..."
        (cd "$module" && GOWORK=off go mod tidy)
    done
