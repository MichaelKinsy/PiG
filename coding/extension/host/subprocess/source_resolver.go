package subprocess

import (
	"fmt"
	"path/filepath"
	"strings"

	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
)

// ResolveExtConfig resolves one authorized path into one conventional factory
// or exact isolated standalone runtime configuration.
func ResolveExtConfig(path string) (ExtConfig, *extsource.Definition, error) {
	return ResolveExtConfigWithIdentity(path, "")
}

// ResolveExtConfigWithIdentity binds runtime registration to an identity owned
// by the selecting Package or Piglet. Empty expectedName derives from the exact
// selected path.
func ResolveExtConfigWithIdentity(path, expectedName string) (ExtConfig, *extsource.Definition, error) {
	return ResolveExtConfigWithResolver(path, expectedName, extsource.Resolve)
}

// ResolveExtConfigWithResolver maps the Definition returned by resolve into a
// runtime configuration. Startup passes its bounded resolver snapshot so
// Package validation, trust loading, and final loading consume one scan.
func ResolveExtConfigWithResolver(path, expectedName string, resolve extsource.ResolveFunc) (ExtConfig, *extsource.Definition, error) {
	if resolve == nil {
		resolve = extsource.Resolve
	}
	definition, err := resolve(path)
	if err != nil {
		return ExtConfig{}, nil, err
	}
	name := strings.TrimSpace(expectedName)
	if name == "" {
		name = extensionDefinitionName(definition, path)
	}
	config := ExtConfig{
		Name:               name,
		selectedPath:       path,
		Enabled:            true,
		RuntimeKind:        "subprocess",
		RuntimeLanguage:    definition.Language,
		SDKName:            derivedSDKName(definition),
		Isolation:          "isolated",
		EntrypointKind:     string(definition.Form),
		ModulePath:         definition.ModulePath,
		Package:            definition.Package,
		Factory:            definition.Factory,
		GoWorkspaceModules: append([]string(nil), definition.GoWorkspaceModules...),
	}
	// A conventional Node factory (a default export) packs into the one Node
	// cell for the session, the way upstream pi hosts every extension in one
	// process (N8). definition.Packable stays false for node because that
	// field also drives Piglet Binary fusibility, which Node components never
	// support (they stay external, D19); packing and fusibility are separate
	// questions here.
	if definition.Form == extsource.Factory && (definition.Packable || definition.Language == "node") {
		config.Isolation = "shared-ok"
	}
	if definition.Form == extsource.Factory {
		config.Source = definition.Root
		if definition.Language == "node" {
			config.Source = path
		}
		return config, &definition, nil
	}
	switch definition.Language {
	case "go", "rust":
		config.Source = definition.Root
	default:
		config.Path = definition.Entrypoint
	}
	if config.Source == "" && config.Path == "" {
		return ExtConfig{}, nil, fmt.Errorf("extension %q standalone has no exact executable or conventional executable source", name)
	}
	return config, &definition, nil
}

func extensionDefinitionName(definition extsource.Definition, input string) string {
	if definition.Form == extsource.Factory && definition.Language == "go" && definition.Package != "" {
		inputRoot, _ := filepath.Abs(input)
		if filepath.Clean(inputRoot) != filepath.Clean(definition.Root) {
			return filepath.Base(definition.Package)
		}
	}
	name := filepath.Base(filepath.Clean(input))
	if extension := filepath.Ext(name); extension != "" {
		name = strings.TrimSuffix(name, extension)
	}
	return name
}

// derivedSDKName names the SDK a definition runs against. A Go factory keeps
// the SDK module path it imports, so the cell planner never packs a legacy-SDK
// factory with current-SDK ones and the Piglet planner never fuses it.
func derivedSDKName(definition extsource.Definition) string {
	switch definition.Language {
	case "go":
		if definition.SDKModulePath != "" {
			return definition.SDKModulePath
		}
		return extsource.GoSDKModulePath
	case "rust":
		return "pig-sdk-rs"
	case "python":
		return "pig-sdk-py"
	case "node":
		return "pi-node"
	default:
		return ""
	}
}
