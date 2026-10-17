package tui

import (
	"strings"
	"testing"
)

// Pi 1.1.0 packages/coding-agent/src/modes/interactive/components/bash-execution.ts:35 constructor(command, ui, excludeFromContext = false,
// outputPad = 1): the fourth argument is the padding of the header, output and status rows from the first render (141, 149, 159, 205).
// mutation-checked: NewBashExecutionComponent ignoring outputPad (keeping 1) fails this test.
func TestBashExecutionConstructorTakesPisOutputPad(t *testing.T) {
	for _, pad := range []int{0, 1, 3} {
		b := NewBashExecutionComponent("make test", nil, false, pad)
		var header string
		for _, line := range b.Render(60) {
			if plain := stripANSI(line); strings.Contains(plain, "$ make test") {
				header = plain
			}
		}
		if header == "" {
			t.Fatalf("outputPad %d: no command header in %q", pad, b.Render(60))
		}
		if got := len(header) - len(strings.TrimLeft(header, " ")); got != pad {
			t.Errorf("outputPad %d: header indent = %d", pad, got)
		}
	}
	if got := NewBashExecutionComponent("x", nil, false, 1).outputPad; got != 1 {
		t.Errorf("the constructor takes Pi outputPad as given, got %d", got)
	}
}

// Pi bash-execution.ts:101-118 setComplete(exitCode, cancelled, truncationResult?, fullOutputPath?): the status is "cancelled" when cancelled, else
// "error" for an exit code other than 0 (undefined and null stay "complete"), else "complete"; the "(cancelled)" and "(exit N)" rows follow
// (187-191); the truncation row needs truncationResult.truncated and a fullOutputPath (200-203).
// mutation-checked: finishBashExecution mapping a non-zero exit to complete, or ignoring TruncationResult.Truncated, fails this test.
func TestBashExecutionSetCompleteMatchesPiStatusAndTruncation(t *testing.T) {
	zero, seven := 0, 7
	cases := []struct {
		name       string
		exit       *int
		cancelled  bool
		truncation *TruncationResult
		path       string
		want, deny []string
	}{
		{"zero exit completes", &zero, false, nil, "", nil, []string{"(exit", "(cancelled)", "Output truncated"}},
		{"undefined exit completes", nil, false, nil, "", nil, []string{"(exit", "(cancelled)"}},
		{"non-zero exit is an error", &seven, false, nil, "", []string{"(exit 7)"}, []string{"(cancelled)"}},
		{"cancelled wins over a non-zero exit", &seven, true, nil, "", []string{"(cancelled)"}, []string{"(exit 7)"}},
		{"truncated with a path adds the full-output row", &zero, false, &TruncationResult{Truncated: true}, "/tmp/full.log", []string{"Output truncated. Full output: /tmp/full.log"}, nil},
		{"truncated without a path adds no row", &zero, false, &TruncationResult{Truncated: true}, "", nil, []string{"Output truncated"}},
		{"untruncated result with a path adds no row", &zero, false, &TruncationResult{Truncated: false}, "/tmp/full.log", nil, []string{"Output truncated"}},
	}
	for _, c := range cases {
		b := NewBashExecutionComponent("cmd", nil, false, 1)
		b.AppendOutput("out")
		b.SetComplete(c.exit, c.cancelled, c.truncation, c.path)
		var plain strings.Builder
		for _, line := range b.Render(80) {
			plain.WriteString(stripANSI(line) + "\n")
		}
		for _, want := range c.want {
			if !strings.Contains(plain.String(), want) {
				t.Errorf("%s: missing %q in\n%s", c.name, want, plain.String())
			}
		}
		for _, deny := range c.deny {
			if strings.Contains(plain.String(), deny) {
				t.Errorf("%s: unexpected %q in\n%s", c.name, deny, plain.String())
			}
		}
	}
}
