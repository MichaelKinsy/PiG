// Ports packages/coding-agent/src/core/package-manager.ts.
package packagecontent

import "strings"

// PatternState retains the first-match insertion order of upstream's path-to-enabled Map.
type PatternState struct {
	Path    string
	Enabled bool
}

// ApplyAutoloadDisabledPatterns returns only paths touched by a pattern, with later patterns replacing their state. Plus/minus match exact paths; other entries use glob matching.
func ApplyAutoloadDisabledPatterns(allPaths, patterns []string, baseDir string, kind Kind) []PatternState {
	result := make([]PatternState, 0)
	indices := map[string]int{}
	for _, pattern := range patterns {
		target := pattern
		if len(pattern) > 0 && strings.ContainsRune("+-!", rune(pattern[0])) {
			target = pattern[1:]
		}
		enabled := !strings.HasPrefix(pattern, "-") && !strings.HasPrefix(pattern, "!")
		exact := strings.HasPrefix(pattern, "+") || strings.HasPrefix(pattern, "-")
		for _, file := range allPaths {
			matches := false
			if exact {
				matches = matchesAnyExactPattern(file, []string{target}, baseDir, kind)
			} else {
				matches = matchesAnyPattern(file, []string{target}, baseDir, kind)
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
	return result
}
