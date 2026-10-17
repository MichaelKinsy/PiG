package packagemanager

import (
	"strings"

	"github.com/MichaelKinsy/PiG/coding/packagecontent"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// ResolveBuiltinExtensions resolves each built-in extension name as a `builtin:<name>` extension resource, in name order: a matching `+`, `-` or `!` entry of the project setting decides it, and otherwise it is enabled unless the user `extensions` setting excludes it, for example with `-builtin:mcp`. The user setting is read only for a name the project setting leaves alone, so only then can its pattern fail; a pattern minimatch cannot compile returns the items before it with the error Pi's resolve() rejects with.
// Ports .upstream/v0.99.1/packages/coding-agent/src/core/package-manager.ts:970-986 (DefaultPackageManager.resolve).
func ResolveBuiltinExtensions(sm *codingagent.SettingsManager, names []string) ([]tui.ResourceItem, error) {
	global, project := sm.GetGlobalSettings(), sm.GetProjectSettings()
	globalBase, projectBase := sm.AgentDir(), codingagent.ProjectConfigDir(sm.CWD())
	overrides := overridePatterns(project.Extensions)
	items := make([]tui.ResourceItem, 0, len(names))
	for _, name := range names {
		path := codingagent.BuiltinPathPrefix + name
		states, err := packagecontent.ApplyAutoloadDisabledPatterns([]string{path}, overrides, projectBase, packagecontent.Extensions)
		if err != nil {
			return items, err
		}
		enabled, scope := false, "project"
		if len(states) > 0 {
			enabled = states[len(states)-1].Enabled
		} else {
			scope = "user"
			if enabled, err = packagecontent.EnabledByOverrides(path, global.Extensions, globalBase, packagecontent.Extensions); err != nil {
				return items, err
			}
		}
		items = append(items, tui.ResourceItem{Path: path, Enabled: enabled, ResourceType: tui.ResourceExtensions, Scope: scope, Origin: "top-level", Source: "builtin"})
	}
	return items, nil
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
