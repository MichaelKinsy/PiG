package codingagent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestStdinBufferDispatchChunks(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		flush bool
		want  []string
	}{
		{"single printable", "a", false, []string{"a"}},
		{"plain word", "hello", false, []string{"h", "e", "l", "l", "o"}},
		{"slash command then enter", "/help\r", false, []string{"/", "h", "e", "l", "p", "\r"}},
		{"trailing newline", "hi\n", false, []string{"h", "i", "\n"}},
		{"two lines", "a\rb", false, []string{"a", "\r", "b"}},
		{"CRLF pair", "x\r\n", false, []string{"x", "\r", "\n"}},
		{"both bare returns", "\r\n", false, []string{"\r", "\n"}},

		// Shift+Enter under Ghostty/iTerm legacy mode arrives as ESC+CR and
		// must remain one sequence so CR does not submit the editor.
		{"shift-enter legacy ESC+CR", "\x1b\r", false, []string{"\x1b\r"}},
		{"shift-enter kitty CSI-u", "\x1b[13;2u", false, []string{"\x1b[13;2u"}},
		{"shifted printable modifyOtherKeys", "\x1b[27;2;65~", false, []string{"\x1b[27;2;65~"}},
		{"alt+letter is opaque", "\x1bb", false, []string{"\x1bb"}},
		{"arrow up CSI", "\x1b[A", false, []string{"\x1b[A"}},
		{"home CSI with params", "\x1b[1;5H", false, []string{"\x1b[1;5H"}},
		{"SS3 F1", "\x1bOP", false, []string{"\x1bOP"}},
		{"text then ESC seq then enter", "ab\x1b[Ac\r", false, []string{"a", "b", "\x1b[A", "c", "\r"}},
		{"bare ESC flush", "x\x1b", true, []string{"x", "\x1b"}},
		{"bracketed paste single chunk", "\x1b[200~hi\nthere\x1b[201~", false, []string{"\x1b[200~hi\nthere\x1b[201~"}},
		{"bracketed paste with surrounding text", "before\x1b[200~hi\nthere\x1b[201~after", false,
			[]string{"b", "e", "f", "o", "r", "e", "\x1b[200~hi\nthere\x1b[201~", "a", "f", "t", "e", "r"}},
		// An incomplete paste stays buffered for the next terminal read.
		{"bracketed paste missing end marker", "\x1b[200~incomplete", false, nil},
		{"ctrl+D alone", "\x04", false, []string{"\x04"}},
		{"ctrl+O alone", "\x0f", false, []string{"\x0f"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b StdinBuffer
			got := b.ProcessString(tc.in)
			if tc.flush {
				got = append(got, b.Flush()...)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("StdinBuffer.ProcessString(%q) =\n  got  %#v\n  want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestStdinBufferDispatch_DropsKittyKeyRelease(t *testing.T) {
	// Under the Kitty keyboard protocol (extendedKeyInit pushes \x1b[>7u, whose
	// flag 2 reports event types), a keypress emits a press event AND a release
	// event (event type :3). Releases must not reach a focused component or
	// every key fires twice: the settings-menu up-arrow moved twice per press,
	// `/` was inserted twice.
	//
	// StdinBuffer decodes terminal reads before focus routing; the delivery
	// filter then applies upstream's KeyReleaseReceiver rule per sequence.
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"up-arrow press kept, release dropped", "\x1b[1;1:1A\x1b[1;1:3A", []string{"\x1b[1;1:1A"}},
		{"slash press kept, release dropped", "/\x1b[47;1:3u", []string{"/"}},
		{"bare arrow release dropped", "\x1b[1;1:3A", nil},
		{"bare CSI-u release dropped", "\x1b[47;1:3u", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b StdinBuffer
			got := dropKeyReleases(tui.NewExtensionInputComponent("t", "p"), b.ProcessString(tc.in))
			if len(got) != len(tc.want) {
				t.Fatalf("delivery of StdinBuffer input %q =\n  got  %#v\n  want %#v", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("StdinBuffer input %q[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestClassifyKey(t *testing.T) {
	tui.SetKittyProtocolActive(false)
	t.Cleanup(func() { tui.SetKittyProtocolActive(false) })
	cases := []struct {
		name string
		in   string
		want keyAction
	}{
		// Submit / newline distinction follows upstream mode-aware matching.
		// Ctrl+J is LF in legacy mode; ESC+CR is Alt+Enter until Kitty mode is active.
		{"plain Enter", "\r", actionSubmit},
		{"Ctrl+J", "\n", actionNewline},
		// Alt+Enter (follow-up) depends on the platform's keybindings;
		// TestPlatformDivergentControlKeys asserts it.
		{"ESC+LF is not a Pi key encoding", "\x1b\n", actionInsert},
		{"Shift+Enter kitty CSI-u", "\x1b[13;2u", actionNewline},
		{"unrecognized CSI-tilde", "\x1b[13;2~", actionInsert},
		{"Shift+Enter modifyOtherKeys form", "\x1b[27;2;13~", actionNewline},

		// Control keys we own.
		{"Ctrl+D exit", "\x04", actionExit},
		{"Ctrl+C maps to clear-editor action (dispatcher escalates to exit)", "\x03", actionClearEditor},
		{"Esc interrupts working or no-op idle", "\x1b", actionInterrupt},
		{"Ctrl+O toggle tools", "\x0f", actionToggleTools},
		{"Ctrl+G external editor", "\x07", actionExternalEditor},
		{"Ctrl+L model picker", "\x0c", actionModelPicker},
		// Ctrl+V (paste) and Ctrl+Z (suspend) diverge by platform; asserted
		// platform-correctly in TestPlatformDivergentControlKeys.
		// thinking keybindings.
		{"Shift+Tab cycles thinking", "\x1b[Z", actionCycleThinking},
		{"Ctrl+T toggles thinking", "\x14", actionToggleThinking},
		// Model cycling keybindings.
		{"Ctrl+P cycle model forward", "\x10", actionCycleModelForward},
		// Shift+Ctrl+P (cycle model backward) depends on the platform's
		// keybindings; TestPlatformDivergentControlKeys asserts it.
		// bracketed paste: framed payload is one chunk.
		{"bracketed paste chunk", "\x1b[200~hello\nworld\x1b[201~", actionBracketedPaste},
		// Follow-up / dequeue keybindings.
		{"Alt+Enter kitty CSI-u follow-up", "\x1b[13;3u", actionFollowUp},
		{"Shift+A modifyOtherKeys printable insert", "\x1b[27;2;65~", actionInsert},
		{"Alt+Enter xterm modifyOtherKeys", "\x1b[27;3;13~", actionFollowUp},
		{"Alt+Up CSI-u dequeue", "\x1b[1;3A", actionDequeue},

		// Everything else falls through to insert.
		{"printable text", "hello", actionInsert},
		{"slash command body", "/help", actionInsert},
		{"arrow up", "\x1b[A", actionInsert}, // ESC+[A is a CSI, not bare ESC
		{"alt+letter", "\x1bb", actionInsert},
		{"backspace", "\x7f", actionInsert},
	}
	km := otherColumnKeys()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyKeyWithBindings(tc.in, km); got != tc.want {
				t.Fatalf("classifyKey(%q) = %d want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestResolveOutcomeMatrix(t *testing.T) {
	// Ctrl+C and Esc are state-sensitive. Everything else
	// is state-invariant. This table is the contract.
	cases := []struct {
		name        string
		action      keyAction
		idle        bool
		editorEmpty bool
		want        dispatchOutcome
	}{
		// Esc / actionInterrupt
		{"Esc idle empty", actionInterrupt, true, true, outcomeNop},
		{"Esc idle full", actionInterrupt, true, false, outcomeNop},
		{"Esc working empty", actionInterrupt, false, true, outcomeAbort},
		{"Esc working full", actionInterrupt, false, false, outcomeAbort},

		// Ctrl+C / actionClearEditor: always routes to the clear/exit
		// handler (upstream handleCtrlC): never aborts, regardless of state.
		{"Ctrl+C idle empty (arms exit timer)", actionClearEditor, true, true, outcomeClearEditor},
		{"Ctrl+C idle non-empty (clear editor)", actionClearEditor, true, false, outcomeClearEditor},
		{"Ctrl+C working empty (clear, not abort)", actionClearEditor, false, true, outcomeClearEditor},
		{"Ctrl+C working non-empty (clear, not abort)", actionClearEditor, false, false, outcomeClearEditor},

		// Ctrl+D: exit only when the editor is empty; a non-empty editor
		// deletes the character forward via the editor (upstream custom-editor.ts).
		{"Ctrl+D empty exits", actionExit, true, true, outcomeExit},
		{"Ctrl+D non-empty deletes forward, not exit", actionExit, false, false, outcomeInsert},
		{"Ctrl+O toggles regardless of state", actionToggleTools, true, true, outcomeToggleTools},
		{"Ctrl+G idle opens editor", actionExternalEditor, true, true, outcomeExternalEditor},
		{"Ctrl+G working opens editor", actionExternalEditor, false, false, outcomeExternalEditor},
		{"Ctrl+V idle paste", actionPasteImage, true, true, outcomePasteImage},
		{"Ctrl+V working paste", actionPasteImage, false, false, outcomePasteImage},
		{"Ctrl+L idle picker", actionModelPicker, true, true, outcomeModelPicker},
		{"Ctrl+L working picker", actionModelPicker, false, false, outcomeModelPicker},
		{"session new working", actionSessionNew, false, false, outcomeSessionNew},
		{"session tree working", actionSessionTree, false, false, outcomeSessionTree},
		{"session fork working", actionSessionFork, false, false, outcomeSessionFork},
		{"session resume working", actionSessionResume, false, false, outcomeSessionResume},
		// Ctrl+Z is state-invariant: always suspends.
		{"Ctrl+Z idle suspends", actionSuspend, true, true, outcomeSuspend},
		{"Ctrl+Z working suspends", actionSuspend, false, false, outcomeSuspend},
		// thinking actions are state-invariant.
		{"Shift+Tab idle cycles thinking", actionCycleThinking, true, true, outcomeCycleThinking},
		{"Shift+Tab working cycles thinking", actionCycleThinking, false, false, outcomeCycleThinking},
		{"Ctrl+T idle toggles thinking", actionToggleThinking, true, true, outcomeToggleThinking},
		{"Ctrl+T working toggles thinking", actionToggleThinking, false, false, outcomeToggleThinking},
		// Model cycling: state-invariant.
		{"Ctrl+P idle cycles model forward", actionCycleModelForward, true, true, outcomeCycleModelForward},
		{"Ctrl+P working cycles model forward", actionCycleModelForward, false, false, outcomeCycleModelForward},
		{"Shift+Ctrl+P idle cycles model backward", actionCycleModelBackward, true, true, outcomeCycleModelBackward},
		{"Shift+Ctrl+P working cycles model backward", actionCycleModelBackward, false, false, outcomeCycleModelBackward},
		// Follow-up: always fires (state handled inside the dispatch).
		{"Alt+Enter idle follow-up", actionFollowUp, true, false, outcomeFollowUp},
		{"Alt+Enter working follow-up", actionFollowUp, false, false, outcomeFollowUp},
		// Dequeue: state-invariant.
		{"Alt+Up idle dequeue", actionDequeue, true, true, outcomeDequeue},
		{"Alt+Up working dequeue", actionDequeue, false, false, outcomeDequeue},
		{"Newline regardless of state", actionNewline, true, false, outcomeNewline},
		{"Insert printable", actionInsert, true, true, outcomeInsert},

		// Submit always reaches the submit handler. While working, it queues a steering message.
		{"Enter idle", actionSubmit, true, false, outcomeSubmit},
		{"Enter working steers", actionSubmit, false, false, outcomeSubmit},
		// bracketed paste edits the pending editor buffer in any state.
		{"bracketed paste idle", actionBracketedPaste, true, false, outcomeBracketedPaste},
		{"bracketed paste working", actionBracketedPaste, false, false, outcomeBracketedPaste},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveOutcome(tc.action, tc.idle, tc.editorEmpty)
			if got != tc.want {
				t.Fatalf("resolveOutcome(%v,idle=%v,empty=%v) = %d want %d", tc.action, tc.idle, tc.editorEmpty, got, tc.want)
			}
		})
	}
}

func TestStdinBufferEscapeBoundaries(t *testing.T) {
	cases := []struct {
		in    string
		flush bool
		want  []string
	}{
		{"\x1b", true, []string{"\x1b"}},
		{"\x1b\r", false, []string{"\x1b\r"}},
		{"\x1bb", false, []string{"\x1bb"}},
		{"\x1b[A", false, []string{"\x1b[A"}},
		{"\x1b[1;5H", false, []string{"\x1b[1;5H"}},
		{"\x1b[200~", false, nil}, // paste stays pending for content + end
		{"\x1bOP", false, []string{"\x1bOP"}},
		{"\x1b[", true, []string{"\x1b["}},
	}
	for _, tc := range cases {
		var b StdinBuffer
		got := b.ProcessString(tc.in)
		if tc.flush {
			got = append(got, b.Flush()...)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("StdinBuffer input %q = %q want %q", tc.in, got, tc.want)
		}
	}
}

// TestThinkingCycleLevels verifies the cycling logic used by cycleThinkingLevel.
// (updated: "minimal" added between "off" and "low").
func TestThinkingCycleLevels(t *testing.T) {
	// Full model (MaxThinking = High) should cycle off→minimal→low→medium→high→off.
	fullModel := &ai.Model{Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingHigh}}
	maxIdx := maxThinkingIndex(fullModel)
	if maxIdx != 4 {
		t.Fatalf("full model maxIdx: got %d want 4", maxIdx)
	}

	levels := levelsForModel(fullModel)
	cur := 0 // "off"
	wants := []string{"minimal", "low", "medium", "high", "off"}
	for _, want := range wants {
		next := (cur + 1) % (maxIdx + 1)
		got := levels[next]
		if got != want {
			t.Errorf("cycle step: got %q want %q", got, want)
		}
		cur = next
	}
}

func TestThinkingCycleLevels_XHigh(t *testing.T) {
	// XHigh model cycles through all 6 levels including "xhigh".
	xh := "xhigh"
	xhighModel := &ai.Model{
		Capabilities:     ai.ModelCapabilities{MaxThinking: ai.ThinkingXHigh},
		ThinkingLevelMap: ai.ThinkingLevelMap{ai.ThinkingXHigh: &xh},
	}
	maxIdx := maxThinkingIndex(xhighModel)
	if maxIdx != 5 {
		t.Fatalf("xhigh model maxIdx: got %d want 5", maxIdx)
	}
	levels := levelsForModel(xhighModel)
	cur := 0
	wants := []string{"minimal", "low", "medium", "high", "xhigh", "off"}
	for _, want := range wants {
		next := (cur + 1) % (maxIdx + 1)
		if levels[next] != want {
			t.Errorf("xhigh cycle step: got %q want %q", levels[next], want)
		}
		cur = next
	}
}

func TestThinkingCycleLevels_NoSupport(t *testing.T) {
	// Model with no MaxThinking: maxIdx = 0 → no cycling.
	noneModel := &ai.Model{Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingNone}}
	if maxThinkingIndex(noneModel) != 0 {
		t.Error("non-reasoning model: maxIdx should be 0")
	}
	if maxThinkingIndex(nil) != 0 {
		t.Error("nil model: maxIdx should be 0")
	}
}

func TestThinkingCycleLevels_Capped(t *testing.T) {
	medModel := &ai.Model{Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingMedium}}
	maxIdx := maxThinkingIndex(medModel)
	if maxIdx != 3 {
		t.Fatalf("medium model maxIdx: got %d want 3", maxIdx)
	}
	levels := levelsForModel(medModel)
	cur := 0
	wants := []string{"minimal", "low", "medium", "off"}
	for _, want := range wants {
		next := (cur + 1) % (maxIdx + 1)
		if levels[next] != want {
			t.Errorf("capped step: got %q want %q", levels[next], want)
		}
		cur = next
	}
}

func TestThinkingCycleLevels_MinimalOnly(t *testing.T) {
	// Minimal-cap model should cycle off→minimal→off.
	minModel := &ai.Model{Capabilities: ai.ModelCapabilities{MaxThinking: ai.ThinkingMinimal}}
	maxIdx := maxThinkingIndex(minModel)
	if maxIdx != 1 {
		t.Fatalf("minimal model maxIdx: got %d want 1", maxIdx)
	}
	levels := levelsForModel(minModel)
	// One step forward from off: minimal.
	if levels[(0+1)%(maxIdx+1)] != "minimal" {
		t.Errorf("minimal model first cycle: got %q want \"minimal\"", levels[1])
	}
	// One more step: wraps back to off.
	if levels[(1+1)%(maxIdx+1)] != "off" {
		t.Errorf("minimal model wrap: got %q want \"off\"", levels[0])
	}
}
