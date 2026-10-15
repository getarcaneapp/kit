package convert

import (
	"context"
	"strings"

	"github.com/compose-spec/compose-go/v2/loader"
	compose "github.com/compose-spec/compose-go/v2/types"
	kit "go.getarcane.app/kit/pkg"

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

	project, err := loader.LoadWithContext(context.Background(), compose.ConfigDetails{
		WorkingDir: ".",
		ConfigFiles: []compose.ConfigFile{
			{Filename: "compose.yaml", Content: yamlData},
		},
		Environment: map[string]string{},
	}, func(loaderOpts *loader.Options) {
		loaderOpts.SetProjectName("converted", true)
		loaderOpts.SkipResolveEnvironment = true
		loaderOpts.SkipConsistencyCheck = true
	})
	if err != nil {
		return nil, types.NewConversionError("validate generated compose: load compose project: %v", err)
	}

	services := make([]types.ServiceResult, 0, len(doc.ServiceOrder))
	for _, name := range doc.ServiceOrder {
		image, _ := doc.Services[name]["image"].(string)
		services = append(services, types.ServiceResult{Name: name, Image: image})
	}

	var envLines []string
	for _, cmd := range commands {
		for _, flag := range cmd.Flags {
			if flag.Name == "environment" && strings.Contains(flag.Value, "=") {
				envLines = append(envLines, flag.Value)
			}
		}
	}

	return &types.Result{
		YAML:     yamlData,
		Project:  project,
		Services: services,
		EnvFile:  kit.Ternary(len(envLines) == 0, nil, []byte(strings.Join(envLines, "\n")+"\n")),
		Warnings: doc.Warnings,
	}, nil
}
