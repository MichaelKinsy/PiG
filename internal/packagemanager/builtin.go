package packagemanager

import (
	"strings"

	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// ResolveBuiltinExtensions resolves each built-in extension name as a `builtin:<name>` extension resource, in name order: enabled unless the user `extensions` setting excludes it, for example with `-builtin:mcp`, and overridden by a matching `+`, `-` or `!` entry of the project setting.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/package-manager.ts:970-986 (DefaultPackageManager.resolve).
func ResolveBuiltinExtensions(sm *codingagent.SettingsManager, names []string) []tui.ResourceItem {
	global, project := sm.GetGlobalSettings(), sm.GetProjectSettings()
	globalBase, projectBase := sm.AgentDir(), codingagent.ProjectConfigDir(sm.CWD())
	overrides := overridePatterns(project.Extensions)
	items := make([]tui.ResourceItem, 0, len(names))
	for _, name := range names {
		path := codingagent.BuiltinPathPrefix + name
		enabled, scope := packagecontent.EnabledByOverrides(path, global.Extensions, globalBase, packagecontent.Extensions), "user"
		for _, state := range packagecontent.ApplyAutoloadDisabledPatterns([]string{path}, overrides, projectBase, packagecontent.Extensions) {
			enabled, scope = state.Enabled, "project"
		}
		items = append(items, tui.ResourceItem{Path: path, Enabled: enabled, ResourceType: tui.ResourceExtensions, Scope: scope, Origin: "top-level", Source: "builtin"})
	}
	return items
}

// ResolveBuiltinExtensionSources resolves the `builtin:<name>` entries of sources, the `-e` paths, as enabled built-in extension resources of scope, and returns the other sources unchanged. The resource loader reports an unknown name.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/package-manager.ts:996-1006 (resolveExtensionSources).
func ResolveBuiltinExtensionSources(sources []string, scope string) (builtin []tui.ResourceItem, others []string) {
	for _, source := range sources {
		if !strings.HasPrefix(source, codingagent.BuiltinPathPrefix) {
			others = append(others, source)
			continue
		}
		builtin = append(builtin, tui.ResourceItem{Path: source, Enabled: true, ResourceType: tui.ResourceExtensions, Scope: scope, Origin: "top-level", Source: "builtin"})
	}
	return builtin, others
}

// overridePatterns keeps the `!`, `+` and `-` entries of a resource setting (package-manager.ts:714-716, getOverridePatterns).
func overridePatterns(entries []string) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry, "!") || strings.HasPrefix(entry, "+") || strings.HasPrefix(entry, "-") {
			out = append(out, entry)
		}
	}
	return out
}
