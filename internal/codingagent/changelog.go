package codingagent

import (
	"regexp"
	"strconv"
	"strings"
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
var versionHeaderRE = regexp.MustCompile(`^##\s+\[?(\d+)\.(\d+)\.(\d+)\]?`)

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

// FormatChangelogForChat takes parsed entries and produces the markdown
// block /changelog appends to the chat. Mirrors upstream
// `handleChangelogCommand` (interactive-mode.ts:4795-4814) shape:
//   - reverse the entries (so they read oldest-at-top, newest-at-bottom
//     within the inline block: matches upstream's `.reverse().map(...)`),
//   - join their content with two newlines,
//   - wrap in a bold "What's New" header and horizontal-rule "borders"
//     (pig's markdown renderer paints `---` as a separator line; this
//     is the parity-correct equivalent of upstream's DynamicBorder).
//
// Empty input returns the upstream-verbatim "No changelog entries found."
// fallback so a user with a missing/empty CHANGELOG sees a sensible
// message instead of an empty bordered block.
func FormatChangelogForChat(entries []ChangelogEntry) string {
	if len(entries) == 0 {
		return "No changelog entries found."
	}
	// Reverse in place on a copy.
	rev := make([]ChangelogEntry, len(entries))
	for i, e := range entries {
		rev[len(entries)-1-i] = e
	}
	parts := make([]string, 0, len(rev))
	for _, e := range rev {
		parts = append(parts, e.Content)
	}
	body := strings.Join(parts, "\n\n")

	var b strings.Builder
	b.WriteString("---\n\n")
	b.WriteString("**What's New**\n\n")
	b.WriteString(body)
	b.WriteString("\n\n---")
	return b.String()
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
