//go:build !windows

package pixel

import (
	"os"
	"unicode"
)

func colorTermIsTrueColor() bool {
	value := os.Getenv("COLORTERM")
	return foldEqual(value, "truecolor") || foldEqual(value, "24bit")
}

func inWindowsTerminal() bool { return os.Getenv("WT_SESSION") != "" }

// foldEqual reports whether strings.ToLower(s) == lower without allocating.
func foldEqual(s, lower string) bool {
	for _, r := range s {
		if lower == "" || unicode.ToLower(r) != rune(lower[0]) {
			return false
		}
		lower = lower[1:]
	}
	return lower == ""
}
