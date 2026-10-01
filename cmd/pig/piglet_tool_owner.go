package main

import "github.com/MichaelKinsy/PiG/coding/extension"

// pigletToolOwner names the Piglet extension entry that owns a tool, for Piglet tool scoping in every mode. Session
// tool info reports Pi's provenance object (path, source, scope, origin) as a tool's sourceInfo, which names no entry, so
// the owner reads the registration instead: the tool's own source attribution (pig additive (D23), for example
// "mcp:<server>") or, without one, the name of the extension that registered it. It returns "" for a tool no extension
// registered, which scopes as a built-in.
func pigletToolOwner(extensions func() []extension.Extension) func(extension.ToolInfo) string {
	return func(tool extension.ToolInfo) string {
		for _, ext := range extensions() {
			registered, ok := ext.RegisteredTool(tool.Name)
			if !ok {
				continue
			}
			if source, ok := registered.SourceInfo.(string); ok && source != "" {
				return source
			}
			return ext.Name
		}
		return ""
	}
}
