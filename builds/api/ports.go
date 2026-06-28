package api

import (
	"log/slog"

	"go.getarcane.app/builds/types"
)

// SettingsProvider provides build settings owned by the host application.
type SettingsProvider = types.SettingsProvider

// DockerClientProvider provides Docker clients.
type DockerClientProvider = types.DockerClientProvider

// RegistryAuthProvider provides registry credentials owned by the host application.
type RegistryAuthProvider = types.RegistryAuthProvider

// Config configures Service.
type Config struct {
	SettingsProvider     SettingsProvider
	DockerClientProvider DockerClientProvider
	RegistryAuthProvider RegistryAuthProvider
	Logger               *slog.Logger
}
