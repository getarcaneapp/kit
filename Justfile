[default]
_list:
    @{{ just_executable() }} --list

# Format: go, just, all. Add --check to verify.
[group('quality')]
mod format 'just/format.just'

# Lint: go, vet, all
[group('quality')]
mod lint 'just/lint.just'

# Apply go fix: all
[group('quality')]
mod fix 'just/fix.just'

# Run tests: all, race
[group('test')]
mod test 'just/test.just'

# Benchmarks: acfs
[group('test')]
mod bench 'just/bench.just'

# Manage dependencies: update [--patch] [--all]
[group('dependencies')]
mod deps 'just/deps.just'

# Release tooling: module <dir> [version] [flags], all [flags], check, snapshot
[group('release')]
mod release 'just/release.just'
