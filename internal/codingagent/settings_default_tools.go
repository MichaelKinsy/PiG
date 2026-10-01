package codingagent

import "slices"

// defaultToolNames are the tools enabled at startup when `defaultTools` does not change them.
// Mirrors upstream DEFAULT_TOOL_NAMES (settings-manager.ts:213).
var defaultToolNames = []string{"read", "bash", "edit", "write"}

// isToolModifier reports whether a defaultTools entry is a `+name` or `-name` modifier (settings-manager.ts:215-217).
func isToolModifier(entry string) bool {
	return len(entry) > 0 && (entry[0] == '+' || entry[0] == '-')
}

// mergeDefaultTools merges `defaultTools` of two settings layers. A list with plain tool names replaces the inherited one; a list of only `+name` and `-name` entries, including an empty list, is appended to an inherited list.
// Mirrors upstream mergeDefaultTools (settings-manager.ts:222-228).
func mergeDefaultTools(base, overrides []string) []string {
	if overrides == nil {
		return base
	}
	if base == nil || slices.ContainsFunc(overrides, func(entry string) bool { return !isToolModifier(entry) }) {
		return slices.Clone(overrides)
	}
	// Not slices.Concat: it returns nil for two empty lists, which reads as unset instead of `[]` (no tools).
	return append(slices.Clone(base), overrides...)
}

// resolveDefaultTools resolves a merged `defaultTools` list: plain names replace the built-in defaults, then `+name` adds and `-name` removes a tool in list order.
// Mirrors upstream resolveDefaultTools (settings-manager.ts:234-245).
func resolveDefaultTools(entries []string) []string {
	plain := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !isToolModifier(entry) {
			plain = append(plain, entry)
		}
	}
	tools := plain
	if len(plain) == 0 && len(entries) != 0 {
		tools = slices.Clone(defaultToolNames)
	}
	for _, entry := range entries {
		if !isToolModifier(entry) {
			continue
		}
		name := entry[1:]
		index := slices.Index(tools, name)
		switch {
		case entry[0] == '+' && index == -1 && name != "":
			tools = append(tools, name)
		case entry[0] == '-' && index != -1:
			tools = slices.Delete(tools, index, index+1)
		}
	}
	return tools
}
