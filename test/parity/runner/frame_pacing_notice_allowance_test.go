//go:build parity

package runner

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// framePacingNotice is the warning PiG's interactive mode shows at start when a run selects the adaptive frame pacing and not the screen writer, as D625 states it (docs/additive-features.md). Pi has no such warning.
const framePacingNotice = `Warning: Adaptive frame pacing (terminal.framePacing, PIG_FRAME_PACING) has no effect without the screen writer (terminal.screenWriter "writer"): frames keep Pi's 16 ms interval.`

// The warning's foreground as tmux's escaped capture writes it, and the return to the default foreground.
const (
	framePacingNoticeColor = `\x1b\[38;2;205;154;34m`
	framePacingNoticeReset = `\x1b\[39m`
)

// framePacingNoticeRows matches the notice wrapped at any width: between two words stands one space or rowBreak, the bytes a capture holds from the end of one row of the block to the first word of the next.
func framePacingNoticeRows(rowBreak string) string {
	words := strings.Fields(framePacingNotice)
	for i, word := range words {
		words[i] = regexp.QuoteMeta(word)
	}
	return strings.Join(words, "(?: |"+rowBreak+")")
}

// framePacingNoticeCut removes a part of the notice block from a capture. The pattern must match exactly once. A pattern with a group matches a row of the block that an overlay cuts short: the group is what is left of the notice's text, and must be a piece of it.
type framePacingNoticeCut struct {
	pattern *regexp.Regexp
	keep    string
}

// framePacingNoticeAllowed are the scenarios of D625's parity allowance: the ones whose asserted crop includes rows where the notice block is drawn, so that PiG under PIG_FRAME_PACING=adaptive without the screen writer differs there from Pi. For each, the block is cut from the capture the scenario's comparator reads (the escaped capture, or the plain one), and what is left must be Pi's capture under that comparator.
var framePacingNoticeAllowed = []struct {
	path    string
	escaped bool
	cuts    []framePacingNoticeCut
}{
	// The main screen after an exit: the block's rows (the text and the blank row after it) stand between the resumed transcript and the status line. The cut starts at the block's first coloured cell and ends with the left margin of the row after the block, so the bytes tmux writes for the first cell after the block stay where Pi has them.
	{"interactive-rendering/22-exit-final-layout.toml", true, []framePacingNoticeCut{{
		regexp.MustCompile(framePacingNoticeColor + framePacingNoticeRows(`\n`+framePacingNoticeReset+` `+framePacingNoticeColor) + framePacingNoticeReset + `\n\n `), "",
	}}},
	// The main screen during a compaction: the block's rows stand between the resumed transcript and the editor.
	{"fullscreen/09-main-screen-compaction-in-progress.toml", false, []framePacingNoticeCut{{
		regexp.MustCompile(`(?m)^ ` + framePacingNoticeRows(`\n `) + ` *\n *\n`), "",
	}}},
	// The alternate screen with the search box open: the block's rows are the startup rows the box overlays, so each of the two is cut short by the box. The scenario already drops the cells left of the box; the cuts require that those cells are the notice's and drop the colour tmux writes for the block's cells right of the box.
	{"fullscreen/13-alt-screen-search-input.toml", true, []framePacingNoticeCut{
		{regexp.MustCompile(`(?m)^ ` + framePacingNoticeColor + `([^\n\x1b]*)` + framePacingNoticeReset + `│ expr`), "│ expr"},
		{regexp.MustCompile(framePacingNoticeColor + `\n` + framePacingNoticeReset + ` ` + framePacingNoticeColor + `([^\n\x1b]*)` + framePacingNoticeReset + `└`), "\n└"},
	}},
}

// framePacingNoticeSelection is the set of scenario files this invocation runs, selected as TestParity selects them (directory, tags, drivers, shard): the allowance of a scenario is checked by the parity run that runs the scenario.
func framePacingNoticeSelection(t *testing.T, canonicalDir string) map[string]*Scenario {
	t.Helper()
	dir := *flagScenarioDir
	if dir == "" {
		dir = canonicalDir
	}
	scenarios, err := DiscoverScenarios(dir)
	if err != nil {
		t.Fatalf("discover scenarios: %v", err)
	}
	if value := strings.TrimSpace(*flagTags); value != "" {
		scenarios = FilterByTags(scenarios, strings.Split(value, ","))
	}
	if value := strings.TrimSpace(*flagExcludeTags); value != "" {
		scenarios, _ = FilterOutTags(scenarios, strings.Split(value, ","))
	}
	if value := strings.TrimSpace(*flagDrivers); value != "" {
		scenarios = FilterByDrivers(scenarios, strings.Split(value, ","))
	}
	if *flagRuntimeOnly {
		scenarios = FilterByRuntimeRatio(scenarios)
	}
	if value := strings.TrimSpace(*flagShard); value != "" {
		shard, shards, err := parseShard(value)
		if err != nil {
			t.Fatal(err)
		}
		scenarios = FilterByShard(scenarios, shard, shards)
	}
	selected := make(map[string]*Scenario, len(scenarios))
	for _, sc := range scenarios {
		selected[filepath.Clean(sc.SourcePath)] = sc
	}
	return selected
}

// D625's parity allowance, kept: under PIG_FRAME_PACING=adaptive without the screen writer PiG differs from Pi, in the three scenarios the record names, by the startup notice and by nothing else. Each scenario is run as it stands with the two variables added to PiG's environment. Its own assertions must fail (the block is in the asserted crop), the block must be found exactly where the cuts say, and with the block cut from PiG's capture the scenario's own assertions must pass. A capture in which one more glyph differs must fail them again, and the record must state the notice's text and name the scenario.
func TestFramePacingNoticeIsTheOnlyDifferenceUnderAdaptivePacing(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("locate repo root: %v", err)
	}
	canonicalDir := filepath.Join(repoRoot, "test/parity", "scenarios")
	selected := framePacingNoticeSelection(t, canonicalDir)
	record, err := os.ReadFile(filepath.Join(repoRoot, "docs", "additive-features.md"))
	if err != nil {
		t.Fatal(err)
	}
	// The binaries are resolved once, for the test, and only when this invocation runs one of the scenarios.
	var pig, pi BinaryRef
	for _, allowed := range framePacingNoticeAllowed {
		if selected[filepath.Join(canonicalDir, filepath.FromSlash(allowed.path))] != nil && pig.Path == "" {
			// No binary was provided at all (neither PIG_PARITY_PIG_BIN nor PIG_BIN): the input is absent, not wrong, so this test has nothing to run against and skips. Every parity gate sets PIG_PARITY_PIG_BIN, and a path that is set but missing still fails in ResolvePigBin.
			if pigBinUnderTest() == "" {
				t.Skip("no pig binary under test: build one (`go build -o bin/pig-parity ./cmd/pig`) and set PIG_PARITY_PIG_BIN (or PIG_BIN); the parity make targets (`make parity`) do this")
			}
			if err := upstreamMirrorMissing(repoRoot); err != nil {
				t.Fatal(err)
			}
			pig = ResolvePigBin(t)
			pi = pinPiPackageDir(t, ResolveUpstreamPiBin(t))
			requireFreshPig(t, repoRoot, pig, *flagAllowStale)
		}
	}
	for _, allowed := range framePacingNoticeAllowed {
		t.Run(strings.TrimSuffix(allowed.path, ".toml"), func(t *testing.T) {
			if !strings.Contains(string(record), "`"+framePacingNotice+"`") || !strings.Contains(string(record), "`"+strings.TrimSuffix(allowed.path, ".toml")+"`") {
				t.Fatalf("D625 (docs/additive-features.md) does not state the notice as %q and name the scenario %s: the record and this check have parted", framePacingNotice, allowed.path)
			}
			sc := selected[filepath.Join(canonicalDir, filepath.FromSlash(allowed.path))]
			if sc == nil {
				t.Skipf("%s is not among the scenarios this invocation runs", allowed.path)
			}
			adaptive := *sc
			adaptive.Assert.Runs = 1
			adaptive.Env.Pig = append(slices.Clone(sc.Env.Pig), "PIG_FRAME_PACING=adaptive", "PIG_TUI_SCREEN_WRITER=pi")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			o := RunScenario(ctx, t, &adaptive, pig, pi, true)
			if len(o.Pig.Runs) != 1 || len(o.Pi.Runs) != 1 {
				t.Fatalf("the scenario did not complete one pair: %v", o.Failures)
			}
			if o.Passed() {
				t.Fatalf("the scenario passes under PIG_FRAME_PACING=adaptive without the screen writer: its asserted crop no longer holds the notice, and D625's allowance for it is stale")
			}
			capture, piCapture := &o.Pig.Runs[0].Output, o.Pi.Runs[0].Output
			if allowed.escaped {
				capture, piCapture = &o.Pig.Runs[0].Escaped, o.Pi.Runs[0].Escaped
			}
			for _, cut := range allowed.cuts {
				found := cut.pattern.FindAllStringSubmatch(*capture, -1)
				if len(found) != 1 {
					t.Fatalf("the notice block was found %d times by %s, want once, in PiG's capture:\n%s", len(found), cut.pattern, *capture)
				}
				if len(found[0]) > 1 && (found[0][1] == "" || !strings.Contains(framePacingNotice, found[0][1])) {
					t.Fatalf("the cells an overlay leaves of the block hold %q, which is no piece of the notice", found[0][1])
				}
				*capture = cut.pattern.ReplaceAllLiteralString(*capture, cut.keep)
			}
			o.Failures = nil
			EvaluateOutcome(o)
			if !o.Passed() {
				t.Fatalf("without the notice block PiG's capture is not Pi's under the scenario's own assertions: %v\n%s", o.Failures, LineDiff(*capture, piCapture))
			}
			// The assertions still tell one changed glyph: the first letter of the capture, which is not the notice's.
			at := strings.IndexFunc(*capture, func(r rune) bool { return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') })
			if at < 0 {
				t.Fatal("PiG's capture holds no letter to change")
			}
			changed := "X"
			if (*capture)[at] == 'X' {
				changed = "Y"
			}
			*capture = (*capture)[:at] + changed + (*capture)[at+1:]
			o.Failures = nil
			EvaluateOutcome(o)
			if o.Passed() {
				t.Fatalf("a capture with one changed glyph outside the notice block passes the scenario's assertions: this check proves nothing")
			}
		})
	}
}
