import 'settings.just'

set working-directory := '..'

[default]
[private]
list:
    @{{ just_executable() }} --justfile {{ justfile() }} --list test

# Every module
[script('bash')]
all:
    set -euo pipefail
    for module in {{ modules }}; do
        (cd "$module" && go test ./...)
    done

# Every module with the race detector
[script('bash')]
race:
    set -euo pipefail
    for module in {{ modules }}; do
        (cd "$module" && go test -race ./...)
    done
