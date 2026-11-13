package codingagent

import (
	"slices"
	"testing"
)

// Matches reads the keys rebuild resolved: for every action the merged table defines they are keysOf's, and an id no definition names has none.
func TestMatchesReadsTheKeysOfTheMergedTable(t *testing.T) {
	km := NewKeybindingsManager(t.TempDir())
	check := func() {
		t.Helper()
		for _, action := range append(slices.Clone(km.ordered), "tui.editor.cursorUp", "tui.input.submit", "app.exit", "no.such.action") {
			if got, want := km.matchKeys[action], km.keysOf(action); !slices.Equal(got, want) {
				t.Fatalf("%s: Matches reads %q, keysOf is %q", action, got, want)
			}
		}
	}
	check()
	km.SetUserBindings(map[string][]KeyID{"app.exit": {"ctrl+q"}, "no.such.action": {"ctrl+y"}})
	check()
	if !km.Matches("\x11", "app.exit") || km.Matches("\x04", "app.exit") || km.Matches("\x19", "no.such.action") {
		t.Fatal("Matches does not follow the user binding of app.exit, or matches an id no definition names")
	}
}

// One typed character is classified against the app actions before the editor inserts it.
func BenchmarkClassifyTypedKey(b *testing.B) {
	km := NewKeybindingsManager(b.TempDir())
	b.ReportAllocs()
	for b.Loop() {
		if classifyKeyWithBindings("a", km) != actionInsert {
			b.Fatal("a typed letter is not an insert")
		}
	}
}
