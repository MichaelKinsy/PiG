package cli

import (
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/packagemanager"
)

// cliPackageResources lists the enabled resources the -e sources contribute besides extensions: Pi's cliEnabledSkills, cliEnabledPrompts and cliEnabledThemes, from resolveExtensionSources(additionalExtensionPaths, {temporary: true}).
// A source that does not resolve contributes nothing here; collectExtensionConfigs reports it.
// Ports packages/coding-agent/src/core/resource-loader.ts (reload) and package-manager.ts (resolveExtensionSources).
type cliPackageResources struct {
	Skills, Prompts, Themes []string
	// Infos is Pi's metadataByPath for these paths: a prompt or theme keeps its Package's PathMetadata, and a skill is {source: "cli", scope: "temporary", origin: "top-level"} (the "Add CLI paths metadata" loop).
	Infos map[string]codingagent.ResourceSourceInfo
}

// remoteCLIResolutions holds the resolution of each npm or git -e source for one load, its error included, because resolving one installs or refreshes a temporary checkout, and Pi resolves the -e sources once per load (resource-loader.ts:525-528) while the extension, prompt, theme and skill collectors each ask for it.
var remoteCLIResolutions sync.Map

type remoteCLIKey struct{ cwd, agentDir, source string }

type remoteCLIResolution struct {
	resolved packagemanager.ResolvedPaths
	err      error
}

// resetCLIPackageResolutions starts a new load: the next resolution of a remote -e source refreshes it, as each Pi reload() does.
func resetCLIPackageResolutions() {
	remoteCLIResolutions.Clear()
}

// collectCLIPackageResources stops at the first -e source that fails to resolve: Pi's resolvePackageSources resolves the sources in turn and rejects there, so no later source installs (package-manager.ts:1293-1342). collectCLIExtensionConfigs reports the failure.
func collectCLIPackageResources(cwd, agentDir string, sm *codingagent.SettingsManager, flags Args) cliPackageResources {
	out := cliPackageResources{Infos: map[string]codingagent.ResourceSourceInfo{}}
	for _, source := range flags.Extensions {
		if strings.HasPrefix(source, codingagent.BuiltinPathPrefix) {
			continue
		}
		resolved, err := resolveCLIPackageSource(cwd, agentDir, sm, source)
		if err != nil {
			if packagemanager.DetectSourceKind(source) != "local" {
				break
			}
			continue
		}
		out.Skills = append(out.Skills, out.collect(resolved.Skills, "skills")...)
		out.Prompts = append(out.Prompts, out.collect(resolved.Prompts, "prompts")...)
		out.Themes = append(out.Themes, out.collect(resolved.Themes, "themes")...)
	}
	return out
}

// resolveCLIPackageSource is resolveExtensionSources for one -e source. An npm or git source resolves once per load.
func resolveCLIPackageSource(cwd, agentDir string, sm *codingagent.SettingsManager, source string) (packagemanager.ResolvedPaths, error) {
	remote := packagemanager.DetectSourceKind(source) != "local"
	key := remoteCLIKey{cwd, agentDir, source}
	if remote {
		if cached, ok := remoteCLIResolutions.Load(key); ok {
			resolution, _ := cached.(remoteCLIResolution)
			return resolution.resolved, resolution.err
		}
	}
	manager := packagemanager.NewPackageManager(packagemanager.PackageManagerOptions{CWD: cwd, SettingsManager: sm})
	manager.AgentDir = agentDir
	resolved, err := manager.ResolveExtensionSources([]string{source}, false, true)
	if remote {
		remoteCLIResolutions.Store(key, remoteCLIResolution{resolved: resolved, err: err})
	}
	return resolved, err
}

func (r cliPackageResources) collect(resources []codingagent.ResolvedResource, kind string) []string {
	paths := make([]string, 0, len(resources))
	for _, resource := range resources {
		if !resource.Enabled {
			continue
		}
		paths = append(paths, resource.Path)
		if _, ok := r.Infos[resource.Path]; ok {
			continue
		}
		info := codingagent.ResourceSourceInfo{Path: resource.Path, ResourceType: kind, Enabled: true, Source: resource.Metadata.Source, Scope: resource.Metadata.Scope, Origin: resource.Metadata.Origin, BaseDir: resource.Metadata.BaseDir}
		if kind == "skills" {
			info.Source, info.Scope, info.Origin, info.BaseDir = "cli", "temporary", "top-level", ""
		}
		r.Infos[resource.Path] = info
	}
	return paths
}
