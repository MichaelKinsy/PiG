package packagemanager

import (
	"errors"
	"fmt"

	extsource "github.com/MichaelKinsy/PiG/coding/extension/source"
	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// ApplyPackageAutoloadStates keeps only touched resources in first-match order, including disabled resources that shadow inherited entries.
func ApplyPackageAutoloadStates(pkg ConfiguredPackage, items []tui.ResourceItem) []tui.ResourceItem {
	if !IsProjectPackageDelta(pkg.Source) {
		return items
	}
	out := make([]tui.ResourceItem, 0, len(items))
	for _, kind := range []packagecontent.Kind{packagecontent.Extensions, packagecontent.Skills, packagecontent.Prompts, packagecontent.Themes} {
		paths := make([]string, 0)
		byPath := make(map[string]tui.ResourceItem)
		for _, item := range items {
			if packagecontent.Kind(item.ResourceType) != kind {
				continue
			}
			path := item.Path
			if kind == packagecontent.Skills {
				path = packagecontent.SkillFile(path)
			}
			paths = append(paths, path)
			byPath[path] = item
		}
		for _, state := range packagecontent.ApplyAutoloadDisabledPatterns(paths, ConfiguredPackageFilters(pkg.Source)[kind], pkg.InstalledPath, kind) {
			item := byPath[state.Path]
			item.Enabled = state.Enabled
			out = append(out, item)
		}
	}
	return out
}

// CollectResolvedPackageResourceItems preserves addResource's first entry, including its enabled state and metadata. Project deltas are resolved before inherited user packages, not merged into user filters.
func CollectResolvedPackageResourceItems(cwd, agentDir string, sm *codingagent.SettingsManager, ambientScopes *[]string, inspect bool, resolvers ...extsource.ResolveFunc) ([]tui.ResourceItem, error) {
	items := make([]tui.ResourceItem, 0)
	seen := make(map[string]struct{})
	var errs []error
	for _, pkg := range ResolvedConfiguredPackageSources(cwd, agentDir, sm, true) {
		if !PackageScopeEnabled(ambientScopes, pkg.Scope) {
			continue
		}
		root := pkg.InstalledPath
		if root == "" {
			errs = append(errs, fmt.Errorf("%s Package %q is not materialized; run pig update %s", pkg.Scope, pkg.Source.Source, pkg.Source.Source))
			continue
		}
		if ConfiguredPackageNeedsInstall(pkg) {
			continue
		}
		resources, err := CollectPackageResourceItems(root, pkg, inspect, resolvers...)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, item := range resources {
			key := string(item.ResourceType) + ":" + item.Path
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			items = append(items, item)
		}
	}
	return items, errors.Join(errs...)
}

// CollectEnabledPackagePaths lists the enabled paths of one resource kind that the configured Packages expose, in resolution order.
func CollectEnabledPackagePaths(cwd, agentDir string, sm *codingagent.SettingsManager, kind packagecontent.Kind, ambientScopes *[]string, resolvers ...extsource.ResolveFunc) []string {
	items, _ := CollectResolvedPackageResourceItems(cwd, agentDir, sm, ambientScopes, false, resolvers...)
	paths := make([]string, 0)
	for _, item := range items {
		if packagecontent.Kind(item.ResourceType) == kind && item.Enabled {
			paths = append(paths, item.Path)
		}
	}
	return paths
}
