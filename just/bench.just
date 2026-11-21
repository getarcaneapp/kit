set working-directory := '..'

[default]
[private]
list:
    @{{ just_executable() }} --justfile {{ justfile() }} --list bench

# acfs list, read, and write paths with allocation counts
acfs:
    cd acfs && go test -run '^$' -bench 'Benchmark(List|ReadTo|WriteFrom)' -benchmem .
