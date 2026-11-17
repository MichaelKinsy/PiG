package tui

import "strings"

// trimmedRows is lines with the trailing spaces of every row removed: the padding a component adds is not what a test of its rows compares.
func trimmedRows(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = strings.TrimRight(line, " ")
	}
	return out
}
