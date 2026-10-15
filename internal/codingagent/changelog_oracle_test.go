package codingagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's parseChangelog and getNewEntries (utils/changelog.ts) run against the vendored Pi module for the same changelog text and last-seen versions.
func TestChangelogMatchesPiOracle(t *testing.T) {
	changelogs := []string{
		"",
		"# Changelog\n\n## [1.2.3] - 2026-01-01\n- a\n\n## [1.2.2]\n- b\n\n## [0.9.10]\n- c\n",
		"## 1.0.0\nx\n## 1.0.1\ny\n",
		"## [Unreleased]\n- pending\n\n## [1.0.4]\n- done\n",
		"## [1.0.0]\n\n\n## [1.0.1]\nbody\n",
		"## [1.0.0]\r\n- crlf\r\n## [1.0.1]\r\n- two\r\n",
		"## \u00a0[1.0.0]\nnbsp header\n## [1.0.1]\nx\n",
		"##  [1.0.0]\ntwo spaces\n##\t[1.0.1]\ntab\n",
		"## Notes ## 1.2.3\nsecond marker\n",
		"## [1.0.0]\n\ufeff\n\u00a0\nbody \u00a0\n\n## [1.0.1]\n\u2003x\u2003\n",
		"## [01.002.0003]\nleading zeros\n## [1.2.3.4]\nfour parts\n## [1.2]\ntwo parts\n",
		"## [1.0.0-beta]\nbeta\n## [10.0.0]\nten\n## [2.0.0]\ntwo\n",
		"preamble\n## [3.0.0]\nthree\n### sub\nx\n## [broken\ny\n## [2.9.9]\nz\n",
		" ## [1.0.0]\nindented header\n",
		"## [1.0.0]\nbody\ufeff\n\ufeff\n## [1.0.1]\nnel\u0085\n\u0085",
	}
	lastVersions := []string{"", "0", "1", "1.0", "1.2", "1.2.2", "1.2.3", "1.2.3.4", "1.0.0-beta", "0.9.9", "10", "10.0.0", " 1.0.0", "1.0.0 ", "01.0.0", "1e0.0.0", "0x1.0.0", "a.b.c", "1..3", ".1.1", "-1.0.0", "1.-1.0", "Infinity.0.0", "2.9.9", "1.0.1"}
	type input struct {
		Changelogs   []string `json:"changelogs"`
		LastVersions []string `json:"lastVersions"`
		Dir          string   `json:"dir"`
	}
	type entry struct {
		Major   float64 `json:"major"`
		Minor   float64 `json:"minor"`
		Patch   float64 `json:"patch"`
		Content string  `json:"content"`
	}
	dir := t.TempDir()
	for i, text := range changelogs {
		if err := os.WriteFile(filepath.Join(dir, "c"+string(rune('a'+i))+".md"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var want [][][]entry
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/utils/changelog.js");
const { join } = await import("node:path");
emit(input.changelogs.map((_, i) => {
	const entries = mod.parseChangelog(join(input.dir, "c" + String.fromCharCode(97 + i) + ".md"));
	return [entries, ...input.lastVersions.map((v) => mod.getNewEntries(entries, v))];
}));`, input{changelogs, lastVersions, dir}, &want)
	for i, text := range changelogs {
		entries := ParseChangelog(text)
		check := func(label string, got []ChangelogEntry, want []entry) {
			if len(got) != len(want) {
				t.Errorf("changelog %d %s: %d entries, Pi %d (%q)", i, label, len(got), len(want), text)
				return
			}
			for j := range got {
				if (entry{float64(got[j].Major), float64(got[j].Minor), float64(got[j].Patch), got[j].Content}) != want[j] {
					t.Errorf("changelog %d %s entry %d: Go %+v, Pi %+v", i, label, j, got[j], want[j])
				}
			}
		}
		check("parse", entries, want[i][0])
		for k, last := range lastVersions {
			check("since "+last, GetNewEntries(entries, last), want[i][k+1])
		}
	}
}
