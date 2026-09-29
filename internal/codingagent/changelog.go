package codingagent

import (
	"slices"
	"strconv"
	"strings"

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

// versionHeaderRE mirrors upstream's regex from utils/changelog.ts:35.
//
//	/##\s+\[?(\d+)\.(\d+)\.(\d+)\]?/
var versionHeaderRE = lazyregexp.New(`^##\s+\[?(\d+)\.(\d+)\.(\d+)\]?`)

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
			entry.Content = strings.TrimSpace(strings.Join(currentLines, "\n"))
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
		parts = append(parts, entry.Content)
	}
	return strings.Join(parts, "\n\n")
}

// compareVersions compares two ChangelogEntry values by version number.
// Returns positive if v1 > v2, zero if equal, negative if v1 < v2.
// Mirrors upstream compareVersions (utils/changelog.ts:76-80).
func compareVersions(v1, v2 ChangelogEntry) int {
	if v1.Major != v2.Major {
		return v1.Major - v2.Major
	}
	if v1.Minor != v2.Minor {
		return v1.Minor - v2.Minor
	}
	return v1.Patch - v2.Patch
}

// GetNewEntries returns the subset of entries whose version is strictly
// newer than sinceVersion (a "x.y.z" string).
// Mirrors upstream getNewEntries (utils/changelog.ts:84-96).
func GetNewEntries(entries []ChangelogEntry, sinceVersion string) []ChangelogEntry {
	parts := strings.SplitN(sinceVersion, ".", 3)
	parseNum := func(s string) int {
		n, _ := strconv.Atoi(s)
		return n
	}
	var last ChangelogEntry
	if len(parts) >= 3 {
		last.Major = parseNum(parts[0])
		last.Minor = parseNum(parts[1])
		last.Patch = parseNum(parts[2])
	}
	var newer []ChangelogEntry
	for _, e := range entries {
		if compareVersions(e, last) > 0 {
			newer = append(newer, e)
		}
	}
	return newer
}
