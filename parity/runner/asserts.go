//go:build parity

package runner

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/MichaelKinsy/PiG/coding"
)

// EvaluateOutcome runs every configured assertion against the pig and
// pi result groups, populating Outcome.Failures.
//
// The function is symmetric: every failure says which side(s) failed so
// the test output points at the real problem, not just "they don't match".
func EvaluateOutcome(o *ScenarioOutcome) {
	a := o.Scenario.Assert
	o.Pig.MedianMs = medianRuntime(o.Pig.Runs)
	o.Pi.MedianMs = medianRuntime(o.Pi.Runs)

	// exit_code
	if a.ExitCode != nil {
		wantCode := *a.ExitCode
		for _, r := range o.Pig.Runs {
			if r.ExitCode != wantCode {
				o.Failures = append(o.Failures,
					fmt.Sprintf("pig exit_code=%d want %d", r.ExitCode, wantCode))
				break
			}
		}
		for _, r := range o.Pi.Runs {
			if r.ExitCode != wantCode {
				o.Failures = append(o.Failures,
					fmt.Sprintf("pi exit_code=%d want %d", r.ExitCode, wantCode))
				break
			}
		}
	}

	if a.PigExitCode != nil {
		wantCode := *a.PigExitCode
		for _, result := range o.Pig.Runs {
			if result.ExitCode != wantCode {
				o.Failures = append(o.Failures, fmt.Sprintf("pig exit_code=%d want %d", result.ExitCode, wantCode))
				break
			}
		}
	}
	if a.PiExitCode != nil {
		wantCode := *a.PiExitCode
		for _, result := range o.Pi.Runs {
			if result.ExitCode != wantCode {
				o.Failures = append(o.Failures, fmt.Sprintf("pi exit_code=%d want %d", result.ExitCode, wantCode))
				break
			}
		}
	}

	// both_contain
	for _, want := range a.BothContain {
		// Allow this substring to be claimed by a divergence block.
		if slices.Contains(o.Scenario.Diverge.PigContains, want) ||
			slices.Contains(o.Scenario.Diverge.PiContains, want) {
			continue
		}
		if !allRunsContain(o.Pig.Runs, want) {
			o.Failures = append(o.Failures,
				fmt.Sprintf("pig output missing %q", want))
		}
		if !allRunsContain(o.Pi.Runs, want) {
			o.Failures = append(o.Failures,
				fmt.Sprintf("pi output missing %q", want))
		}
	}

	// both_not_contain
	for _, deny := range a.BothNotContain {
		if anyRunContains(o.Pig.Runs, deny) {
			o.Failures = append(o.Failures,
				fmt.Sprintf("pig output unexpectedly contains %q", deny))
		}
		if anyRunContains(o.Pi.Runs, deny) {
			o.Failures = append(o.Failures,
				fmt.Sprintf("pi output unexpectedly contains %q", deny))
		}
	}

	// pig_contains / pi_contains (asymmetric: used for divergences too)
	allPigContains := expandVersionTokens(a.PigContains, o.Scenario.Diverge.PigContains)
	for _, want := range allPigContains {
		if !allRunsContain(o.Pig.Runs, want) {
			o.Failures = append(o.Failures,
				fmt.Sprintf("pig output missing %q (asymmetric)", want))
		}
	}
	for _, deny := range a.PigNotContain {
		if anyRunContains(o.Pig.Runs, deny) {
			o.Failures = append(o.Failures,
				fmt.Sprintf("pig output unexpectedly contains %q (asymmetric)", deny))
		}
	}
	allPiContains := expandVersionTokens(a.PiContains, o.Scenario.Diverge.PiContains)
	for _, want := range allPiContains {
		if !allRunsContain(o.Pi.Runs, want) {
			o.Failures = append(o.Failures,
				fmt.Sprintf("pi output missing %q (asymmetric)", want))
		}
	}
	for _, deny := range a.PiNotContain {
		if anyRunContains(o.Pi.Runs, deny) {
			o.Failures = append(o.Failures,
				fmt.Sprintf("pi output unexpectedly contains %q (asymmetric)", deny))
		}
	}

	// both_match_regex
	for _, pat := range a.BothMatchRegex {
		re, err := regexp.Compile(pat)
		if err != nil {
			o.Failures = append(o.Failures,
				fmt.Sprintf("invalid regex %q: %v", pat, err))
			continue
		}
		if !allRunsMatchRegex(o.Pig.Runs, re) {
			o.Failures = append(o.Failures,
				fmt.Sprintf("pig output does not match /%s/", pat))
		}
		if !allRunsMatchRegex(o.Pi.Runs, re) {
			o.Failures = append(o.Failures,
				fmt.Sprintf("pi output does not match /%s/", pat))
		}
	}

	compareOutputs := func(label string, transform func(string) string) {
		for run := range min(len(o.Pig.Runs), len(o.Pi.Runs)) {
			g := transform(o.Pig.Runs[run].Output)
			p := transform(o.Pi.Runs[run].Output)
			// Apply scenario-declared regex replacements (e.g.
			// timing-dependent fields like "Took 0.1s"). Compile
			// once; failures surface as scenario failures.
			for _, rule := range a.NormalizeReplace {
				re, err := regexp.Compile(rule.Pattern)
				if err != nil {
					o.Failures = append(o.Failures,
						fmt.Sprintf("invalid normalize_replace pattern %q: %v",
							rule.Pattern, err))
					continue
				}
				g = re.ReplaceAllString(g, rule.With)
				p = re.ReplaceAllString(p, rule.With)
			}
			if g != p {
				o.Failures = append(o.Failures,
					fmt.Sprintf("run %d %s mismatch: pig=%q pi=%q", run+1, label, trunc(g, 80), trunc(p, 80)))
			}
		}
	}
	if a.OutputEqual {
		compareOutputs("output", func(s string) string { return s })
	}
	if a.OutputNormalizedEqual {
		compareOutputs("normalized output", normalize)
	}

	// Artifact assertions inspect what the command wrote rather than what it
	// printed. A command that reports success on stdout says nothing about the
	// file it produced, so a scenario covering a file-producing surface needs
	// these to exercise it at all.
	if a.ArtifactNormalizedEqual || len(a.ArtifactBothContain) > 0 || len(a.ArtifactBothNotContain) > 0 {
		for _, sr := range []*SystemResults{&o.Pig, &o.Pi} {
			for run := range sr.Runs {
				if err := sr.Runs[run].ArtifactErr; err != nil {
					o.Failures = append(o.Failures,
						fmt.Sprintf("run %d %s artifact unreadable: %v", run+1, sr.System, err))
				}
			}
		}
	}
	for _, want := range a.ArtifactBothContain {
		if !allRunsArtifactContain(o.Pig.Runs, want) {
			o.Failures = append(o.Failures, fmt.Sprintf("pig artifact missing %q", want))
		}
		if !allRunsArtifactContain(o.Pi.Runs, want) {
			o.Failures = append(o.Failures, fmt.Sprintf("pi artifact missing %q", want))
		}
	}
	for _, unwanted := range a.ArtifactBothNotContain {
		if anyRunArtifactContains(o.Pig.Runs, unwanted) {
			o.Failures = append(o.Failures, fmt.Sprintf("pig artifact unexpectedly contains %q", unwanted))
		}
		if anyRunArtifactContains(o.Pi.Runs, unwanted) {
			o.Failures = append(o.Failures, fmt.Sprintf("pi artifact unexpectedly contains %q", unwanted))
		}
	}
	if a.ArtifactNormalizedEqual {
		for run := range min(len(o.Pig.Runs), len(o.Pi.Runs)) {
			g := normalize(o.Pig.Runs[run].Artifact)
			p := normalize(o.Pi.Runs[run].Artifact)
			for _, rule := range a.NormalizeReplace {
				re, err := regexp.Compile(rule.Pattern)
				if err != nil {
					o.Failures = append(o.Failures,
						fmt.Sprintf("invalid normalize_replace pattern %q: %v", rule.Pattern, err))
					continue
				}
				g = re.ReplaceAllString(g, rule.With)
				p = re.ReplaceAllString(p, rule.With)
			}
			if g != p {
				o.Failures = append(o.Failures,
					fmt.Sprintf("run %d artifact mismatch: pig=%q pi=%q", run+1, trunc(g, 120), trunc(p, 120)))
			}
		}
	}

	// output_layout_equal: compare every pair preserving blank-line structure.
	// Strips ANSI colors but keeps empty lines, catching spacing/padding drift
	// that output_normalized_equal hides. Use for visual layout parity:
	// bg-painted box padding, compaction/branch summary spacing, tool block
	// separation, etc.
	if a.OutputLayoutEqual {
		for run := range min(len(o.Pig.Runs), len(o.Pi.Runs)) {
			g := normalizeLayout(o.Pig.Runs[run].Output)
			p := normalizeLayout(o.Pi.Runs[run].Output)
			for _, rule := range a.NormalizeReplace {
				re, err := regexp.Compile(rule.Pattern)
				if err != nil {
					o.Failures = append(o.Failures,
						fmt.Sprintf("invalid normalize_replace pattern %q: %v",
							rule.Pattern, err))
					continue
				}
				g = re.ReplaceAllString(g, rule.With)
				p = re.ReplaceAllString(p, rule.With)
			}
			if g != p {
				// Line-by-line diff for actionable failure messages.
				gLines := strings.Split(g, "\n")
				pLines := strings.Split(p, "\n")
				var diffs []string
				for i := range max(len(gLines), len(pLines)) {
					gl, pl := "", ""
					if i < len(gLines) {
						gl = gLines[i]
					}
					if i < len(pLines) {
						pl = pLines[i]
					}
					if gl != pl {
						diffs = append(diffs, fmt.Sprintf("  line %d: pig=%q pi=%q", i+1, trunc(gl, 60), trunc(pl, 60)))
					}
					if len(diffs) >= 5 {
						diffs = append(diffs, "  ...")
						break
					}
				}
				o.Failures = append(o.Failures,
					fmt.Sprintf("run %d layout mismatch (pig %d lines vs pi %d lines):\n%s",
						run+1, len(gLines), len(pLines), strings.Join(diffs, "\n")))
			}
		}
	}

	// escaped_output_equal: compare every pair's escape-preserving capture.
	if a.EscapedOutputEqual {
		for run := range min(len(o.Pig.Runs), len(o.Pi.Runs)) {
			g := o.Pig.Runs[run].Escaped
			p := o.Pi.Runs[run].Escaped
			// Apply scenario-declared regex replacements (e.g. timing-dependent
			// "Took 0.1s") to the escaped stream too: the pattern matches the
			// plain text between ANSI codes, same as the other comparators.
			for _, rule := range a.NormalizeReplace {
				re, err := regexp.Compile(rule.Pattern)
				if err != nil {
					o.Failures = append(o.Failures,
						fmt.Sprintf("invalid normalize_replace pattern %q: %v",
							rule.Pattern, err))
					continue
				}
				g = re.ReplaceAllString(g, rule.With)
				p = re.ReplaceAllString(p, rule.With)
			}
			if g != p {
				o.Failures = append(o.Failures,
					fmt.Sprintf("run %d escaped output mismatch: pig=%q pi=%q", run+1, trunc(g, 80), trunc(p, 80)))
			}
		}
	}

	// both_reach_ready (tmux-specific)
	if a.BothReachReady {
		for _, r := range o.Pig.Runs {
			if !r.ReadyOK {
				o.Failures = append(o.Failures, "pig did not reach ready pattern")
				break
			}
		}
		for _, r := range o.Pi.Runs {
			if !r.ReadyOK {
				o.Failures = append(o.Failures, "pi did not reach ready pattern")
				break
			}
		}
	}

	// runtime_ratio_max: the performance gate.
	// pig.median <= ratio * pi.median, when both have at least one run.
	// Suppressed under parallel mode (CPU contention skews the
	// measurement); use `make parity-perf` for an authoritative reading.
	if a.RuntimeRatioMax > 0 && o.Pi.MedianMs > 0 && o.Pig.MedianMs > 0 && !o.SkipPerfGate {
		ratio := float64(o.Pig.MedianMs) / float64(o.Pi.MedianMs)
		if ratio > a.RuntimeRatioMax {
			o.Failures = append(o.Failures,
				fmt.Sprintf("perf regression: pig=%dms pi=%dms ratio=%.2f max=%.2f",
					o.Pig.MedianMs, o.Pi.MedianMs, ratio, a.RuntimeRatioMax))
		}
	}

	// Structured extension-host assertions (pig-only downstream runtime QC).
	evaluateExtensionAssertions(o, a.Extensions)

	// Propagate per-run errors to failures so they surface.
	for _, r := range o.Pig.Runs {
		if r.Err != nil {
			o.Failures = append(o.Failures, fmt.Sprintf("pig run error: %v", r.Err))
		}
	}
	for _, r := range o.Pi.Runs {
		if r.Err != nil {
			o.Failures = append(o.Failures, fmt.Sprintf("pi run error: %v", r.Err))
		}
	}

	o.Pig.AllOK = allRunsExitOK(o.Pig.Runs)
	o.Pi.AllOK = allRunsExitOK(o.Pi.Runs)
}

func allRunsContain(runs []Result, want string) bool {
	for _, r := range runs {
		if !strings.Contains(r.Output, want) {
			return false
		}
	}
	return len(runs) > 0
}

func anyRunContains(runs []Result, want string) bool {
	for _, r := range runs {
		if strings.Contains(r.Output, want) {
			return true
		}
	}
	return false
}

func allRunsArtifactContain(runs []Result, want string) bool {
	for _, r := range runs {
		if !strings.Contains(r.Artifact, want) {
			return false
		}
	}
	return len(runs) > 0
}

func anyRunArtifactContains(runs []Result, want string) bool {
	for _, r := range runs {
		if strings.Contains(r.Artifact, want) {
			return true
		}
	}
	return false
}

func allRunsMatchRegex(runs []Result, re *regexp.Regexp) bool {
	for _, r := range runs {
		if !re.MatchString(r.Output) {
			return false
		}
	}
	return len(runs) > 0
}

func allRunsExitOK(runs []Result) bool {
	for _, r := range runs {
		if r.Err != nil {
			return false
		}
	}
	return true
}

func medianRuntime(runs []Result) int64 {
	if len(runs) == 0 {
		return 0
	}
	ms := make([]int64, 0, len(runs))
	for _, r := range runs {
		ms = append(ms, r.RuntimeMs)
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })
	return ms[len(ms)/2]
}

// normalize strips ANSI escape sequences and collapses whitespace for
// output_normalized_equal comparisons.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`)

func normalize(s string) string {
	s = ansiRE.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r", "")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// normalizeLayout strips ANSI escapes and trailing whitespace but preserves
// blank lines and line count. This catches layout/spacing differences that
// normalize() hides (e.g. missing padding rows in bg-painted boxes).
func normalizeLayout(s string) string {
	s = ansiRE.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r", "")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	// Trim leading/trailing empty lines only: preserve internal blank lines.
	start, end := 0, len(lines)
	for start < end && lines[start] == "" {
		start++
	}
	for end > start && lines[end-1] == "" {
		end--
	}
	return strings.Join(lines[start:end], "\n")
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// versionTokens lets asymmetric expectations name the pinned versions without
// copying literals that go stale: {{VERSION}} is PiG's composite version and
// {{UPSTREAM_VERSION}} is the Pi pin.
var versionTokens = strings.NewReplacer(
	"{{VERSION}}", coding.Version,
	"{{UPSTREAM_VERSION}}", coding.UpstreamVersion,
)

// expandVersionTokens joins expectation lists and expands their version tokens.
func expandVersionTokens(lists ...[]string) []string {
	var out []string
	for _, list := range lists {
		for _, want := range list {
			out = append(out, versionTokens.Replace(want))
		}
	}
	return out
}
