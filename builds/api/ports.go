package api

import (
	"log/slog"

	"kit.getarcane.app/builds/types"
)

type (
	// SettingsProvider provides build settings owned by the host application.
	SettingsProvider = types.SettingsProvider

	// DockerClientProvider provides Docker clients.
	DockerClientProvider = types.DockerClientProvider

	// RegistryAuthProvider provides registry credentials owned by the host application.
	RegistryAuthProvider = types.RegistryAuthProvider

	// Config configures Service.
	Config struct {
		SettingsProvider     SettingsProvider
		DockerClientProvider DockerClientProvider
		RegistryAuthProvider RegistryAuthProvider
		Logger               *slog.Logger
	}
)
