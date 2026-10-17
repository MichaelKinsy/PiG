package codingagent

import (
	"fmt"
	"maps"
	"slices"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// ExtensionConflict is a tool or flag that an extension registers after
// another extension, at a different path, already registered it.
type ExtensionConflict struct {
	Path    string
	Message string
}

// ToolConflict is a tool that the extension at Path registered after the extension at Owner, at a different path, already registered it.
// pig additive (D109): PiG's stale-copy diagnostic needs the owner as data; Pi's conflict carries only the message.
type ToolConflict struct {
	Path  string
	Owner string
	Tool  string
}

// ExtensionsInLoadOrder appends in-process builtins after configured
// extensions. Upstream resource-loader.ts loads path extensions first, then
// inline factories, and the runner preserves that order for dispatch and
// conflict ownership.
func ExtensionsInLoadOrder(configured, builtins []extension.Extension) []extension.Extension {
	return ExtensionsInLoadOrderWithExplicit(configured, builtins, nil)
}

// ExtensionsInLoadOrderWithExplicit is ExtensionsInLoadOrder with the built-ins named by `-e builtin:<name>` (explicit holds their paths) placed after
// the extensions of the other -e sources and ahead of the settings extensions. resource-loader.ts:566-575 lists the -e paths before the settings
// paths and package-manager.ts resolveExtensionSources sorts them by resourcePrecedenceRank, which puts a built-in after every file or package
// extension (:192-198); loadFinalExtensionSet orders the extensions by that list (:765-775). The other built-ins and the inline factories follow
// the configured extensions. The -e extensions are the configured ones whose source is the CLI.
func ExtensionsInLoadOrderWithExplicit(configured, builtins []extension.Extension, explicit []string) []extension.Extension {
	ordered := make([]extension.Extension, 0, len(configured)+len(builtins))
	isCLI := func(ext extension.Extension) bool { return ext.SourceInfo.Source == CLISourceName }
	for _, ext := range configured {
		if isCLI(ext) && len(explicit) > 0 {
			ordered = append(ordered, ext)
		}
	}
	for _, builtin := range builtins {
		if slices.Contains(explicit, builtin.Path) {
			ordered = append(ordered, builtin)
		}
	}
	for _, ext := range configured {
		if !isCLI(ext) || len(explicit) == 0 {
			ordered = append(ordered, ext)
		}
	}
	for _, builtin := range builtins {
		if !slices.Contains(explicit, builtin.Path) {
			ordered = append(ordered, builtin)
		}
	}
	return ordered
}

// DetectExtensionConflicts mirrors upstream resource-loader.ts
// detectExtensionConflicts. It walks extensions in load order and each
// extension's tools in registration order, keeps the first owner of each tool
// and flag name, and reports every later registration by another extension.
// All extensions stay loaded; upstream adds the conflicts to the extension
// load errors. Flags have no recorded registration order and are walked by
// name.
func DetectExtensionConflicts(exts []extension.Extension) []ExtensionConflict {
	conflicts, _ := detectExtensionConflicts(exts)
	return conflicts
}

// DetectToolConflicts reports the tool conflicts DetectExtensionConflicts reports, with the path of the extension that owns each tool.
func DetectToolConflicts(exts []extension.Extension) []ToolConflict {
	_, tools := detectExtensionConflicts(exts)
	return tools
}

func detectExtensionConflicts(exts []extension.Extension) ([]ExtensionConflict, []ToolConflict) {
	var toolConflicts []ToolConflict
	toolOwners := make(map[string]string)
	flagOwners := make(map[string]string)
	var conflicts []ExtensionConflict
	for _, ext := range exts {
		path := ext.Path
		if path == "" {
			path = ext.Name
		}
		for _, name := range toolNamesInRegistrationOrder(ext) {
			if owner, exists := toolOwners[name]; exists && owner != path {
				conflicts = append(conflicts, ExtensionConflict{Path: path, Message: fmt.Sprintf("Tool \"%s\" conflicts with %s", name, owner)})
				toolConflicts = append(toolConflicts, ToolConflict{Path: path, Owner: owner, Tool: name})
				continue
			}
			toolOwners[name] = path
		}
		var flagNames []string
		for _, flag := range ext.RegisteredFlags() {
			flagNames = append(flagNames, flag.Name)
		}
		slices.Sort(flagNames)
		for _, name := range flagNames {
			if owner, exists := flagOwners[name]; exists && owner != path {
				conflicts = append(conflicts, ExtensionConflict{Path: path, Message: fmt.Sprintf("Flag \"--%s\" conflicts with %s", name, owner)})
				continue
			}
			flagOwners[name] = path
		}
	}
	return conflicts, toolConflicts
}

// toolNamesInRegistrationOrder returns Tools keys in ToolOrder order, the Go form of upstream's insertion-ordered ext.tools Map. Keys absent from ToolOrder follow by name.
func toolNamesInRegistrationOrder(ext extension.Extension) []string {
	names := make([]string, 0, len(ext.Tools))
	seen := make(map[string]struct{}, len(ext.Tools))
	for _, name := range ext.ToolOrder {
		if _, ok := ext.Tools[name]; !ok {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for _, name := range slices.Sorted(maps.Keys(ext.Tools)) {
		if _, ok := seen[name]; !ok {
			names = append(names, name)
		}
	}
	return names
}
