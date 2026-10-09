package cli

import (
	"slices"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// toolRegistryFilters returns the allowlist and denylist that bound the tool
// registry pi.getAllTools() reports, as upstream sdk.ts derives them from the
// CLI: allowedToolNames = tools ?? (noTools === "all" ? [] : undefined), and
// excludedToolNames = excludeTools. --no-builtin-tools changes only the active
// set, so it leaves the registry whole.
func toolRegistryFilters(flags Args) (allowed, excluded map[string]struct{}) {
	switch {
	case slices.ContainsFunc(flags.Tools, codingagent.IsToolModifier):
		// A list of only +name/-name entries is not an allowlist: only --no-tools turns the selection into one (sdk.ts:279-295 allowedToolNames).
		if flags.NoTools {
			allowed = map[string]struct{}{}
			for _, name := range codingagent.ApplyToolModifiers(nil, flags.Tools) {
				allowed[name] = struct{}{}
			}
		}
	case flags.Tools != nil:
		allowed = make(map[string]struct{}, len(flags.Tools))
		for _, name := range flags.Tools {
			allowed[name] = struct{}{}
		}
	case flags.NoTools:
		allowed = map[string]struct{}{}
	}
	if len(flags.ExcludeTools) > 0 {
		excluded = make(map[string]struct{}, len(flags.ExcludeTools))
		for _, name := range flags.ExcludeTools {
			excluded[name] = struct{}{}
		}
	}
	return allowed, excluded
}
