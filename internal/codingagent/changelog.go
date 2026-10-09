package codingagent

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// `/changelog` slash command and parser.
//
// Mirrors upstream `utils/changelog.ts` (v0.69.0) line-for-line:
// scan for `## ` headers, parse the version triple from the first one
// matching `## [x.y.z]` or `## x.y.z`, accumulate body lines until the
// next `## ` line or EOF. Non-version headers (e.g. `## [Unreleased]`)
// reset the accumulator without recording an entry.
//
// `## [Unreleased]` is intentionally skipped: its content is by
// definition not in any released version yet, and surfacing it in the
// /changelog viewer would mislead the user about what version they're
// running.

// ChangelogEntry is one parsed `## [x.y.z]` block.
type ChangelogEntry struct {
	Major   int
	Minor   int
	Patch   int
	Content string // includes the `## [x.y.z]` header line itself, trimmed
}

// versionHeaderRE is upstream's `/##\s+\[?(\d+)\.(\d+)\.(\d+)\]?/` (utils/changelog.ts): not anchored to the line start, and `\s` is JavaScript's
// whitespace set.
var versionHeaderRE = lazyregexp.New(`##[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]+\[?(\d+)\.(\d+)\.(\d+)\]?`)

// ParseChangelog walks the markdown content of a CHANGELOG.md and
// returns one ChangelogEntry per `## [x.y.z]`-style header. Order is
// the order found in the file (typical convention: newest first).
// Malformed entries are silently skipped: matches upstream behavior
// (errors only logged via console.error).
func ParseChangelog(content string) []ChangelogEntry {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")

	var entries []ChangelogEntry
	var currentLines []string
	var currentVersion *ChangelogEntry

	flush := func() {
		if currentVersion != nil && len(currentLines) > 0 {
			entry := *currentVersion
			entry.Content = jsstring.Trim(strings.Join(currentLines, "\n"))
			entries = append(entries, entry)
		}
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			flush()
			match := versionHeaderRE.FindStringSubmatch(line)
			if match != nil {
				major, _ := strconv.Atoi(match[1])
				minor, _ := strconv.Atoi(match[2])
				patch, _ := strconv.Atoi(match[3])
				currentVersion = &ChangelogEntry{Major: major, Minor: minor, Patch: patch}
				currentLines = []string{line}
			} else {
				// Non-version header (e.g. `## [Unreleased]`): reset.
				currentVersion = nil
				currentLines = nil
			}
			continue
		}
		if currentVersion != nil {
			currentLines = append(currentLines, line)
		}
	}
	flush()

	return entries
}

// CompareChangelogEntries returns -1, 0, or 1 mirroring upstream
// `compareVersions`. Callers use it to filter entries newer than a version.
func CompareChangelogEntries(a, b ChangelogEntry) int {
	if a.Major != b.Major {
		if a.Major < b.Major {
			return -1
		}
		return 1
	}
	if a.Minor != b.Minor {
		if a.Minor < b.Minor {
			return -1
		}
		return 1
	}
	if a.Patch < b.Patch {
		return -1
	}
	if a.Patch > b.Patch {
		return 1
	}
	return 0
}

// FormatChangelogForChat wraps the released entries for a plain Markdown output sink. The interactive command uses separate border, title and padded Markdown components.
func FormatChangelogForChat(entries []ChangelogEntry) string {
	if len(entries) == 0 {
		return "No changelog entries found."
	}
	return "---\n\n**What's New**\n\n" + changelogMarkdown(entries) + "\n\n---"
}

// changelogMarkdown renders entries in reverse file order, with the newest release at the bottom.
func changelogMarkdown(entries []ChangelogEntry) string {
	if len(entries) == 0 {
		return "No changelog entries found."
	}
	parts := make([]string, 0, len(entries))
	for _, entry := range slices.Backward(entries) {
		parts = append(parts, NormalizeChangelogLinks(entry.Content, entry.Version()))
	}
	return strings.Join(parts, "\n\n")
}

// GetNewEntries returns the entries whose version is newer than sinceVersion. The last-seen version is read as upstream's
// `lastVersion.split(".").map(Number)` does: each of the first three dot-separated parts is a JavaScript Number, and a part
// that is missing or not a number counts as 0 (utils/changelog.ts getNewEntries).
func GetNewEntries(entries []ChangelogEntry, sinceVersion string) []ChangelogEntry {
	parts := strings.Split(sinceVersion, ".")
	part := func(i int) float64 {
		if i >= len(parts) {
			return 0
		}
		if number := jsStringToNumber(parts[i]); number != 0 && !math.IsNaN(number) {
			return number
		}
		return 0
	}
	last := [3]float64{part(0), part(1), part(2)}
	var newer []ChangelogEntry
	for _, e := range entries {
		for i, value := range [3]float64{float64(e.Major), float64(e.Minor), float64(e.Patch)} {
			if value == last[i] {
				continue
			}
			if value > last[i] {
				newer = append(newer, e)
			}
			break
		}
	}
	return newer
}

var jsDecimalLiteral = lazyregexp.New(`^[+-]?(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?$`)

// jsStringToNumber is Number(text) for a string: surrounding JavaScript whitespace is ignored, the empty string is 0, Infinity, decimal and
// 0x/0o/0b literals convert and anything else is NaN.
func jsStringToNumber(text string) float64 {
	text = jsstring.Trim(text)
	switch {
	case text == "":
		return 0
	case text == "Infinity" || text == "+Infinity":
		return math.Inf(1)
	case text == "-Infinity":
		return math.Inf(-1)
	case jsDecimalLiteral.MatchString(text):
		number, _ := strconv.ParseFloat(text, 64)
		return number
	}
	if len(text) > 2 && text[0] == '0' {
		base := map[byte]int{'x': 16, 'X': 16, 'o': 8, 'O': 8, 'b': 2, 'B': 2}[text[1]]
		if base != 0 {
			if number, err := strconv.ParseUint(text[2:], base, 64); err == nil && !strings.HasPrefix(text[2:], "+") {
				return float64(number)
			}
		}
	}
	return math.NaN()
}
