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
