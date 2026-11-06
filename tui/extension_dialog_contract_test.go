package tui

import (
	"reflect"
	"testing"
)

// runDialogProbes drives probes through Pig's select and input dialogs the way the oracle test does and returns the observed states and frames.
func runDialogProbes(t *testing.T, probes []dialogProbe) []dialogProbeResult {
	t.Helper()
	previousBindings, previousTheme, previousCaps := GetKeybindings(), ActiveTheme(), GetCapabilities()
	t.Cleanup(func() {
		SetKeybindings(previousBindings)
		SetCapabilities(previousCaps)
		storeActiveTheme(previousTheme)
	})
	results := make([]dialogProbeResult, len(probes))
	for i, probe := range probes {
		SetCapabilities(TerminalCapabilities{TrueColor: probe.TrueColor})
		SetTheme(probe.Theme)
		definitions := TUIKeybindingDefinitionsFor(HostKeybindingPlatform())
		definitions["app.tools.expand"] = TUIKeybindingDef{DefaultKeys: []string{"ctrl+o"}}
		restoreKeybindingsAfterTest(t)
		SetKeybindings(NewKeybindingsManager(definitions, probe.Bindings))
		toggles := 0
		selector := NewExtensionSelectorComponent("Rigidity probe", probe.Options, nil, nil, ExtensionSelectorOptions{OnToggleToolsExpanded: func() { toggles++ }})
		submitted := ""
		field := NewExtensionInputComponent("Rigidity probe", "", func(value string) { submitted = value }, nil)
		field.SetFocused(true)
		render, handle := selector.Render, selector.HandleInput
		done, cancelled, value := selector.Done, selector.Cancelled, selector.SelectedValue
		if probe.Kind == "input" {
			render, handle = field.Render, field.HandleInput
			done, cancelled, value = field.Done, field.Cancelled, func() string { return submitted }
		}
		renderContent := func() []string {
			rows := render(probe.Width)
			return rows[2 : len(rows)-2]
		}
		results[i].Frames = [][]string{renderContent()}
		for _, key := range probe.Keys {
			handle(key)
			state := dialogProbeState{Done: done(), Cancelled: cancelled(), Toggles: toggles}
			if state.Done && !state.Cancelled {
				state.Value = value()
			}
			results[i].States = append(results[i].States, state)
			if state.Done {
				break
			}
			results[i].Frames = append(results[i].Frames, renderContent())
		}
	}
	return results
}

// Each pair of neighbouring actions in extension-selector.ts handleInput (expand, up, down, confirm, cancel) and the literal keys (k, j, LF) is bound to
// one key in turn, and the earlier branch must win in Pig as it does in Pi. Boundary lists (empty, one option, first and last row) run through the same
// keys, because the clamp is where the arrow and the literal keys meet the ends.
func TestExtensionSelectorBranchOrderAndBoundariesMatchPi(t *testing.T) {
	three := []string{"first", "second", "third"}
	ctrlO, ctrlX := "\x0f", "\x18"
	cases := []dialogProbe{
		// expand before up, down, confirm and cancel
		{Kind: "select", Options: three, Bindings: map[string][]string{KBSelectUp: {"ctrl+o"}}, Keys: []string{"j", "j", ctrlO, "\r"}},
		{Kind: "select", Options: three, Bindings: map[string][]string{KBSelectDown: {"ctrl+o"}}, Keys: []string{ctrlO, ctrlO, "\r"}},
		{Kind: "select", Options: three, Bindings: map[string][]string{KBSelectConfirm: {"ctrl+o"}}, Keys: []string{ctrlO, "\r"}},
		{Kind: "select", Options: three, Bindings: map[string][]string{KBSelectCancel: {"ctrl+o"}}, Keys: []string{ctrlO, "\x1b"}},
		// up before down, confirm and cancel
		{Kind: "select", Options: three, Bindings: map[string][]string{KBSelectUp: {"ctrl+x"}, KBSelectDown: {"ctrl+x"}}, Keys: []string{"j", "j", ctrlX, ctrlX, "\r"}},
		{Kind: "select", Options: three, Bindings: map[string][]string{KBSelectUp: {"ctrl+x"}, KBSelectConfirm: {"ctrl+x"}}, Keys: []string{"j", ctrlX, ctrlX}},
		{Kind: "select", Options: three, Bindings: map[string][]string{KBSelectUp: {"ctrl+x"}, KBSelectCancel: {"ctrl+x"}}, Keys: []string{"j", ctrlX, ctrlX}},
		// down before confirm and cancel
		{Kind: "select", Options: three, Bindings: map[string][]string{KBSelectDown: {"ctrl+x"}, KBSelectConfirm: {"ctrl+x"}}, Keys: []string{ctrlX, ctrlX, ctrlX, ctrlX}},
		{Kind: "select", Options: three, Bindings: map[string][]string{KBSelectDown: {"ctrl+x"}, KBSelectCancel: {"ctrl+x"}}, Keys: []string{ctrlX, ctrlX, ctrlX}},
		// confirm before cancel, and the literal LF before cancel
		{Kind: "select", Options: three, Bindings: map[string][]string{KBSelectConfirm: {"ctrl+x"}, KBSelectCancel: {"ctrl+x"}}, Keys: []string{"j", ctrlX}},
		{Kind: "select", Options: three, Bindings: map[string][]string{KBSelectCancel: {"ctrl+j"}}, Keys: []string{"j", "\n"}},
		{Kind: "select", Options: three, Bindings: map[string][]string{KBSelectConfirm: {}, KBSelectCancel: {"ctrl+j"}}, Keys: []string{"\n"}},
		// the literal j and k move even when the bound actions are other keys, and win over a cancel bound to them
		{Kind: "select", Options: three, Bindings: map[string][]string{KBSelectUp: {"ctrl+p"}, KBSelectDown: {"ctrl+n"}}, Keys: []string{"j", "j", "k", "\r"}},
		{Kind: "select", Options: three, Bindings: map[string][]string{KBSelectCancel: {"j"}}, Keys: []string{"j", "\r"}},
		{Kind: "select", Options: three, Bindings: map[string][]string{KBSelectCancel: {"k"}}, Keys: []string{"j", "k", "\r"}},
		{Kind: "select", Options: three, Bindings: map[string][]string{KBSelectUp: {}, KBSelectDown: {}}, Keys: []string{"\x1b[B", "\x1b[A", "j", "\r"}},
		// first and last row do not wrap, whichever key moves
		{Kind: "select", Options: three, Keys: []string{"k", "\x1b[A", "j", "j", "j", "\x1b[B", "\x1b[B", "k", "\r"}},
		{Kind: "select", Options: three, Keys: []string{"\x1b[B", "\x1b[B", "\x1b[B", "\x1b[A", "\x1b[A", "\x1b[A", "\x1b[A", "\r"}},
		// one option and no option
		{Kind: "select", Options: []string{"only"}, Keys: []string{"j", "k", "\x1b[B", "\x1b[A", "\r"}},
		{Kind: "select", Options: []string{}, Keys: []string{"j", "\x1b[B", "k", "\x1b[A", "\r", "\n", ctrlO, "\x1b"}},
		{Kind: "select", Options: []string{}, Bindings: map[string][]string{KBSelectCancel: {"ctrl+x"}}, Keys: []string{"\x1b", ctrlX}},
		// an empty-string option is a row that cannot be confirmed, and the clamp still moves over it
		{Kind: "select", Options: []string{"", ""}, Keys: []string{"\r", "j", "\n", "k", "\x1b"}},
		{Kind: "select", Options: []string{"a", ""}, Keys: []string{"j", "\r", "k", "\r"}},
		// keys that no branch owns leave the selector untouched
		{Kind: "select", Options: three, Keys: []string{"x", "\t", "\x1b[5~", "\x1b[6~", "\x1b[H", "\x1b[F", "\x7f", "hello", "\r"}},
		// input: confirm before cancel, the literal LF before cancel, and cancel before the delegated Input
		{Kind: "input", Bindings: map[string][]string{KBSelectConfirm: {"ctrl+x"}, KBSelectCancel: {"ctrl+x"}}, Keys: []string{"abc", ctrlX}},
		{Kind: "input", Bindings: map[string][]string{KBSelectCancel: {"ctrl+j"}}, Keys: []string{"abc", "\n"}},
		{Kind: "input", Bindings: map[string][]string{KBSelectConfirm: {}, KBSelectCancel: {"ctrl+j"}}, Keys: []string{"abc", "\n"}},
		{Kind: "input", Bindings: map[string][]string{KBSelectCancel: {"ctrl+a"}}, Keys: []string{"abc", "\x01"}},
		{Kind: "input", Bindings: map[string][]string{KBSelectCancel: {"ctrl+q"}}, Keys: []string{"abc", "\x11", "x"}},
		{Kind: "input", Bindings: map[string][]string{KBInputSubmit: {"ctrl+s"}, KBSelectConfirm: {"ctrl+y"}}, Keys: []string{"abc", "\x13", "\x19"}},
		{Kind: "input", Keys: []string{"a", "\x7f", "\x7f", "\r"}},
		{Kind: "input", Keys: []string{"\r"}},
		{Kind: "input", Keys: []string{"\x1b"}},
		{Kind: "input", Keys: []string{"\x03"}},
	}
	var probes []dialogProbe
	for _, theme := range []string{"dark", "light"} {
		for _, probe := range cases {
			probe.Theme, probe.Width, probe.TrueColor = theme, 60, true
			probes = append(probes, probe)
		}
	}
	expected := piDialogOracle(t, probes)
	got := runDialogProbes(t, probes)
	for i, probe := range probes {
		if !reflect.DeepEqual(got[i].States, expected[i].States) {
			t.Errorf("probe %d %+v: states = %+v; Pi = %+v", i, probe, got[i].States, expected[i].States)
		}
		if !reflect.DeepEqual(got[i].Frames, expected[i].Frames) {
			t.Errorf("probe %d %+v: frames = %q; Pi = %q", i, probe, got[i].Frames, expected[i].Frames)
		}
	}
}

// extension-selector.ts handleInput calls onSelectCallback(option) for the highlighted row and onCancelCallback() on cancel; extension-input.ts calls
// onSubmitCallback(value) and onCancelCallback(). The callbacks are the dialog's only outward effect, so each fires exactly once for the key that completes
// the dialog, with the option or the text, and never for a key that does not complete it.
func TestExtensionDialogCallbacksFireOncePerCompletingKey(t *testing.T) {
	restoreKeybindingsAfterTest(t)
	definitions := TUIKeybindingDefinitionsFor(HostKeybindingPlatform())
	definitions["app.tools.expand"] = TUIKeybindingDef{DefaultKeys: []string{"ctrl+o"}}
	SetKeybindings(NewKeybindingsManager(definitions, nil))
	t.Run("selector select", func(t *testing.T) {
		var selected []string
		cancels := 0
		s := NewExtensionSelectorComponent("Pick", []string{"a", "b", "c"}, func(option string) { selected = append(selected, option) }, func() { cancels++ })
		for _, key := range []string{"j", "x", "k", "j", "\x1b[B"} {
			s.HandleInput(key)
		}
		if len(selected) != 0 || cancels != 0 {
			t.Fatalf("navigation completed the dialog: selected=%q cancels=%d", selected, cancels)
		}
		s.HandleInput("\r")
		if !reflect.DeepEqual(selected, []string{"c"}) || cancels != 0 {
			t.Fatalf("selected=%q cancels=%d, want [c] and 0", selected, cancels)
		}
	})
	t.Run("selector LF", func(t *testing.T) {
		var selected []string
		s := NewExtensionSelectorComponent("Pick", []string{"a", "b"}, func(option string) { selected = append(selected, option) }, nil)
		s.HandleInput("j")
		s.HandleInput("\n")
		if !reflect.DeepEqual(selected, []string{"b"}) {
			t.Fatalf("selected=%q, want [b]", selected)
		}
	})
	t.Run("selector cancel", func(t *testing.T) {
		var selected []string
		cancels := 0
		s := NewExtensionSelectorComponent("Pick", []string{"a", "b"}, func(option string) { selected = append(selected, option) }, func() { cancels++ })
		s.HandleInput("\x1b")
		if len(selected) != 0 || cancels != 1 || !s.Cancelled() || s.SelectedIndex() != -1 {
			t.Fatalf("selected=%q cancels=%d cancelled=%v index=%d", selected, cancels, s.Cancelled(), s.SelectedIndex())
		}
	})
	t.Run("selector with nothing to pick", func(t *testing.T) {
		for _, options := range [][]string{{}, {""}} {
			selected, cancels := 0, 0
			s := NewExtensionSelectorComponent("Pick", options, func(string) { selected++ }, func() { cancels++ })
			s.HandleInput("\r")
			s.HandleInput("\n")
			if selected != 0 || cancels != 0 || s.Done() {
				t.Fatalf("options %q: selected=%d cancels=%d done=%v", options, selected, cancels, s.Done())
			}
		}
	})
	t.Run("selector expand", func(t *testing.T) {
		toggles := 0
		s := NewExtensionSelectorComponent("Pick", []string{"a"}, nil, nil, ExtensionSelectorOptions{OnToggleToolsExpanded: func() { toggles++ }})
		s.HandleInput("\x0f")
		s.HandleInput("\x0f")
		if toggles != 2 || s.Done() {
			t.Fatalf("toggles=%d done=%v, want 2 and false", toggles, s.Done())
		}
	})
	t.Run("input submit", func(t *testing.T) {
		var submitted []string
		cancels := 0
		in := NewExtensionInputComponent("Name", "", func(value string) { submitted = append(submitted, value) }, func() { cancels++ })
		for _, key := range []string{"a", "b", "\x7f", "c"} {
			in.HandleInput(key)
		}
		if len(submitted) != 0 || cancels != 0 {
			t.Fatalf("editing completed the dialog: submitted=%q cancels=%d", submitted, cancels)
		}
		in.HandleInput("\r")
		if !reflect.DeepEqual(submitted, []string{"ac"}) || cancels != 0 {
			t.Fatalf("submitted=%q cancels=%d, want [ac] and 0", submitted, cancels)
		}
	})
	t.Run("input LF", func(t *testing.T) {
		var submitted []string
		in := NewExtensionInputComponent("Name", "", func(value string) { submitted = append(submitted, value) }, nil, ExtensionInputOptions{InitialValue: "seed"})
		in.HandleInput("\n")
		if !reflect.DeepEqual(submitted, []string{"seed"}) {
			t.Fatalf("submitted=%q, want [seed]", submitted)
		}
	})
	t.Run("input cancel", func(t *testing.T) {
		var submitted []string
		cancels := 0
		in := NewExtensionInputComponent("Name", "", func(value string) { submitted = append(submitted, value) }, func() { cancels++ })
		in.HandleInput("x")
		in.HandleInput("\x1b")
		if len(submitted) != 0 || cancels != 1 || !in.Cancelled() {
			t.Fatalf("submitted=%q cancels=%d cancelled=%v", submitted, cancels, in.Cancelled())
		}
	})
}
