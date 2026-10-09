package codingagent

import (
	"slices"
	"strings"
)

// defaultToolNames are the tools enabled at startup when `defaultTools` does not change them.
// Mirrors upstream DEFAULT_TOOL_NAMES (settings-manager.ts:213).
var defaultToolNames = []string{"read", "bash", "edit", "write"}

// IsToolModifier reports whether a defaultTools entry is a `+name` or `-name` modifier (settings-manager.ts:215-217).
func IsToolModifier(entry string) bool {
	return len(entry) > 0 && (entry[0] == '+' || entry[0] == '-')
}

// mergeDefaultTools merges `defaultTools` of two settings layers. A list with plain tool names replaces the inherited one; a list of only `+name` and `-name` entries, including an empty list, is appended to an inherited list.
// Mirrors upstream mergeDefaultTools (settings-manager.ts:222-228).
func mergeDefaultTools(base, overrides []string) []string {
	if overrides == nil {
		return base
	}
	if base == nil || slices.ContainsFunc(overrides, func(entry string) bool { return !IsToolModifier(entry) }) {
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
		if !IsToolModifier(entry) {
			plain = append(plain, entry)
		}
	}
	base := plain
	if len(plain) == 0 && len(entries) != 0 {
		base = defaultToolNames
	}
	return ApplyToolModifiers(base, entries)
}

// GetToolListError validates a tool list from `--tools`: it is either an allowlist of plain names and patterns or a list of only `+name` and `-name` entries with exact names. It returns the problem, or "" when the list is valid.
// Mirrors upstream getToolListError (settings-manager.ts:218-228).
func GetToolListError(entries []string) string {
	modifiers := 0
	for _, entry := range entries {
		if IsToolModifier(entry) {
			modifiers++
		}
	}
	if modifiers == 0 {
		return ""
	}
	if modifiers < len(entries) {
		return "tool names cannot be mixed with +name or -name entries"
	}
	for _, entry := range entries {
		if strings.Contains(entry, "*") {
			return "+name and -name entries take exact tool names, not patterns: " + entry
		}
	}
	return ""
}

// ApplyToolModifiers applies the `+name` and `-name` entries of entries to base in order: `+name` adds a tool and `-name` removes one. Other entries are ignored.
// Mirrors upstream applyToolModifiers (settings-manager.ts:236-248).
func ApplyToolModifiers(base, entries []string) []string {
	tools := slices.Clone(base)
	if tools == nil {
		tools = []string{}
	}
	for _, entry := range entries {
		if !IsToolModifier(entry) {
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
