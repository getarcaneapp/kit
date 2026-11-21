import 'settings.just'

set working-directory := '..'

[default]
[private]
list:
    @{{ just_executable() }} --justfile {{ justfile() }} --list format

# Format every target
[arg('check', long, value='--check')]
[parallel]
all check='': (go check) (just check)

# Go modules with the formatters configured in .golangci.yml
[arg('check', long, value='--check')]
[script('bash')]
go check='':
    shopt -s nullglob
    failed=0
    for module in {{ modules }}; do
        (
            cd "$module" || exit 1
            go list -e -f '{{{{.Dir}}' ./... | while read -r dir; do printf '%s\n' "$dir"/*.go; done |
                xargs golangci-lint fmt -c "{{ golangci_config }}" {{ if check != '' { '--diff' } else { '' } }}
        ) || failed=1
    done
    exit "${failed}"

# Root Justfile and its modules
[arg('check', long, value='--check')]
[script('bash')]
just check='':
    set -euo pipefail
    for file in Justfile just/*.just; do
        just --fmt {{ check }} --justfile "$file"
    done
