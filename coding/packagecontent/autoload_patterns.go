// Ports packages/coding-agent/src/core/package-manager.ts.
package packagecontent

import "strings"

// PatternState retains the first-match insertion order of upstream's path-to-enabled Map.
type PatternState struct {
	Path    string
	Enabled bool
}

// ApplyAutoloadDisabledPatterns returns only paths touched by a pattern, with later patterns replacing their state. Plus/minus match exact paths; other entries use minimatch, and fail where its Minimatch constructor throws (applyAutoloadDisabledPatterns, package-manager.ts:793-810).
func ApplyAutoloadDisabledPatterns(allPaths, patterns []string, baseDir string, kind Kind) ([]PatternState, error) {
	result := make([]PatternState, 0)
	indices := map[string]int{}
	for _, pattern := range patterns {
		target := pattern
		if len(pattern) > 0 && strings.ContainsRune("+-!", rune(pattern[0])) {
			target = pattern[1:]
		}
		enabled := !strings.HasPrefix(pattern, "-") && !strings.HasPrefix(pattern, "!")
		exact := strings.HasPrefix(pattern, "+") || strings.HasPrefix(pattern, "-")
		filters := newFilterPatterns([]string{target})
		for _, file := range allPaths {
			var matches bool
			var err error
			if exact {
				matches, err = matchesAnyExactPattern(file, []string{target}, baseDir, kind)
			} else {
				matches, err = matchesAnyPattern(file, filters, baseDir, kind)
			}
			if err != nil {
				return nil, err
			}
			if !matches {
				continue
			}
			if i, found := indices[file]; found {
				result[i].Enabled = enabled
			} else {
				indices[file] = len(result)
				result = append(result, PatternState{file, enabled})
			}
		}
	}
	return result, nil
}
