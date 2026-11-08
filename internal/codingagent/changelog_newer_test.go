//go:build !pig_strip_changelog

package codingagent

import (
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

type changelogByteCounter struct {
	r io.Reader
	n int
}

func (c *changelogByteCounter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

// parseChangelogNewerThan returns what filtering ParseChangelog's entries with GetNewEntries returns, for texts that list their releases newest first.
func TestParseChangelogNewerThanMatchesTheFilteredParse(t *testing.T) {
	bundled := bundledChangelog()
	texts := []string{
		bundled,
		"",
		"## [1.2.3]",
		"## [1.2.3]\n",
		"\n## [1.2.3]\nbody",
		"## [1.2.3]\r\n- a\r\n## [1.2.2]\r\n- b\r\n",
		"## [Unreleased]\n- u\n## [1.2.3]\n- a\n## [1.2.2]\n- b",
		"intro\n- x\n## [2.0.0] - 2026-01-01\n### Added\n- y\n\n## [Unreleased]\n- skipped\n## [1.0.0]\n- z\n",
		"## [3.0.0]\n- a\n##not\n ## [2.0.0]\n- b\n## \n## [1.0.0]\n- c\n",
		"## 2.0.0\nplain heading\n\n## 1.9.9\nold\n",
	}
	versions := []string{"0.0.0", "0.0.1", "1.0.0", "1.2.2", "1.2.3", "1.9.9", "2.0.0", "2.0.1", "3.0.0", "9999.0.0", "v1.2.2", "1.2.2-rc.1"}
	for _, e := range ParseChangelog(bundled) {
		versions = append(versions, fmt.Sprintf("%d.%d.%d", e.Major, e.Minor, e.Patch), fmt.Sprintf("%d.%d.%d", e.Major, e.Minor, e.Patch+1), fmt.Sprintf("%d.%d.%d", e.Major, e.Minor, max(e.Patch-1, 0)))
	}
	for _, text := range texts {
		for _, version := range versions {
			last, ok := parseLastSeenVersion(version)
			if !ok {
				t.Fatalf("version %q has no triple", version)
			}
			want, _ := GetNewEntries(ParseChangelog(text), version)
			got, err := parseChangelogNewerThan(strings.NewReader(text), last)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("parseChangelogNewerThan(%.50q, %s) = %d entries, %v; want %d entries", text, version, len(got), err, len(want))
			}
		}
	}
}

// The startup What's New reads the bundled changelog only as far as the entries newer than the recorded version, which is what makes it a prefix read:
// the releases are listed newest first. A release listed out of order below an older one would be missed.
func TestBundledChangelogIsNewestFirst(t *testing.T) {
	entries := ParseChangelog(bundledChangelog())
	if len(entries) == 0 {
		t.Fatal("the bundled changelog has no release entries")
	}
	for i := 1; i < len(entries); i++ {
		if CompareChangelogEntries(entries[i-1], entries[i]) < 0 {
			t.Fatalf("CHANGELOG.md lists %d.%d.%d above the older %d.%d.%d: the releases must be newest first (startupChangelog reads only the newer prefix)",
				entries[i].Major, entries[i].Minor, entries[i].Patch, entries[i-1].Major, entries[i-1].Minor, entries[i-1].Patch)
		}
	}
}

// A recorded version at or ahead of the newest release has no newer entries, and the read stops at the first release heading.
func TestParseChangelogNewerThanStopsAtTheFirstOlderRelease(t *testing.T) {
	counter := &changelogByteCounter{r: bundledChangelogReader()}
	last, _ := parseLastSeenVersion("9999.0.0")
	got, err := parseChangelogNewerThan(counter, last)
	if err != nil || len(got) != 0 {
		t.Fatalf("parseChangelogNewerThan(9999.0.0) = %d entries, %v; want none", len(got), err)
	}
	if counter.n > 64<<10 {
		t.Fatalf("read %d bytes of the changelog to find no newer entry; want only the head", counter.n)
	}
}
