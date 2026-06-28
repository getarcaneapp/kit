package api

import (
	"context"
	"io"
	"testing"

	"go.getarcane.app/builds/types"
)

type testSettingsProviderInternal struct{}

func (testSettingsProviderInternal) BuildSettings() types.BuildSettings {
	return types.BuildSettings{}
}

func TestNewServiceReturnsBuildEngine(t *testing.T) {
	service := NewService(Config{SettingsProvider: testSettingsProviderInternal{}})

	var engine interface {
		BuildImage(context.Context, types.BuildRequest, io.Writer, string) (*types.BuildResult, error)
	} = service

	if engine == nil {
		t.Fatalf("expected service to satisfy build engine interface")
	}
}
