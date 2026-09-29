//go:build parity

package runner

import (
	"strings"
	"testing"
)

func TestLineDiff_Equal(t *testing.T) {
	if d := LineDiff("same\nlines\n", "same\nlines\n"); d != "" {
		t.Errorf("expected empty diff for equal inputs, got %q", d)
	}
}

func TestLineDiff_FirstDiffLine(t *testing.T) {
	pig := "a\nb\nPIG\nd\n"
	pi := "a\nb\nPI\nd\n"
	d := LineDiff(pig, pi)
	if !strings.Contains(d, "first diff at line 3") {
		t.Errorf("expected 'first diff at line 3', got:\n%s", d)
	}
	if !strings.Contains(d, "PIG") || !strings.Contains(d, "PI") {
		t.Errorf("expected both sides shown, got:\n%s", d)
	}
}

func TestLineDiff_LengthMismatch(t *testing.T) {
	d := LineDiff("a\nb\n", "a\nb\nextra\n")
	if !strings.Contains(d, "first diff at line 3") {
		t.Errorf("expected diff at line 3 for length mismatch, got:\n%s", d)
	}
	if !strings.Contains(d, "<EOF>") {
		t.Errorf("expected <EOF> marker on shorter side, got:\n%s", d)
	}
}

func TestLineDiff_DifferingCount(t *testing.T) {
	pig := "a\nb\nc\nd\n"
	pi := "a\nX\nc\nY\n"
	d := LineDiff(pig, pi)
	if !strings.Contains(d, "of 2 differing lines") {
		t.Errorf("expected differing count = 2, got:\n%s", d)
	}
}

func TestSiblingScenarios_NoOverlap(t *testing.T) {
	a := &Scenario{Name: "a", Covers: []string{"pkg/x.ts"}}
	b := &Scenario{Name: "b", Covers: []string{"pkg/y.ts"}}
	c := &Scenario{Name: "c", Covers: []string{"pkg/z.ts"}}
	got := SiblingScenarios(a, []*Scenario{a, b, c})
	if len(got) != 0 {
		t.Errorf("expected no siblings, got %v", got)
	}
}

func TestSiblingScenarios_Overlap(t *testing.T) {
	a := &Scenario{Name: "a", Covers: []string{"pkg/x.ts", "pkg/y.ts"}}
	b := &Scenario{Name: "b", Covers: []string{"pkg/y.ts"}}     // shares y
	c := &Scenario{Name: "c", Covers: []string{"pkg/x.ts"}}     // shares x
	d := &Scenario{Name: "d", Covers: []string{"pkg/other.ts"}} // no share
	got := SiblingScenarios(a, []*Scenario{a, b, c, d})
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Errorf("expected [b c], got %v", got)
	}
}

func TestSiblingScenarios_SelfExcluded(t *testing.T) {
	a := &Scenario{Name: "a", Covers: []string{"pkg/x.ts"}}
	got := SiblingScenarios(a, []*Scenario{a})
	if len(got) != 0 {
		t.Errorf("scenario must not list itself as a sibling, got %v", got)
	}
}

func TestSiblingScenarios_EmptyCovers(t *testing.T) {
	a := &Scenario{Name: "a", Covers: nil}
	b := &Scenario{Name: "b", Covers: []string{"pkg/x.ts"}}
	got := SiblingScenarios(a, []*Scenario{a, b})
	if len(got) != 0 {
		t.Errorf("scenario with no covers must report no siblings, got %v", got)
	}
}

func TestFaithfulnessReminder_NonEmpty(t *testing.T) {
	if !strings.Contains(FaithfulnessReminder, "Scenarios serve faithfulness") {
		t.Errorf("FaithfulnessReminder must carry the axis statement")
	}
	if !strings.Contains(FaithfulnessReminder, "lint-known-gap") {
		t.Errorf("FaithfulnessReminder must warn against lint-known-gap as a workaround")
	}
}
