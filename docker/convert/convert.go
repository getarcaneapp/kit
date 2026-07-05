package convert

import (
	"context"
	"fmt"

	"github.com/compose-spec/compose-go/v2/loader"
	composegotypes "github.com/compose-spec/compose-go/v2/types"
	converttypes "go.getarcane.app/docker/convert/types"
)

func Convert(input string, opts converttypes.Options) (*converttypes.Result, error) {
	commands, err := ParseCommands(input, converttypes.ParseOptions{})
	if err != nil {
		return nil, err
	}

	doc, err := BuildDocument(commands, opts)
	if err != nil {
		return nil, err
	}

	yamlData, err := MarshalYAML(doc, converttypes.MarshalOptions{RenderWarnings: opts.RenderWarnings})
	if err != nil {
		return nil, err
	}

	project, err := loadComposeProjectInternal(yamlData)
	if err != nil {
		return nil, converttypes.NewConversionError("validate generated compose: %v", err)
	}

	return &converttypes.Result{
		YAML:     yamlData,
		Project:  project,
		Services: serviceResultsInternal(doc),
		EnvFile:  envFileInternal(commands),
		Warnings: doc.Warnings,
	}, nil
}

func ParseCommands(input string, opts converttypes.ParseOptions) ([]converttypes.RunCommand, error) {
	return parseCommandsInternal(input, opts)
}

func BuildDocument(commands []converttypes.RunCommand, opts converttypes.Options) (*converttypes.Document, error) {
	return buildDocumentInternal(commands, opts)
}

func MarshalYAML(doc *converttypes.Document, opts converttypes.MarshalOptions) ([]byte, error) {
	return marshalYAMLInternal(doc, opts)
}

func loadComposeProjectInternal(yamlData []byte) (*composegotypes.Project, error) {
	details := composegotypes.ConfigDetails{
		WorkingDir: ".",
		ConfigFiles: []composegotypes.ConfigFile{
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
