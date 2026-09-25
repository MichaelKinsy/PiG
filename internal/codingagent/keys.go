package codingagent

import (
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// keyAction is the abstract action a keystroke triggers. Lifted out
// of dispatchKey so the keystroke → action map is testable without
// instantiating a TUI. Order of cases matches dispatchKey: keep the
// two in sync.
//
// Split: Ctrl+C and Esc map to *different* actions even
// though both can abort while working. The dispatcher decides what
// to do based on idle/working state. classifyKey itself is pure.
type keyAction int

const (
	actionInsert             keyAction = iota // pass to editor.HandleInput as-is
	actionExit                                // Ctrl+D
	actionClearEditor                         // Ctrl+C : working: abort; idle non-empty: clear; idle empty: no-op
	actionInterrupt                           // Esc     : working: abort; idle: no-op
	actionToggleTools                         // Ctrl+O
	actionExternalEditor                      // Ctrl+G : open $VISUAL || $EDITOR on the editor buffer
	actionPasteImage                          // Ctrl+V : read clipboard image, write tmpfile, insert path at cursor
	actionModelPicker                         // Ctrl+L: open model selector overlay
	actionSuspend                             // Ctrl+Z : SIGTSTP to self; resume on `fg`
	actionSubmit                              // plain Enter
	actionNewline                             // Shift+Enter / Ctrl+J: insert literal newline
	actionFollowUp                            // Alt+Enter: queue follow-up (working) or submit (idle)
	actionDequeue                             // Alt+Up   : restore queued messages to editor
	actionCycleThinking                       // Shift+Tab: cycle thinking level
	actionToggleThinking                      // Ctrl+T: toggle thinking block visibility
	actionCycleModelForward                   // Ctrl+P    : cycle to next model
	actionCycleModelBackward                  // Shift+Ctrl+P: cycle to previous model
	actionSessionNew                          // app.session.new: start fresh conversation
	actionSessionTree                         // app.session.tree: open tree selector
	actionSessionFork                         // app.session.fork: open user-message fork selector
	actionSessionResume                       // app.session.resume: open session resume selector
	actionMessageCopy                         // app.message.copy: copy last agent message (Ctrl+X)
	actionBracketedPaste                      // ESC[200~…ESC[201~ bracketed-paste payload
)

// classifyKey maps a single (post-split) keystroke chunk to a
// keyAction. Pure function: no I/O, no state: so the test suite
// can assert the whole mapping table.
func classifyKey(data string) keyAction {
	return classifyKeyWithBindings(data, DefaultKeybindingsManager())
}

func classifyKeyWithBindings(data string, kb *KeybindingsManager) keyAction {
	if kb != nil {
		// CustomEditor.handleInput checks these bindings independently and in
		// this order before consulting history or the remaining app actions.
		// Resolve cannot preserve that contract when a user deliberately binds
		// more than one action to the same key because its registry order starts
		// with interrupt and exit.
		switch {
		case kb.Matches(data, "app.clipboard.pasteImage"):
			return actionPasteImage
		case kb.Matches(data, "app.interrupt"):
			return actionInterrupt
		case kb.Matches(data, "app.exit"):
			return actionExit
		}

		// Explicit history bindings precede every remaining app action, so a
		// user can bind ctrl+p to history although it cycles models by default.
		if kb.MatchesEditorHistory(data) {
			return actionInsert
		}

		resolved := kb.Resolve(data)
		switch resolved {
		case "app.clear":
			return actionClearEditor
		case "app.tools.expand":
			return actionToggleTools
		case "app.editor.external":
			return actionExternalEditor
		case "app.model.select":
			return actionModelPicker
		case "app.suspend":
			return actionSuspend
		case "app.message.followUp":
			return actionFollowUp
		case "app.message.dequeue":
			return actionDequeue
		case "app.thinking.cycle":
			return actionCycleThinking
		case "app.thinking.toggle":
			return actionToggleThinking
		case "app.model.cycleForward":
			return actionCycleModelForward
		case "app.model.cycleBackward":
			return actionCycleModelBackward
		case "app.session.new":
			return actionSessionNew
		case "app.session.tree":
			return actionSessionTree
		case "app.session.fork":
			return actionSessionFork
		case "app.session.resume":
			return actionSessionResume
		case "app.message.copy":
			return actionMessageCopy
		}
	}

	if tui.MatchesKeyID(data, "shift+enter") || tui.MatchesKeyID(data, "ctrl+j") {
		return actionNewline
	}
	if data == "\r" {
		return actionSubmit
	}
	// Bracketed paste arrives as one framed payload. StdinBuffer holds the
	// body across terminal reads, so embedded newlines cannot become submits.
	if strings.HasPrefix(data, "\x1b[200~") && strings.HasSuffix(data, "\x1b[201~") {
		return actionBracketedPaste
	}

	// Kitty CSI-u protocol fallback: \x1b[<codepoint>;<modifier>u
	// Decode any CSI-u sequence not matched by the hardcoded cases above.
	// This handles keybindings from terminals with Kitty keyboard protocol
	// enabled, providing forward compatibility with new bindings.
	if act := classifyCSIu(data); act != actionInsert {
		return act
	}

	return actionInsert
}

// dispatchOutcome is the state-resolved action the dispatcher should
// take. classifyKey gives the abstract keystroke meaning; resolveOutcome
// folds in idle/editor state so the dispatcher itself stays a thin
// switch over outcomes. Pure function: testable.
type dispatchOutcome int

const (
	outcomeNop dispatchOutcome = iota // do nothing (idle Esc, idle empty Ctrl+C)
	outcomeExit
	outcomeAbort       // working: abort current op (Ctrl+C or Esc while working)
	outcomeClearEditor // idle, editor non-empty: clear + flash
	outcomeToggleTools
	outcomeExternalEditor
	outcomePasteImage
	outcomeModelPicker
	outcomeSuspend
	outcomeSubmit
	outcomeNewline
	outcomeFollowUp // Alt+Enter: working: queue; idle: submit
	outcomeDequeue  // Alt+Up: restore queued messages to editor
	outcomeInsert
	outcomeCycleThinking      // Shift+Tab: state-invariant
	outcomeToggleThinking     // Ctrl+T: state-invariant
	outcomeCycleModelForward  // Ctrl+P   : state-invariant
	outcomeCycleModelBackward // Shift+Ctrl+P: state-invariant
	outcomeSessionNew         // app.session.new: start fresh conversation
	outcomeSessionTree        // app.session.tree: open tree selector
	outcomeSessionFork        // app.session.fork: open fork selector
	outcomeSessionResume      // app.session.resume: open resume selector
	outcomeCopyMessage        // app.message.copy: copy last agent message to clipboard
	outcomeBracketedPaste     // ESC[200~…ESC[201~ inserts the paste body without submitting
)

// resolveOutcome maps (keyAction, state) → dispatchOutcome.
// Pure; the unit test asserts the whole 6×2 matrix.
//
// Matrix (Ctrl+C and Esc are state-sensitive; everything
// else is state-invariant):
//
//	Idle Esc                 → outcomeNop
//	Working Esc              → outcomeAbort
//	Ctrl+C (any state)       → outcomeClearEditor (dispatcher escalates:
//	                           1st clears the editor, 2nd within 500ms exits;
//	                           never aborts: abort is Esc-only, matching
//	                           upstream handleCtrlC / onEscape)
func resolveOutcome(action keyAction, idle, editorEmpty bool) dispatchOutcome {
	switch action {
	case actionExit:
		// Ctrl+D is dual-bound: app.exit ("Exit when editor is empty") and
		// tui.editor.deleteCharForward. Upstream custom-editor.ts fires the
		// exit handler only when getText() is empty; a non-empty editor falls
		// through to super.handleInput, where the Editor deletes the character
		// forward. Route the non-empty case back to the editor via the
		// dispatcher default (outcomeInsert → editor.HandleInput), which
		// matches KBEditorDeleteCharForward on ctrl+d. The guard is editor
		// emptiness, not idle/working state.
		if editorEmpty {
			return outcomeExit
		}
		return outcomeInsert
	case actionToggleTools:
		return outcomeToggleTools
	case actionExternalEditor:
		// Upstream binds app.editor.external without a streaming check, so
		// the next steering or follow-up message can be composed there.
		return outcomeExternalEditor
	case actionPasteImage:
		// Ctrl+V image-paste inserts a tempfile path into the editor. It must work
		// while a turn is running so the user can prepare the next steering or
		// follow-up message instead of waiting for the agent to stop.
		return outcomePasteImage
	case actionModelPicker:
		// Ctrl+L opens the model selector overlay in any state, as upstream.
		return outcomeModelPicker
	case actionSuspend:
		// Ctrl+Z drops to the shell. Always allowed,
		// regardless of idle/working state: SIGTSTP is the user's
		// universal escape hatch and matches upstream's
		// unconditional `app.suspend` binding. Any in-flight
		// operation continues running while suspended (the agent
		// goroutine doesn't stop on SIGTSTP); on `fg`, the input
		// loop picks back up where it left off.
		return outcomeSuspend
	case actionCycleThinking:
		// Shift+Tab cycles thinking level regardless of
		// idle/working state. Mirrors upstream onAction("app.thinking.cycle").
		return outcomeCycleThinking
	case actionToggleThinking:
		// Ctrl+T toggles thinking block visibility. State-invariant.
		return outcomeToggleThinking
	case actionCycleModelForward:
		// Ctrl+P cycles to next model. State-invariant.
		// upstream: keybindings.ts:76-78 (app.model.cycleForward)
		return outcomeCycleModelForward
	case actionCycleModelBackward:
		// Shift+Ctrl+P cycles to previous model. State-invariant.
		// upstream: keybindings.ts:80-82 (app.model.cycleBackward)
		return outcomeCycleModelBackward
	case actionSessionNew:
		// Upstream binds the session actions without a streaming check; a
		// replacement settles the active run first (settleActiveRun).
		return outcomeSessionNew
	case actionSessionTree:
		return outcomeSessionTree
	case actionSessionFork:
		return outcomeSessionFork
	case actionSessionResume:
		return outcomeSessionResume
	case actionMessageCopy:
		// app.message.copy: copy the last agent message to the clipboard.
		// State-invariant, matching upstream's editor onAction binding.
		return outcomeCopyMessage
	case actionBracketedPaste:
		// Bracketed paste edits the pending editor buffer. Keep it available while
		// a turn is running so pasted text can be submitted as steering/follow-up.
		return outcomeBracketedPaste
	case actionSubmit:
		return outcomeSubmit
	case actionNewline:
		return outcomeNewline
	case actionFollowUp:
		// Alt+Enter: while working, queue follow-up; while idle, act as submit.
		// upstream: interactive-mode.ts:3258-3270
		return outcomeFollowUp
	case actionDequeue:
		// Alt+Up: restore queued messages to editor. State-invariant.
		// upstream: interactive-mode.ts:3272-3279 (handleDequeue)
		return outcomeDequeue
	case actionInterrupt:
		if !idle {
			return outcomeAbort
		}
		return outcomeNop
	case actionClearEditor:
		// Ctrl+C (app.clear) maps to upstream handleCtrlC: clear the
		// editor on the first press, exit on a second within 500ms. It
		// never aborts a running turn: that is Esc (actionInterrupt).
		// The clear-vs-exit escalation needs wall-clock state, so it
		// lives in the dispatcher's outcomeClearEditor case; here we
		// always route Ctrl+C there regardless of idle/editor state so
		// the second-press exit timer is always armed.
		return outcomeClearEditor
	}
	return outcomeInsert
}

// ctrlCExits reports whether a Ctrl+C at time now should exit the app
// rather than clear the editor: true when it is the second Ctrl+C within
// 500ms of the previous one (last). Mirrors upstream handleCtrlC's
// `now - lastSigintTime < 500` check (interactive-mode.ts:3262). A zero
// last (no prior Ctrl+C) never exits.
// upstream: coding-agent/src/modes/interactive/interactive-mode.ts:lastSigintTime
func ctrlCExits(last, now time.Time) bool {
	return !last.IsZero() && now.Sub(last) < 500*time.Millisecond
}
