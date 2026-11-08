package codingagent

import (
	"bufio"
	"io"
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
	// Upstream joins an entry's lines with "\n", which gives back the content from the entry's header line to the newline before the next "## " line (or to the end), so each entry's content is that substring.
	var entries []ChangelogEntry
	var currentVersion *ChangelogEntry
	currentStart := 0

	flush := func(end int) {
		if currentVersion != nil {
			entry := *currentVersion
			entry.Content = jsstring.Trim(content[currentStart:end])
			entries = append(entries, entry)
		}
	}

	for lineStart := 0; lineStart < len(content); {
		lineEnd := len(content)
		if i := strings.IndexByte(content[lineStart:], '\n'); i >= 0 {
			lineEnd = lineStart + i
		}
		if line := content[lineStart:lineEnd]; strings.HasPrefix(line, "## ") {
			// A current entry started on an earlier line, so lineStart-1 is the newline that ends it.
			flush(lineStart - 1)
			match := versionHeaderRE.FindStringSubmatch(line)
			if match != nil {
				major, _ := strconv.Atoi(match[1])
				minor, _ := strconv.Atoi(match[2])
				patch, _ := strconv.Atoi(match[3])
				currentVersion = &ChangelogEntry{Major: major, Minor: minor, Patch: patch}
				currentStart = lineStart
			} else {
				// Non-version header (e.g. `## [Unreleased]`): reset.
				currentVersion = nil
			}
		}
		lineStart = lineEnd + 1
	}
	flush(len(content))

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

// lastSeenVersionRE reads the leading major.minor.patch of a recorded version.
var lastSeenVersionRE = lazyregexp.New(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)`)

// GetNewEntries returns the entries whose version is newer than sinceVersion, and false when sinceVersion has no major.minor.patch
// to compare against. Only the leading major.minor.patch is read, like the changelog headings, so a prerelease or build suffix such as
// 0.1.5-20261020.7 counts as 0.1.5 and its notes are not shown again on every launch.
//
// pig divergence (D245): Pi 1.5.0 reads `lastVersion.split(".").map(Number)`, so 0.1.5-20261020.7 counts as 0.1.0 and an unreadable
// value as 0.0.0; Pi main 9a1953961 (#11753) reads the leading triple and returns undefined for an unreadable one.
func GetNewEntries(entries []ChangelogEntry, sinceVersion string) ([]ChangelogEntry, bool) {
	last, ok := parseLastSeenVersion(sinceVersion)
	if !ok {
		return nil, false
	}
	var newer []ChangelogEntry
	for _, e := range entries {
		if e.newerThan(last) {
			newer = append(newer, e)
		}
	}
	return newer, true
}

// parseLastSeenVersion reads the leading major.minor.patch of a recorded version, and reports false when it has none.
func parseLastSeenVersion(sinceVersion string) ([3]float64, bool) {
	parts := lastSeenVersionRE.FindStringSubmatch(jsstring.Trim(sinceVersion))
	if parts == nil {
		return [3]float64{}, false
	}
	var last [3]float64
	for i := range last {
		last[i], _ = strconv.ParseFloat(parts[i+1], 64)
	}
	return last, true
}

// newerThan reports whether the entry's version is greater than last, component by component.
func (e ChangelogEntry) newerThan(last [3]float64) bool {
	for i, value := range [3]float64{float64(e.Major), float64(e.Minor), float64(e.Patch)} {
		if value == last[i] {
			continue
		}
		return value > last[i]
	}
	return false
}

// parseChangelogNewerThan returns the entries ParseChangelog would find in the changelog r reads that are newer than last, reading only as far as
// they extend. A changelog lists its releases newest first (TestBundledChangelogIsNewestFirst holds the bundled one to it), so the first release
// that is not newer ends the read: the startup What's New of a build that is not an update, or whose recorded version is ahead of it, decodes a few
// kilobytes of the embedded changelog instead of inflating all of it. read reports the reader's failure, which an embedded changelog cannot have.
func parseChangelogNewerThan(r io.Reader, last [3]float64) (newer []ChangelogEntry, err error) {
	br := bufio.NewReader(r)
	var current *ChangelogEntry
	var body strings.Builder
	// finish closes the entry under construction. An entry's content runs from its heading line to the newline before the next "## " line,
	// which the trim removes with the rest.
	finish := func() {
		if current != nil {
			current.Content = jsstring.Trim(body.String())
			newer = append(newer, *current)
			current = nil
		}
		body.Reset()
	}
	for {
		line, readErr := br.ReadString('\n')
		if strings.HasPrefix(line, "## ") {
			finish()
			if match := versionHeaderRE.FindStringSubmatch(strings.TrimSuffix(line, "\n")); match != nil {
				major, _ := strconv.Atoi(match[1])
				minor, _ := strconv.Atoi(match[2])
				patch, _ := strconv.Atoi(match[3])
				entry := ChangelogEntry{Major: major, Minor: minor, Patch: patch}
				if !entry.newerThan(last) {
					return newer, nil
				}
				current = &entry
			}
		}
		if current != nil {
			body.WriteString(line)
		}
		if readErr != nil {
			if readErr == io.EOF {
				finish()
				return newer, nil
			}
			return newer, readErr
		}
	}
}
