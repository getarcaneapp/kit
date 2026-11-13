module kit.getarcane.app/updater

go 1.27

replace (
	kit.getarcane.app => ../kit
	kit.getarcane.app/docker => ../docker
)

require (
	github.com/Masterminds/semver/v3 v3.5.0
	github.com/containerd/errdefs v1.0.0
	github.com/containerd/platforms v1.0.0-rc.5
	github.com/distribution/reference v0.6.0
	github.com/docker/cli v29.9.3+incompatible
	github.com/google/go-containerregistry v0.22.1
	github.com/moby/docker-image-spec v1.3.1
	github.com/moby/moby/api v1.57.0
	github.com/moby/moby/client v0.6.3
	github.com/opencontainers/go-digest v1.0.0
	kit.getarcane.app v0.7.2
	kit.getarcane.app/docker v0.3.3
)

require (
	github.com/Azure/go-ansiterm v0.0.0-20261006220739-8c912ac31dd4 // indirect
	github.com/Microsoft/go-winio v0.6.4 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/containerd/errdefs/pkg v0.3.0 // indirect
	github.com/containerd/log v0.2.0 // indirect
	github.com/docker/docker-credential-helpers v0.9.9 // indirect
	github.com/docker/go-connections v0.8.2 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/klauspost/compress v1.20.2 // indirect
	github.com/moby/term v0.5.2 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/samber/mo v1.17.0 // indirect
	github.com/sirupsen/logrus v1.10.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.72.0 // indirect
	go.opentelemetry.io/otel v1.47.0 // indirect
	go.opentelemetry.io/otel/log v1.47.0 // indirect
	go.opentelemetry.io/otel/metric v1.47.0 // indirect
	go.opentelemetry.io/otel/trace v1.47.0 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/sys v0.50.0 // indirect
)
