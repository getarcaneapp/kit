package convert

import (
	"context"
	"fmt"

	"github.com/compose-spec/compose-go/v2/loader"
	compose "github.com/compose-spec/compose-go/v2/types"
	"go.getarcane.app/docker/convert/types"
)

func Convert(input string, opts types.Options) (*types.Result, error) {
	commands, err := Parse(input, types.ParseOptions{})
	if err != nil {
		return nil, err
	}

	doc, err := Build(commands, opts)
	if err != nil {
		return nil, err
	}

	yamlData, err := Marshal(doc, types.MarshalOptions{RenderWarnings: opts.RenderWarnings})
	if err != nil {
		return nil, err
	}

	project, err := loadComposeProjectInternal(yamlData)
	if err != nil {
		return nil, types.NewConversionError("validate generated compose: %v", err)
	}

	return &types.Result{
		YAML:     yamlData,
		Project:  project,
		Services: serviceResultsInternal(doc),
		EnvFile:  envFileInternal(commands),
		Warnings: doc.Warnings,
	}, nil
}

func loadComposeProjectInternal(yamlData []byte) (*compose.Project, error) {
	details := compose.ConfigDetails{
		WorkingDir: ".",
		ConfigFiles: []compose.ConfigFile{
			{Filename: "compose.yaml", Content: yamlData},
		},
		Environment: map[string]string{},
	}

	project, err := loader.LoadWithContext(context.Background(), details, func(opts *loader.Options) {
		opts.SetProjectName("converted", true)
		opts.SkipResolveEnvironment = true
		opts.SkipConsistencyCheck = true
	})
	if err != nil {
		return nil, fmt.Errorf("load compose project: %w", err)
	}

	return project, nil
}
