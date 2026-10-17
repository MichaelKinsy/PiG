package tui

import (
	"slices"
	"testing"
)

// BenchmarkKeystrokeBindingMatch is what one keystroke costs the key path: the input is matched against every binding of the default keybindings, as the focused editor and the interactive mode's handlers do. allocs/op is the per-keystroke gate.
func BenchmarkKeystrokeBindingMatch(b *testing.B) {
	manager := NewTUIKeybindingsManager(nil)
	var actions []string
	for action := range TUIKeybindings {
		actions = append(actions, action)
	}
	slices.Sort(actions)
	// Keystrokes differ from one to the next, so the input is parsed afresh each time, as it is when typing.
	inputs := map[string][]string{
		"printable":      {"a", "b", "c", "d", "e", "f", "g", "h"},
		"enter":          {"\r", "\n", "\x1bOM"},
		"ctrl-arrow-csi": {"\x1b[1;5C", "\x1b[1;5D", "\x1b[1;3A", "\x1b[1;5B"},
		"kitty-csi-u":    {"\x1b[97;5u", "\x1b[98;5u", "\x1b[99;5u", "\x1b[100;5u"},
	}
	for _, name := range []string{"printable", "enter", "ctrl-arrow-csi", "kitty-csi-u"} {
		keys := inputs[name]
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				data := keys[i%len(keys)]
				i++
				for _, action := range actions {
					manager.Matches(data, action)
				}
			}
		})
	}
}
