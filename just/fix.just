import 'settings.just'

set working-directory := '..'

[default]
[private]
list:
    @{{ just_executable() }} --justfile {{ justfile() }} --list fix

# Apply go fix to every module
[script('bash')]
all:
    set -euo pipefail
    for module in {{ modules }}; do
        (cd "$module" && go fix ./...)
    done
