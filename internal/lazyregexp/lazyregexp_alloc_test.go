package lazyregexp

import (
	"strings"
	"testing"
)

var newSink *Regexp

// Creating a pattern costs one allocation (plus the registry's amortized growth): every linked package creates its patterns at process start.
func TestNewCostsOneAllocation(t *testing.T) {
	if allocs := testing.AllocsPerRun(200, func() { newSink = New(`a+b`) }); allocs >= 2 {
		t.Fatalf("New = %v allocations, want 1", allocs)
	}
	if !newSink.MatchString("aab") || newSink.MatchString("b") {
		t.Fatal("the pattern does not match as a+b")
	}
	if first, second := newSink.Regexp(), newSink.Regexp(); first != second {
		t.Fatal("each use compiled the pattern again")
	}
}

// Every use of an invalid pattern panics with the same value, as sync.OnceValue repeats it.
func TestInvalidPatternPanicsOnEveryUse(t *testing.T) {
	r := New("a(" + strings.TrimSpace(" b"))
	var panics []any
	for range 3 {
		func() {
			defer func() { panics = append(panics, recover()) }()
			r.MatchString("ab")
		}()
	}
	if panics[0] == nil || panics[1] != panics[0] || panics[2] != panics[0] {
		t.Fatalf("panics = %v, want one value three times", panics)
	}
}
