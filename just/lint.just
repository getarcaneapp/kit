import 'settings.just'

set working-directory := '..'

[default]
[private]
list:
    @{{ just_executable() }} --justfile {{ justfile() }} --list lint

all: go vet

# golangci-lint in every module
[script('bash')]
go:
    set -euo pipefail
    for module in {{ modules }}; do
        (cd "$module" && golangci-lint run -c "{{ golangci_config }}" ./...)
    done

# go vet in every module
[script('bash')]
vet:
    set -euo pipefail
    for module in {{ modules }}; do
        (cd "$module" && go vet ./...)
    done
