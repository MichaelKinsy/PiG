//go:build parity

package runner

import (
	"fmt"
	"sort"
	"strings"
)

// FaithfulnessReminder is appended verbatim to the failure log of every
// scenario that failed at least one assertion. It exists because agents
// repeatedly rationalize away from fixing real pig bugs by treating
// failing scenarios as constraints on pig behavior. They are not.
// Scenarios are claims about upstream behavior. If they disagree with
// upstream, the scenario is wrong, not the fix.
const FaithfulnessReminder = `
─── faithfulness reminder ───────────────────────────────────────────
Scenarios serve faithfulness. Faithfulness does not serve scenarios.

If a pig observable diverges from real upstream behavior, the pig
code is wrong: even if some existing scenario currently asserts the
divergent output.

Do NOT:
  • weaken a comparator (output_equal → output_normalized_equal → substring)
  • add a # lint-known-gap: for a difference you have diagnosed as a pig bug
  • narrow a crop window to dodge the diff
  • revert a faithfulness fix to make a scenario pass

DO:
  • re-probe upstream pi for the asserted surface in this loop
  • update the scenario's evidence comment, crop, and comparator
  • if the fix's blast radius exceeds the loop, stop and tell the user
─────────────────────────────────────────────────────────────────────`

// LineDiff produces a readable line-by-line diff of two strings.
// It is not minimal in the LCS sense; it lines up by index and shows
// the first chunk where outputs diverge. For agents reading test logs
// this is much more useful than the 80-char truncated form.
//
// Format (when the strings differ):
//
//	first diff at line N
//	─── pig ───
//	  N-1: <context line>
//	! N:   <pig line>
//	  N+1: <context line>
//	─── pi ───
//	  N-1: <context line>
//	! N:   <pi line>
//	  N+1: <context line>
//	(M more differing lines)
func LineDiff(pig, pi string) string {
	if pig == pi {
		return ""
	}
	gl := strings.Split(pig, "\n")
	pl := strings.Split(pi, "\n")

	// Find first differing line.
	first := -1
	maxIdx := len(gl)
	if len(pl) > maxIdx {
		maxIdx = len(pl)
	}
	for i := 0; i < maxIdx; i++ {
		var g, p string
		if i < len(gl) {
			g = gl[i]
		}
		if i < len(pl) {
			p = pl[i]
		}
		if g != p {
			first = i
			break
		}
	}
	if first < 0 {
		return ""
	}

	// Count total differing lines for the summary.
	differing := 0
	for i := 0; i < maxIdx; i++ {
		var g, p string
		if i < len(gl) {
			g = gl[i]
		}
		if i < len(pl) {
			p = pl[i]
		}
		if g != p {
			differing++
		}
	}

	const ctx = 2
	const window = 8
	lo := max(0, first-ctx)
	hi := min(maxIdx, first+window)

	var b strings.Builder
	fmt.Fprintf(&b, "first diff at line %d (of %d differing lines)\n", first+1, differing)

	b.WriteString("─── pig ───\n")
	writeWindow(&b, gl, lo, hi, first)
	b.WriteString("─── pi ───\n")
	writeWindow(&b, pl, lo, hi, first)
	return b.String()
}

func writeWindow(b *strings.Builder, lines []string, lo, hi, mark int) {
	for i := lo; i < hi; i++ {
		var line string
		if i < len(lines) {
			line = lines[i]
		} else {
			line = "<EOF>"
		}
		marker := "  "
		if i == mark {
			marker = "! "
		}
		fmt.Fprintf(b, "%s%4d: %s\n", marker, i+1, line)
	}
}

// SiblingScenarios returns the names of every other scenario whose
// `covers` set intersects sc.Covers. Sorted alphabetically. Used by
// the test logger so when one scenario fails, the agent immediately
// sees which other scenarios assert behavior of the same upstream files
// : those scenarios may also be wrong and require re-probing.
func SiblingScenarios(sc *Scenario, all []*Scenario) []string {
	if len(sc.Covers) == 0 || len(all) == 0 {
		return nil
	}
	want := make(map[string]struct{}, len(sc.Covers))
	for _, c := range sc.Covers {
		want[c] = struct{}{}
	}
	seen := make(map[string]struct{})
	for _, other := range all {
		if other == nil || other.Name == sc.Name {
			continue
		}
		for _, c := range other.Covers {
			if _, ok := want[c]; ok {
				seen[other.Name] = struct{}{}
				break
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
