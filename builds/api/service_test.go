package api

import (
	"testing"

	"go.getarcane.app/builds/types"
)

type testSettingsProviderInternal struct{}

func (testSettingsProviderInternal) BuildSettings() types.BuildSettings {
	return types.BuildSettings{}
}

func TestNewServiceReturnsBuildEngine(t *testing.T) {
	service := NewService(Config{SettingsProvider: testSettingsProviderInternal{}})

	var _ types.Builder = service
}
