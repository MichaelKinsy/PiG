package main

// toolRegistryFilters returns the allowlist and denylist that bound the tool
// registry pi.getAllTools() reports, as upstream sdk.ts derives them from the
// CLI: allowedToolNames = tools ?? (noTools === "all" ? [] : undefined), and
// excludedToolNames = excludeTools. --no-builtin-tools changes only the active
// set, so it leaves the registry whole.
func toolRegistryFilters(flags CLIFlags) (allowed, excluded map[string]struct{}) {
	switch {
	case len(flags.Tools) > 0:
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
