package codingagent

import (
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// pi: packages/coding-agent/src/utils/changelog.ts parseChangelog and getNewEntries against pinned Pi over random changelogs assembled from release headers (bracketed or bare, with odd versions, huge and zero-padded numbers, stray text and whitespace forms), bodies and every newline form. Versions beyond int64 are left out: ChangelogEntry holds ints, Pi doubles.
func TestChangelogRandomMatchesPi(t *testing.T) {
	versions := []string{"1.2.3", "0.0.0", "1.2.1", "0.0.1", "1.0.0", "10.20.30", "01.002.0003", "1.2", "1", "1.2.3.4", "1.0.0-beta", "9007199254740993.0.0", "1e3.0.0", "٣.٤.٥", "1.2.3beta", " 1.2.3", "v1.2.3", "1..3", "-1.2.3"}
	headers := []string{"## [%s]", "## [%s] - 2026-01-01", "## %s", "##%s", "##  [%s]", "##\t[%s]", "## \u00a0[%s]", " ## [%s]", "### [%s]", "## [Unreleased]", "## [%s", "## Notes ## %s", "## [%s]]"}
	fillers := []string{"- item", "text", "", "  ", "\u00a0", "\ufeff", "\u2003x\u2003", "\u0085", "### sub", "😀", "## not a version", "[1.2.3]"}
	newlines := []string{"\n", "\n", "\r\n", "\r", "\n\n"}
	rng := rand.New(rand.NewSource(20261010))
	var changelogs []string
	for range 1500 {
		var b strings.Builder
		for range rng.Intn(6) {
			if rng.Intn(3) != 0 {
				header := headers[rng.Intn(len(headers))]
				if before, after, found := strings.Cut(header, "%s"); found {
					header = before + versions[rng.Intn(len(versions))] + after
				}
				b.WriteString(header)
				b.WriteString(newlines[rng.Intn(len(newlines))])
			}
			for range rng.Intn(4) {
				b.WriteString(fillers[rng.Intn(len(fillers))])
				b.WriteString(newlines[rng.Intn(len(newlines))])
			}
		}
		changelogs = append(changelogs, b.String())
	}
	lastVersions := []string{"", "0", "1", "1.2", "1.2.2", "1.2.3", "1.2.3.4", "10.20.30", "0.0.0", "9007199254740993.0.0", "1e0.0.0", " 1.0.0", "a.b.c", "1..3", "-1.2.3"}
	dir := t.TempDir()
	for i, text := range changelogs {
		if err := os.WriteFile(filepath.Join(dir, "c"+strconv.Itoa(i)+".md"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	type entry struct {
		Major   float64 `json:"major"`
		Minor   float64 `json:"minor"`
		Patch   float64 `json:"patch"`
		Content string  `json:"content"`
	}
	var want [][][]entry
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/utils/changelog.js");
const { join } = await import("node:path");
emit(input.changelogs.map((_, i) => {
	const entries = mod.parseChangelog(join(input.dir, "c" + i + ".md"));
	return [entries, ...input.lastVersions.map((v) => mod.getNewEntries(entries, v))];
}));`, map[string]any{"changelogs": changelogs, "lastVersions": lastVersions, "dir": dir}, &want)
	failures := 0
	for i, text := range changelogs {
		entries := ParseChangelog(text)
		check := func(label string, got []ChangelogEntry, want []entry) {
			same := len(got) == len(want)
			for j := 0; same && j < len(got); j++ {
				same = (entry{float64(got[j].Major), float64(got[j].Minor), float64(got[j].Patch), got[j].Content}) == want[j]
			}
			if !same {
				if failures++; failures <= 8 {
					t.Errorf("changelog %d %s: Go %+v, Pi %+v (%q)", i, label, got, want, text)
				}
			}
		}
		check("parse", entries, want[i][0])
		for k, last := range lastVersions {
			check("since "+last, GetNewEntries(entries, last), want[i][k+1])
		}
	}
	if failures > 8 {
		t.Errorf("%d results differ from Pi", failures)
	}
}
