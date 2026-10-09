package tui

import (
	"strings"
	"testing"
)

// Pi 1.1.0 packages/coding-agent/src/modes/interactive/components/bash-execution.ts:73 setOutputPad(outputPad) stores the
// padding and runs updateDisplay, which builds the command header (line 141), the expanded output (149), the collapsed preview
// through truncateToVisualLines(..., this.outputPad) (159) and the status rows (205) with it.
func TestBashExecutionSetOutputPadMatchesPi(t *testing.T) {
	exit := 1
	build := func(expanded bool) *BashExecutionComponent {
		b := NewBashExecutionComponent("make test", nil, false, 1)
		b.AppendOutput("first\nsecond")
		b.SetComplete(&exit, false, nil, "")
		b.SetExpanded(expanded)
		return b
	}
	indent := func(b *BashExecutionComponent, needle string) int {
		t.Helper()
		for _, line := range b.Render(60) {
			if plain := stripANSI(line); strings.Contains(plain, needle) {
				return len(plain) - len(strings.TrimLeft(plain, " "))
			}
		}
		t.Fatalf("no row with %q", needle)
		return -1
	}
	for _, expanded := range []bool{false, true} {
		b := build(expanded)
		for _, needle := range []string{"$ make test", "second", "(exit 1)"} {
			if got := indent(b, needle); got != 1 {
				t.Fatalf("expanded=%v default: %q indent = %d, want 1", expanded, needle, got)
			}
		}
		b.SetOutputPad(0)
		for _, needle := range []string{"$ make test", "second", "(exit 1)"} {
			if got := indent(b, needle); got != 0 {
				t.Fatalf("expanded=%v SetOutputPad(0): %q indent = %d", expanded, needle, got)
			}
		}
		b.SetOutputPad(1)
		if got := indent(b, "second"); got != 1 {
			t.Fatalf("expanded=%v SetOutputPad(1): output indent = %d", expanded, got)
		}
	}
}

// bash-execution.ts constructor(command, ui, excludeFromContext = false, outputPad = 1): the outputPad argument pads the header, output
// and status rows from the first render, and ui is the TUI the running Loader asks for a render (loader.ts updateDisplay).
func TestBashExecutionConstructorTakesTheUIAndTheOutputPad(t *testing.T) {
	exit := 1
	for _, pad := range []int{0, 1, 3} {
		b := NewBashExecutionComponent("make test", nil, false, pad)
		b.AppendOutput("first\nsecond")
		b.SetComplete(&exit, false, nil, "")
		b.SetExpanded(true)
		for _, needle := range []string{"$ make test", "second", "(exit 1)"} {
			found := false
			for _, line := range b.Render(60) {
				if plain := stripANSI(line); strings.Contains(plain, needle) {
					found = true
					if got := len(plain) - len(strings.TrimLeft(plain, " ")); got != pad {
						t.Fatalf("outputPad %d: %q indent = %d", pad, needle, got)
					}
				}
			}
			if !found {
				t.Fatalf("outputPad %d: no row with %q", pad, needle)
			}
		}
	}
	ui := &renderCountingTUI{}
	b := NewBashExecutionComponent("sleep 5", ui, true, 1)
	if got := ui.requests.Load(); got < 1 {
		t.Fatalf("the running loader asked the constructor's ui for %d renders", got)
	}
	before := ui.requests.Load()
	b.Loader().SetMessage("Still running")
	if ui.requests.Load() <= before {
		t.Fatal("a loader change did not reach the constructor's ui")
	}
}
