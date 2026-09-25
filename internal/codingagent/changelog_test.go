package codingagent

import (
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/pigversion"
)

// /changelog parser + handler.

func TestParseChangelog_SingleEntry(t *testing.T) {
	in := `# Changelog

## [0.0.1]: 2026-04-27

### Added
- thing one
- thing two
`
	got := ParseChangelog(in)
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	e := got[0]
	if e.Major != 0 || e.Minor != 0 || e.Patch != 1 {
		t.Errorf("version=%d.%d.%d want 0.0.1", e.Major, e.Minor, e.Patch)
	}
	if !strings.Contains(e.Content, "thing one") {
		t.Errorf("body missing: %q", e.Content)
	}
	if !strings.HasPrefix(e.Content, "## [0.0.1]") {
		t.Errorf("content should include header line, got: %q", e.Content[:minClampInt(50, len(e.Content))])
	}
}

func TestParseChangelog_MultipleEntries(t *testing.T) {
	in := `# Changelog

## [Unreleased]

### Added
- in-progress thing

## [0.1.0]: 2026-05-15

### Added
- second release

## [0.0.1]: 2026-04-27

### Added
- first release
`
	got := ParseChangelog(in)
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2 (Unreleased should be skipped)", len(got))
	}
	if got[0].Minor != 1 || got[0].Patch != 0 {
		t.Errorf("first entry should be 0.1.0, got %d.%d.%d",
			got[0].Major, got[0].Minor, got[0].Patch)
	}
	if got[1].Patch != 1 {
		t.Errorf("second entry should be 0.0.1, got %d.%d.%d",
			got[1].Major, got[1].Minor, got[1].Patch)
	}
	if !strings.Contains(got[0].Content, "second release") {
		t.Errorf("first entry body wrong: %q", got[0].Content)
	}
	if strings.Contains(got[0].Content, "in-progress") {
		t.Errorf("Unreleased content leaked into 0.1.0 entry")
	}
	if strings.Contains(got[1].Content, "second release") {
		t.Errorf("0.1.0 content leaked into 0.0.1 entry")
	}
}

func TestParseChangelog_UnreleasedAtTopSkipped(t *testing.T) {
	// Verifies the upstream-parity behavior: `## [Unreleased]` is a
	// non-version header, so its body must NOT attach to the next
	// version entry.
	in := `## [Unreleased]

- secret feature

## [1.0.0]

- shipped
`
	got := ParseChangelog(in)
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if strings.Contains(got[0].Content, "secret") {
		t.Errorf("Unreleased body bled into 1.0.0: %q", got[0].Content)
	}
}

func TestParseChangelog_BareNumericHeader(t *testing.T) {
	// Upstream regex accepts both `## [1.2.3]` and `## 1.2.3` (the
	// `\[?` and `\]?` are optional).
	in := `## 2.5.0

- bare numeric header without brackets
`
	got := ParseChangelog(in)
	if len(got) != 1 || got[0].Major != 2 || got[0].Minor != 5 {
		t.Fatalf("expected 2.5.0 entry, got %#v", got)
	}
}

func TestParseChangelog_Empty(t *testing.T) {
	if got := ParseChangelog(""); got != nil {
		t.Errorf("empty input should return nil, got %#v", got)
	}
	if got := ParseChangelog("# Just a title\n\nNo version headers here.\n"); len(got) != 0 {
		t.Errorf("no version headers should return empty slice, got %d", len(got))
	}
}

func TestParseChangelog_RealFile(t *testing.T) {
	pigChangelog := readBundledChangelog(t)
	if !strings.Contains(pigChangelog, "## [Unreleased]") {
		t.Fatal("bundled CHANGELOG.md must keep an [Unreleased] section")
	}
	got := ParseChangelog(pigChangelog)
	if len(got) < 2 {
		t.Fatalf("bundled CHANGELOG.md needs the current release and the 0.0.0 baseline: %#v", got)
	}
	newest := got[0] // file order: newest first
	if v := fmt.Sprintf("%d.%d.%d", newest.Major, newest.Minor, newest.Patch); v != pigversion.PigVersion {
		t.Fatalf("newest CHANGELOG.md entry is %s, want the current PigVersion %s", v, pigversion.PigVersion)
	}
	baseline := got[len(got)-1]
	if baseline.Major != 0 || baseline.Minor != 0 || baseline.Patch != 0 {
		t.Fatalf("bundled CHANGELOG.md has no 0.0.0 development baseline: %#v", baseline)
	}
	for _, entry := range got {
		if strings.Contains(entry.Content, "[Unreleased]") {
			t.Fatalf("unreleased content bled into a versioned entry: %q", entry.Content)
		}
	}
}

func readBundledChangelog(t *testing.T) string {
	t.Helper()
	// Indirection so the test file doesn't import the root package
	// directly (which would create a tight coupling and break the
	// file's portability for future refactors). The handler does
	// the import; here we read via a minimal helper.
	return getBundledChangelogForTest()
}

func TestCompareChangelogEntries(t *testing.T) {
	cases := []struct {
		name string
		a, b ChangelogEntry
		want int
	}{
		{"equal", ChangelogEntry{1, 2, 3, ""}, ChangelogEntry{1, 2, 3, ""}, 0},
		{"a-major-newer", ChangelogEntry{2, 0, 0, ""}, ChangelogEntry{1, 9, 9, ""}, 1},
		{"b-major-newer", ChangelogEntry{1, 9, 9, ""}, ChangelogEntry{2, 0, 0, ""}, -1},
		{"a-minor-newer", ChangelogEntry{1, 2, 0, ""}, ChangelogEntry{1, 1, 9, ""}, 1},
		{"a-patch-newer", ChangelogEntry{0, 0, 2, ""}, ChangelogEntry{0, 0, 1, ""}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CompareChangelogEntries(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("got=%d want=%d", got, tc.want)
			}
		})
	}
}

func TestFormatChangelogForChat_EmptyFallback(t *testing.T) {
	got := FormatChangelogForChat(nil)
	if got != "No changelog entries found." {
		t.Errorf("got=%q want upstream-verbatim fallback", got)
	}
}

func TestFormatChangelogForChat_ReversesOrder(t *testing.T) {
	// Input order: newest-first (matches CHANGELOG convention).
	// Output order within the inline block: oldest-first (matches
	// upstream `.reverse().map(...)` so newest appears last).
	in := []ChangelogEntry{
		{Major: 0, Minor: 2, Patch: 0, Content: "## [0.2.0]\nNEWEST"},
		{Major: 0, Minor: 1, Patch: 0, Content: "## [0.1.0]\nMIDDLE"},
		{Major: 0, Minor: 0, Patch: 1, Content: "## [0.0.1]\nOLDEST"},
	}
	got := FormatChangelogForChat(in)
	oldestPos := strings.Index(got, "OLDEST")
	middlePos := strings.Index(got, "MIDDLE")
	newestPos := strings.Index(got, "NEWEST")
	if oldestPos < 0 || middlePos < 0 || newestPos < 0 {
		t.Fatalf("missing entries in output: %q", got)
	}
	if oldestPos >= middlePos || middlePos >= newestPos {
		t.Errorf("expected oldest-first ordering, got positions: oldest=%d middle=%d newest=%d",
			oldestPos, middlePos, newestPos)
	}
	if !strings.Contains(got, "**What's New**") {
		t.Errorf("missing What's New header: %q", got)
	}
	if !strings.HasPrefix(got, "---\n\n") || !strings.HasSuffix(got, "\n\n---") {
		t.Errorf("missing horizontal-rule borders: %q", got[:minClampInt(40, len(got))])
	}
}

func TestChangelogHandler_AppendsDevelopmentBaseline(t *testing.T) {
	sc, out := newFakeSlashCtx()
	if err := changelogHandler(sc); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "**What's New**") || !strings.Contains(got, "[0.0.0]") {
		t.Errorf("expected versioned development baseline: %q", got[:minClampInt(120, len(got))])
	}
}

func TestChangelogHandler_IgnoresCollapseSetting(t *testing.T) {
	sc, out := newFakeSlashCtx()
	sc.SettingsManager = &SettingsManager{merged: Settings{CollapseChangelog: true}}
	if err := changelogHandler(sc); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "**What's New**") || !strings.Contains(got, "[0.0.0]") {
		t.Fatalf("expected full development changelog block: %q", got[:minClampInt(120, len(got))])
	}
	if strings.Contains(got, "Updated to v") {
		t.Fatalf("/changelog should ignore collapse setting, got condensed text: %q", got)
	}
}

func minClampInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestGetNewEntries(t *testing.T) {
	content := "## [2.0.0]\nNew major.\n\n## [1.1.0]\nMinor bump.\n\n## [1.0.0]\nInitial."
	entries := ParseChangelog(content)

	// All entries newer than 0.0.0
	got := GetNewEntries(entries, "0.0.0")
	if len(got) != 3 {
		t.Fatalf("want 3 entries newer than 0.0.0, got %d", len(got))
	}

	// Two entries newer than 1.0.0
	got = GetNewEntries(entries, "1.0.0")
	if len(got) != 2 {
		t.Fatalf("want 2 entries newer than 1.0.0, got %d", len(got))
	}
	if got[0].Major != 2 || got[1].Minor != 1 {
		t.Errorf("wrong entries: %+v", got)
	}

	// No entries newer than 2.0.0
	got = GetNewEntries(entries, "2.0.0")
	if len(got) != 0 {
		t.Fatalf("want 0 entries newer than 2.0.0, got %d", len(got))
	}

	// Malformed sinceVersion treated as 0.0.0
	got = GetNewEntries(entries, "bad")
	if len(got) != 3 {
		t.Fatalf("want 3 entries for malformed sinceVersion, got %d", len(got))
	}
}
