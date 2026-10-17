package codingagent

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// referenceParseChangelog is the line-joining parser ParseChangelog replaced, kept as an independent reference: it is upstream parseChangelog (utils/changelog.ts) step for step.
func referenceParseChangelog(content string) []ChangelogEntry {
	if content == "" {
		return nil
	}
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
	for line := range strings.SplitSeq(content, "\n") {
		if strings.HasPrefix(line, "## ") {
			flush()
			if match := versionHeaderRE.FindStringSubmatch(line); match != nil {
				major, _ := strconv.Atoi(match[1])
				minor, _ := strconv.Atoi(match[2])
				patch, _ := strconv.Atoi(match[3])
				currentVersion = &ChangelogEntry{Major: major, Minor: minor, Patch: patch}
				currentLines = []string{line}
			} else {
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

var parseChangelogSink []ChangelogEntry

// ParseChangelog takes each entry's content as a substring of the changelog and returns what joining its lines returns, for the bundled changelog and for edge layouts.
func TestParseChangelogTakesEntriesAsSubstrings(t *testing.T) {
	bundled := bundledChangelog()
	cases := []string{
		bundled,
		"## [1.2.3]",
		"## [1.2.3]\n",
		"## [1.2.3]\n\n",
		"\n## [1.2.3]\nbody",
		"## [1.2.3]\r\n- a\r\n## [1.2.2]\r\n- b\r\n",
		"## [1.2.3]\n- a\n## [Unreleased]\n- u\n## [1.2.2]\n- b",
		"## [1.2.3]\n## [1.2.2]\n## [1.2.1]\n",
		"intro\n- x\n## [2.0.0] - 2026-01-01\n### Added\n- y\n\n",
		"## [1.0.0]\n- a\n##not\n ## [3.0.0]\n- b\n## \n",
		"\n\n\n",
	}
	for _, content := range cases {
		want, got := referenceParseChangelog(content), ParseChangelog(content)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ParseChangelog(%.60q) = %#v, want %#v", content, got, want)
		}
	}
	if len(bundled) > 0 {
		if len(ParseChangelog(bundled)) == 0 {
			t.Fatal("the bundled changelog has no entries")
		}
		want := testing.AllocsPerRun(5, func() { parseChangelogSink = referenceParseChangelog(bundled) })
		got := testing.AllocsPerRun(5, func() { parseChangelogSink = ParseChangelog(bundled) })
		if got >= want {
			t.Fatalf("ParseChangelog(bundled) = %v allocations, the line-joining parser %v", got, want)
		}
		for _, entry := range ParseChangelog(bundled) {
			if !strings.Contains(bundled, entry.Content) {
				t.Fatalf("an entry's content is not a substring of the changelog: %.60q", entry.Content)
			}
		}
	}
}
