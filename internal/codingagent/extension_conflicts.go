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

// ExtensionsInLoadOrder appends in-process builtins after configured
// extensions. Upstream resource-loader.ts loads path extensions first, then
// inline factories, and the runner preserves that order for dispatch and
// conflict ownership.
func ExtensionsInLoadOrder(configured, builtins []extension.Extension) []extension.Extension {
	ordered := make([]extension.Extension, 0, len(configured)+len(builtins))
	ordered = append(ordered, configured...)
	ordered = append(ordered, builtins...)
	return ordered
}

// DetectExtensionConflicts mirrors upstream resource-loader.ts
// detectExtensionConflicts. It walks extensions in load order, keeps the first
// owner of each tool and flag name, and reports every later registration by
// another extension. All extensions stay loaded; upstream adds the conflicts
// to the extension load errors.
func DetectExtensionConflicts(exts []extension.Extension) []ExtensionConflict {
	toolOwners := make(map[string]string)
	flagOwners := make(map[string]string)
	var conflicts []ExtensionConflict
	for _, ext := range exts {
		path := ext.Path
		if path == "" {
			path = ext.Name
		}
		for _, name := range slices.Sorted(maps.Keys(ext.Tools)) {
			if owner, exists := toolOwners[name]; exists && owner != path {
				conflicts = append(conflicts, ExtensionConflict{Path: path, Message: fmt.Sprintf("Tool %q conflicts with %s", name, owner)})
				continue
			}
			toolOwners[name] = path
		}
		for _, name := range slices.Sorted(maps.Keys(ext.Flags)) {
			if owner, exists := flagOwners[name]; exists && owner != path {
				conflicts = append(conflicts, ExtensionConflict{Path: path, Message: fmt.Sprintf("Flag \"--%s\" conflicts with %s", name, owner)})
				continue
			}
			flagOwners[name] = path
		}
	}
	return conflicts
}
