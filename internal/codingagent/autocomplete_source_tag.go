package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts getAutocompleteSourceTag and
// prefixAutocompleteDescription.

import (
	"strings"

	"github.com/MichaelKinsy/PiG/coding/source"
)

// autocompleteSourceTag is the `[tag]` an autocomplete entry shows for the resource that defines it, or "" for none.
// Built-in extension commands are untagged, like built-in commands.
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:640-664
func autocompleteSourceTag(info *PiSourceInfo) string {
	if info == nil || info.Source == "builtin" {
		return ""
	}
	scopePrefix := "t"
	switch info.Scope {
	case "user":
		scopePrefix = "u"
	case "project":
		scopePrefix = "p"
	}
	src := strings.TrimSpace(info.Source)
	if src == "auto" || src == "local" || src == "cli" {
		return scopePrefix
	}
	if strings.HasPrefix(src, "npm:") {
		return scopePrefix + ":" + src
	}
	if ref, err := source.Parse(src, source.Options{Bare: source.BareReject}); err == nil && ref.Kind == source.KindGit {
		suffix := ""
		if ref.GitRef != "" {
			suffix = "@" + ref.GitRef
		}
		return scopePrefix + ":git:" + ref.GitHost + "/" + ref.GitPath + suffix
	}
	return scopePrefix
}

// prefixAutocompleteDescription prepends the source tag to an autocomplete description.
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:666-672
func prefixAutocompleteDescription(description string, info *PiSourceInfo) string {
	tag := autocompleteSourceTag(info)
	if tag == "" {
		return description
	}
	if description == "" {
		return "[" + tag + "]"
	}
	return "[" + tag + "] " + description
}

// sourceInfoOf is a loaded resource's SourceInfo, absent when the loader recorded none.
func sourceInfoOf(info PiSourceInfo) *PiSourceInfo {
	if info == (PiSourceInfo{}) {
		return nil
	}
	return &info
}
