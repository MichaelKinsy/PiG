package codingagent

// pi: packages/coding-agent/src/modes/interactive/components/extension-editor.ts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// extensionEditorProbe is an ExtensionEditorComponent with the state its callbacks report (extension-editor.ts constructor onSubmit/onCancel).
type extensionEditorProbe struct {
	*ExtensionEditorComponent
	done, cancelled bool
	value           string
	ui              *forcedRenderCounter
}

// forcedRenderCounter counts the repaints a component forces (`requestRender(true)`) on its ui.
type forcedRenderCounter struct {
	tui.TUI
	forced int
}

func (c *forcedRenderCounter) RequestRender(force ...bool) {
	if len(force) > 0 && force[0] {
		c.forced++
	}
	c.TUI.RequestRender(force...)
}

func newExtensionEditorProbe(t *testing.T, title, prefill string, bindings map[string][]string, options *ExtensionEditorOptions) *extensionEditorProbe {
	t.Helper()
	previous := tui.GetKeybindings()
	t.Cleanup(func() { tui.SetKeybindings(previous) })
	p := &extensionEditorProbe{}
	p.ui = &forcedRenderCounter{TUI: tui.NewWithOutput(io.Discard, 80, 40)}
	p.ExtensionEditorComponent = NewExtensionEditorComponent(p.ui, NewKeybindingsManagerFromBindings(bindings, ""), title, prefill,
		func(text string) { p.done, p.value = true, text },
		func() { p.done, p.cancelled = true, true },
		options, "ed-command")
	return p
}

// extension-editor.ts:25-30 and :52-91: border, spacer, title, spacer, editor, spacer, hint, spacer, border; a description adds a spacer and a Text after the title.
// Typing shows in the next render.
func TestExtensionEditorComponentChildrenFollowUpstream(t *testing.T) {
	c := newExtensionEditorProbe(t, "Edit", "", nil, nil)
	if got := len(c.Children()); got != 9 {
		t.Fatalf("children = %d, want 9", got)
	}
	withDescription := newExtensionEditorProbe(t, "Edit", "", nil, &ExtensionEditorOptions{Description: "why"})
	if got := len(withDescription.Children()); got != 11 {
		t.Fatalf("children with a description = %d, want 11", got)
	}
	before := strings.Join(c.Render(40), "\n")
	c.HandleInput("x")
	if after := strings.Join(c.Render(40), "\n"); after == before {
		t.Fatal("typing did not change the render")
	}
	if !strings.Contains(strings.Join(c.Render(100), "\n"), "external editor") {
		t.Fatal("the external editor hint is missing (extension-editor.ts:92-99)")
	}
}

// extension-editor.ts:34-41: `_focused = false`; `set focused` writes `this.editor.focused`, so the cursor marker shows only while focused.
func TestExtensionEditorComponentForwardsFocusToItsEditor(t *testing.T) {
	c := newExtensionEditorProbe(t, "Edit", "draft", nil, nil)
	marker := func() bool { return strings.Contains(strings.Join(c.Render(60), "\n"), widthx.CursorMarker) }
	if c.Focused() || marker() {
		t.Fatalf("a new component is unfocused with no cursor marker (focused=%v marker=%v)", c.Focused(), marker())
	}
	c.SetFocused(true)
	if !c.Focused() || !marker() {
		t.Fatalf("focused=%v marker=%v, want both", c.Focused(), marker())
	}
	c.SetFocused(false)
	if c.Focused() || marker() {
		t.Fatalf("unfocused: focused=%v marker=%v", c.Focused(), marker())
	}
}

// extension-editor.ts handleInput: tui.select.cancel is tested before app.editor.external; every other key reaches the editor, whose Enter calls onSubmit and
// whose Shift+Enter inserts a newline. The callbacks, not the component, carry the outcome.
// mutation-checked: testing the external key first, or not calling onCancel, fails.
func TestExtensionEditorHandleInputTransitions(t *testing.T) {
	t.Run("cancel wins over a remapped external editor key", func(t *testing.T) {
		c := newExtensionEditorProbe(t, "Edit", "draft", map[string][]string{"app.editor.external": {"escape"}}, nil)
		opened := false
		c.SetExternalEditor(func(string, string, func(string)) { opened = true })
		c.HandleInput("\x1b")
		if opened || !c.done || !c.cancelled || c.value != "" {
			t.Fatalf("opened=%v done=%v cancelled=%v value=%q", opened, c.done, c.cancelled, c.value)
		}
	})
	t.Run("typing, Shift+Enter and Enter reach the editor", func(t *testing.T) {
		c := newExtensionEditorProbe(t, "Edit", "", nil, nil)
		for _, key := range []string{"a", "b", "\x1b[13;2u", "c"} {
			c.HandleInput(key)
			if c.done {
				t.Fatalf("%q ended the dialog", key)
			}
		}
		c.HandleInput("\r")
		if !c.done || c.cancelled || c.value != "ab\nc" {
			t.Fatalf("done=%v cancelled=%v value=%q", c.done, c.cancelled, c.value)
		}
		c.HandleInput("x")
		if c.value != "ab\nc" {
			t.Fatal("input after completion changed the value")
		}
	})
	t.Run("Ctrl+C cancels and reports no text", func(t *testing.T) {
		c := newExtensionEditorProbe(t, "Edit", "kept?", nil, nil)
		c.HandleInput("z")
		c.HandleInput("\x03")
		if !c.cancelled || c.value != "" {
			t.Fatalf("cancelled=%v value=%q", c.cancelled, c.value)
		}
	})
}

// extension-editor.ts:114-137: the external-editor binding opens the handoff with the constructor's externalEditorCommand and the editor's text, keeps multiline
// text, leaves the dialog open after completion and asks the ui for a forced repaint (`tui.requestRender(true)`).
// mutation-checked: dropping the command argument, the repaint or the done guard fails.
func TestExtensionEditorExternalAction(t *testing.T) {
	c := newExtensionEditorProbe(t, "Edit", "first\nsecond", map[string][]string{"app.editor.external": {"ctrl+x"}}, nil)
	var complete func(string)
	var gotCommand string
	c.SetExternalEditor(func(command, content string, apply func(string)) {
		gotCommand = command
		if content != "first\nsecond" {
			t.Errorf("content = %q", content)
		}
		complete = apply
	})
	c.HandleInput("\x18")
	if complete == nil || c.done || gotCommand != "ed-command" {
		t.Fatalf("external editor opened=%v done=%v command=%q", complete != nil, c.done, gotCommand)
	}
	complete("changed\ntext")
	if c.ui.forced != 1 {
		t.Fatalf("forced repaints after the external edit = %d, want 1", c.ui.forced)
	}
	c.HandleInput("\r")
	if !c.done || c.value != "changed\ntext" {
		t.Fatalf("submitted %q", c.value)
	}
	complete("late")
	if c.value != "changed\ntext" {
		t.Fatal("a late edit changed the completed dialog")
	}
}

// extension-editor.ts:134-138: after the external edit the forced repaint shows the edited text. Pi re-renders every component on requestRender(true);
// PiG's parent containers reuse a child's cached lines until the child is invalidated, so the dialog must invalidate itself when the edit lands.
func TestExtensionEditorExternalEditRepaintsThroughAParentContainer(t *testing.T) {
	c := newExtensionEditorProbe(t, "Edit", "initial text", map[string][]string{"app.editor.external": {"ctrl+x"}}, nil)
	var complete func(string)
	c.SetExternalEditor(func(_, _ string, apply func(string)) { complete = apply })
	parent := tui.NewContainer(c.ExtensionEditorComponent)
	rendered := func() string { return widthx.StripAnsi(strings.Join(parent.Render(60), "\n")) }
	if before := rendered(); !strings.Contains(before, "initial text") {
		t.Fatalf("first render lacks the prefill:\n%s", before)
	}
	c.HandleInput("\x18")
	if complete == nil {
		t.Fatal("the external editor did not open")
	}
	complete("edited in child")
	if after := rendered(); !strings.Contains(after, "edited in child") || strings.Contains(after, "initial text") {
		t.Fatalf("render after the external edit:\n%s", after)
	}
}

type editorProbe struct {
	Prefill  string              `json:"prefill"`
	Bindings map[string][]string `json:"bindings"`
	Keys     []string            `json:"keys"`
	// Width, when positive, makes each state carry the component rendered at that width.
	Width int `json:"width,omitempty"`
}

type editorProbeState struct {
	Done      bool   `json:"done"`
	Cancelled bool   `json:"cancelled"`
	Value     string `json:"value"`
	Text      string `json:"text"`
	//portlint:allow emptydrop probes without a width ask for no render, so an absent member and an empty one are the same state
	Lines []string `json:"lines,omitempty"`
}

// firstEditorStateDifference names the first differing step and, for a render, the first differing row.
func firstEditorStateDifference(got, want []editorProbeState) string {
	for k := range max(len(got), len(want)) {
		if k >= len(got) || k >= len(want) {
			return fmt.Sprintf("step %d: Pig has %d states, Pi %d", k, len(got), len(want))
		}
		if reflect.DeepEqual(got[k], want[k]) {
			continue
		}
		for j := range max(len(got[k].Lines), len(want[k].Lines)) {
			var a, b string
			if j < len(got[k].Lines) {
				a = got[k].Lines[j]
			}
			if j < len(want[k].Lines) {
				b = want[k].Lines[j]
			}
			if a != b {
				return fmt.Sprintf("step %d row %d:\n  Pig %q\n  Pi  %q", k, j, a, b)
			}
		}
		return fmt.Sprintf("step %d:\n  Pig %+v\n  Pi  %+v", k, got[k], want[k])
	}
	return ""
}

// randomExtensionEditorProbes draws a prefill (multi-line, wide, combining) and key scripts of typing, line breaks, cursor and word movement, kill, yank,
// undo, history, paste, submit and cancel keys. The external-editor key (ctrl+g) stays out because Pi would spawn the editor.
func randomExtensionEditorProbes() []editorProbe {
	rng := rand.New(rand.NewSource(20261030))
	pieces := []string{"", "word", "two words", "日本語", "e\u0301", "😀", "line one\nline two", "\n", "  indented", "tab\tbed", "a very long line that will need to wrap in the editor at the default width because it keeps going on and on and on"}
	keys := []string{"a", "b", " ", "é", "日", "😀", "\r", "\n", "\x1b[13;2u", "\x1b\r", "\x1b[A", "\x1b[B", "\x1b[D", "\x1b[C", "\x01", "\x05", "\x02", "\x06", "\x17", "\x15", "\x0b", "\x19", "\x1b[1;5D", "\x1b[1;5C", "\x1bb", "\x1bf", "\x1bd", "\x7f", "\x1b[3~", "\x1b[H", "\x1b[F", "\x1b[5~", "\x1b[6~", "\x1f", "\x1b[200~pasted\ntext\x1b[201~", "\x1b", "\x03", "\x14", "\x1b[1;3A", "\x04"}
	var probes []editorProbe
	for range 500 {
		probe := editorProbe{Prefill: pieces[rng.Intn(len(pieces))] + pieces[rng.Intn(len(pieces))], Width: 8 + rng.Intn(113)}
		probe.Keys = make([]string, 1+rng.Intn(20))
		for i := range probe.Keys {
			probe.Keys[i] = keys[rng.Intn(len(keys))]
		}
		probes = append(probes, probe)
	}
	return probes
}

// Pi extension-editor.ts handleInput, run against the installed Pi: tui.select.cancel is tested before app.editor.external and every other key reaches the
// editor. The probes never open the external editor (Pi would spawn it); a remapped external key equal to the cancel key shows the order.
func TestExtensionEditorHandleInputMatchesPi(t *testing.T) {
	// The oracle forces FORCE_COLOR=3 and draws 24-bit colors, so Pig must draw them whatever COLORTERM the shell exports.
	pinHeaderTerminal(t, tui.TerminalColorModeTrueColor)
	probes := []editorProbe{
		{Keys: []string{"a", "b", "\r"}},
		{Prefill: "draft", Keys: []string{"x", "\x1b", "y"}},
		{Prefill: "draft", Keys: []string{"x", "\x03"}},
		{Keys: []string{"a", "\x1b[13;2u", "b", "\r"}},
		{Keys: []string{"a", "\n", "b", "\r"}},
		{Prefill: "one\ntwo", Keys: []string{"\x1b[A", "!", "\r"}},
		{Bindings: map[string][]string{"app.editor.external": {"escape"}}, Prefill: "draft", Keys: []string{"\x1b"}},
		{Bindings: map[string][]string{tui.KBSelectCancel: {"ctrl+q"}}, Keys: []string{"\x1b", "\x11"}},
		{Bindings: map[string][]string{tui.KBSelectCancel: {"ctrl+q"}}, Keys: []string{"a", "\x03", "\x11"}},
		{Bindings: map[string][]string{tui.KBInputSubmit: {"ctrl+s"}}, Keys: []string{"a", "\r", "\x13"}},
		{Bindings: map[string][]string{tui.KBInputNewLine: {"ctrl+n"}}, Keys: []string{"a", "\x0e", "b", "\r"}},
		// editor.ts:536: a 40-row terminal shows max(5, floor(40 * 0.3)) = 12 visual lines, not 5.
		{Prefill: strings.Repeat("line\n", 29) + "end", Width: 60, Keys: []string{"a", "\x1b[A", "\x1b[5~"}},
		// keyHint reads the bindings at construction: a rebound or multi-key action shows its own keys in the hint row.
		{Bindings: map[string][]string{tui.KBSelectConfirm: {"ctrl+s"}}, Width: 100, Keys: []string{"a"}},
		{Bindings: map[string][]string{tui.KBInputNewLine: {"ctrl+n", "alt+enter"}}, Width: 100, Keys: []string{"a"}},
		{Bindings: map[string][]string{tui.KBSelectCancel: {"ctrl+q"}}, Width: 100, Keys: []string{"a"}},
		{Bindings: map[string][]string{"app.editor.external": {"ctrl+e", "f2"}}, Width: 100, Keys: []string{"a"}},
		{Bindings: map[string][]string{tui.KBSelectConfirm: {}, tui.KBSelectCancel: {}}, Width: 100, Keys: []string{"a"}},
		// keybinding-hints.ts formatKeys: an unbound app action shows no key, not its default.
		{Bindings: map[string][]string{"app.editor.external": {}}, Width: 100, Keys: []string{"a"}},
	}
	probes = append(probes, randomExtensionEditorProbes()...)
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/extension_editor.mjs", pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []struct {
		States []editorProbeState `json:"states"`
	}
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	for i, probe := range probes {
		c := newExtensionEditorProbe(t, "Probe", probe.Prefill, probe.Bindings, nil)
		// Pi would spawn its external editor for the external key; no probe may reach it.
		c.SetExternalEditor(func(string, string, func(string)) { t.Errorf("probe %d %+v opened the external editor", i, probe) })
		var got []editorProbeState
		for _, key := range probe.Keys {
			c.HandleInput(key)
			state := editorProbeState{Done: c.done, Cancelled: c.cancelled, Text: c.editor.Text()}
			if probe.Width > 0 {
				state.Lines = c.Render(probe.Width)
			}
			if state.Done && !state.Cancelled {
				state.Value = c.value
			}
			got = append(got, state)
			if state.Done {
				break
			}
		}
		if !reflect.DeepEqual(got, expected[i].States) {
			t.Errorf("probe %d %+v:\n%s", i, probe, firstEditorStateDifference(got, expected[i].States))
		}
	}
}

// A fresh dialog at every width from 1 to 120 renders no row wider than the width, with a long title and description (tui's selector width table
// held this case until the component moved here).
func TestExtensionEditorComponentNeverExceedsRenderWidth(t *testing.T) {
	const long = "This dialog text is intentionally long so that every narrow width must wrap or truncate it before rendering"
	for width := 1; width <= 120; width++ {
		c := newExtensionEditorProbe(t, "Report a bug with a long dialog title", long, nil, &ExtensionEditorOptions{Description: long})
		for i, line := range c.Render(width) {
			if got := widthx.VisibleWidth(line); got > width {
				t.Fatalf("width %d: line %d is %d cells wide: %q", width, i, got, line)
			}
		}
	}
}

// extension-editor.ts passes the Editor's submitted text to onSubmit, and the Editor trims it with String.prototype.trim: BOM edges go, NEL stays
// (tui's TestEditorHistoryAndSubmissionUseJavaScriptTrim held this caller case until the component moved here).
func TestExtensionEditorSubmitsTheJavaScriptTrimmedText(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{"empty", "", ""},
		{"ordinary", " \t ordinary prompt \n", "ordinary prompt"},
		{"BOM edges", "\ufeffprompt\ufeff", "prompt"},
		{"BOM only", "\ufeff", ""},
		{"NEL edges", "\u0085prompt\u0085", "\u0085prompt\u0085"},
		{"NEL only", "\u0085", "\u0085"},
		{"mixed edges", "\ufeff \u0085prompt\u0085 \ufeff", "\u0085prompt\u0085"},
		{"large prompt", "\ufeff" + strings.Repeat("ä😀 ", 1024) + "\u0085\ufeff", strings.Repeat("ä😀 ", 1024) + "\u0085"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newExtensionEditorProbe(t, "Prompt", tc.input, nil, nil)
			c.HandleInput("\r")
			if !c.done || c.cancelled || c.value != tc.want {
				t.Fatalf("done=%v cancelled=%v value=%q, want submitted %q", c.done, c.cancelled, c.value, tc.want)
			}
		})
	}
}
