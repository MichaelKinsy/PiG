package codingagent

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi bdd9f039 (38847563) awaits the /tree summary prompts, the /import confirmation and the missing-cwd confirmation through
// showExtensionSelector, showExtensionEditor and showExtensionConfirm (interactive-mode.ts:5565, 5580, 6547 and
// promptForMissingSessionCwd), so each is a dialog on the same stack as an extension's: it stacks with the dialogs an extension
// opens meanwhile, takes the keys while it is shown, reports its blocked status, and closing it shows the dialog below again.
// Pi's /bug does not (bug-report.ts builds its own components with showOverlay and restoreEditor, and reports no status).
// These tests run each prompt on the owner goroutine that awaits it and route the keys as the terminal input pump does.

// promptRig is a mode whose owner goroutine awaits prompts while the test plays the terminal. The owner goroutine runs the loop
// that Run runs when idle (inputLoopUntil) between the calls it is handed, so posted tasks and the input route work the same way.
type promptRig struct {
	*customRig
	terminal *programStatusTerminal
	calls    chan func()
}

func newPromptRig(t *testing.T) *promptRig {
	t.Helper()
	r := newCustomRig(t)
	r.m.inputReadCh = make(chan inputChunk)
	r.m.inputErrCh = make(chan error, 1)
	r.m.isIdle = true
	r.m.tuiInst.SetFocus(r.m.editor) // as in a running session, where the editor has the focus until a dialog takes it
	p := &promptRig{customRig: r, terminal: recordProgramStatus(r.m, ""), calls: make(chan func())}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	r.workers.Go(func() {
		for {
			wake := make(chan struct{})
			next := make(chan func(), 1)
			go func() {
				select {
				case call := <-p.calls:
					next <- call
				case <-stop:
				}
				close(wake)
			}()
			_ = r.m.inputLoopUntil(context.Background(), strings.NewReader(""), wake)
			select {
			case call := <-next:
				call()
			default:
				return
			}
		}
	})
	return p
}

// ownerCall runs call on the owner goroutine, as a slash command handler does, and returns its result.
func ownerCall[T any](r *promptRig, call func() T) <-chan T {
	out := make(chan T, 1)
	r.calls <- func() { out <- call() }
	return out
}

// onOwner runs read on the owner loop, which alone may touch the stack and the editor slot, and returns what it read.
func onOwner[T any](t *testing.T, r *promptRig, read func() T) T {
	t.Helper()
	out := make(chan T, 1)
	if err := r.m.postToMain(r.m.runCtx, func() { out <- read() }); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-out:
		return got
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the owner loop did not run the read")
		panic("unreachable")
	}
}

// until polls cond on the owner loop until it holds.
func (r *promptRig) until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for !onOwner(t, r, cond) {
		if time.Now().After(deadline) {
			t.Fatalf("the owner loop never reached: %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// press routes one key as the terminal input pump does: to the modal that owns the input, else to the owner loop.
func (r *promptRig) press(t *testing.T, key string) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.m.routeInputChunk(r.m.runCtx, []byte(key), r.m.inputReadCh)
	}()
	select {
	case <-done:
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("no loop took the key %q", key)
	}
}

// untilShown waits until the editor slot holds one component that has the focus and shows text.
func (r *promptRig) untilShown(t *testing.T, text string) {
	t.Helper()
	r.until(t, "the editor slot shows "+text, func() bool {
		children := r.m.editorContainer.Children()
		if len(children) != 1 || r.m.tuiInst.GetFocusedComponent() != children[0] {
			return false
		}
		return strings.Contains(widthx.StripAnsi(strings.Join(children[0].Render(80), "\n")), text)
	})
}

// untilSlotShows waits until the editor slot holds one component that shows text, whoever has the focus: a prompt that reads
// the terminal through the modal input route keeps the editor focused.
func (r *promptRig) untilSlotShows(t *testing.T, text string) {
	t.Helper()
	r.until(t, "the editor slot shows "+text, func() bool {
		children := r.m.editorContainer.Children()
		return len(children) == 1 && strings.Contains(widthx.StripAnsi(strings.Join(children[0].Render(80), "\n")), text)
	})
}

func (r *promptRig) untilEditorShown(t *testing.T) {
	t.Helper()
	r.until(t, "the editor shows with the focus", func() bool {
		children := r.m.editorContainer.Children()
		return len(children) == 1 && children[0] == tui.Component(r.m.editor) && r.m.tuiInst.GetFocusedComponent() == tui.Component(r.m.editor)
	})
}

func (r *promptRig) openDialogs(t *testing.T) int {
	t.Helper()
	return onOwner(t, r, func() int { return len(r.m.extensionDialogs) })
}

// extensionSelect runs an extension's ctx.ui.select on a worker and waits until its dialog is on the stack.
func (r *promptRig) extensionSelect(t *testing.T, title string, options ...string) <-chan dialogCall {
	t.Helper()
	before := r.openDialogs(t)
	result := make(chan dialogCall, 1)
	r.workers.Go(func() {
		value, err := r.selectCall(r.m.runCtx, title, options...)()
		result <- dialogCall{value, err}
	})
	r.until(t, "the extension's dialog is open", func() bool { return len(r.m.extensionDialogs) > before })
	return result
}

// reported is the program status reports so far, read on the owner loop.
func (r *promptRig) reported(t *testing.T) []tui.ProgramStatus {
	t.Helper()
	return onOwner(t, r, func() []tui.ProgramStatus { return append([]tui.ProgramStatus(nil), r.terminal.reports...) })
}

func (r *promptRig) requireBlocked(t *testing.T, kind tui.ProgramStatusKind, message string) {
	t.Helper()
	reports := r.reported(t)
	if len(reports) == 0 {
		t.Fatal("the program reported no status")
	}
	got := reports[len(reports)-1]
	want := tui.ProgramStatus{State: tui.ProgramStateBlocked, Kind: kind, Message: message}
	if kind == "" {
		want = tui.ProgramStatus{State: tui.ProgramStateIdle}
	}
	got.App = ""
	if got != want {
		t.Fatalf("last program status = %+v, want %+v", got, want)
	}
}

func receive[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(testbudget.Wait(t)):
		t.Fatalf("%s did not return", what)
		panic("unreachable")
	}
}

// requireOpen fails when the prompt behind ch has already returned.
func requireOpen[T any](t *testing.T, ch <-chan T, what string) {
	t.Helper()
	select {
	case got := <-ch:
		t.Fatalf("%s returned %v while its dialog is still open", what, got)
	default:
	}
}

type choice struct {
	value string
	ok    bool
}

// The entry points under test, one per Pi caller. The tests name only these, so each reads as the user's action.
func confirmMissingCwd(r *promptRig) bool {
	return r.m.askConfirm(r.m.runCtx, "Session cwd not found", "Continue in the current directory?")
}

func chooseSummary(r *promptRig) choice {
	sc := r.m.buildSlashContext(r.m.runCtx)
	value, ok := sc.ShowExtensionSelector("Summarize branch?", []string{"No summary", "Summarize", "Summarize with custom prompt"})
	return choice{value, ok}
}

func askSummaryInstructions(r *promptRig) choice {
	sc := r.m.buildSlashContext(r.m.runCtx)
	value, ok := sc.ShowExtensionEditor("Custom summarization instructions", "")
	return choice{value, ok}
}

// The missing-cwd confirmation of /resume is awaited while an extension's question is open. Pi shows the question again once the
// confirmation is answered (showExtensionConfirm -> pushExtensionDialog/removeExtensionDialog); a confirmation that took the slot
// outside the stack put the editor back and left the question hidden but pending.
func TestMissingCwdConfirmationStacksWithAnOpenExtensionDialog(t *testing.T) {
	r := newPromptRig(t)
	question := r.extensionSelect(t, "Extension question", "a", "b")

	confirmed := ownerCall(r, func() bool { return confirmMissingCwd(r) })
	r.untilSlotShows(t, "Session cwd not found")
	r.requireBlocked(t, tui.ProgramStatusKindPermission, "Session cwd not found")

	r.press(t, "\x1b")
	if receive(t, confirmed, "the confirmation") {
		t.Fatal("Esc confirmed")
	}
	r.untilShown(t, "Extension question")
	r.requireBlocked(t, tui.ProgramStatusKindQuestion, "Extension question")
	requireOpen(t, question, "the extension's question")

	r.press(t, "\r")
	if got := receive(t, question, "the extension's question"); got.value != "a" || got.err != nil {
		t.Fatalf("the question = %q, %v; want the first option", got.value, got.err)
	}
	r.untilEditorShown(t)
	r.requireBlocked(t, "", "")
}

// The keys go to the dialog the stack shows. An extension's dialog that opens while a /tree summary prompt is awaited covers it,
// takes the keys, and shows the prompt again once it is answered.
func TestExtensionDialogOpenedDuringATreeSummaryPromptTakesTheKeys(t *testing.T) {
	r := newPromptRig(t)
	chosen := ownerCall(r, func() choice { return chooseSummary(r) })
	r.untilSlotShows(t, "Summarize branch?")

	question := r.extensionSelect(t, "Extension question", "a", "b")
	r.untilShown(t, "Extension question")
	r.requireBlocked(t, tui.ProgramStatusKindQuestion, "Extension question")

	r.press(t, "\r")
	if got := receive(t, question, "the extension's question"); got.value != "a" || got.err != nil {
		t.Fatalf("the question = %q, %v; want the first option (the key went to the dialog the stack shows)", got.value, got.err)
	}
	requireOpen(t, chosen, "the summary prompt")
	r.untilShown(t, "Summarize branch?")
	r.requireBlocked(t, tui.ProgramStatusKindQuestion, "Summarize branch?")

	r.press(t, "\r")
	if got := receive(t, chosen, "the summary prompt"); got != (choice{"No summary", true}) {
		t.Fatalf("the summary prompt = %+v, want the first option", got)
	}
	r.untilEditorShown(t)
	r.requireBlocked(t, "", "")
}

// showExtensionSelector, showExtensionEditor and showExtensionConfirm report the program as blocked while they wait, a question
// for a selector or editor and a permission request for a confirmation, and idle once they close (interactive-mode.ts:2748,
// 2757, 2828).
func TestStackedPromptsReportTheirBlockedStatus(t *testing.T) {
	t.Run("tree summary selector", func(t *testing.T) {
		r := newPromptRig(t)
		chosen := ownerCall(r, func() choice { return chooseSummary(r) })
		r.untilSlotShows(t, "Summarize branch?")
		r.requireBlocked(t, tui.ProgramStatusKindQuestion, "Summarize branch?")
		r.press(t, "\x1b")
		if got := receive(t, chosen, "the summary prompt"); got.ok {
			t.Fatalf("Esc chose %+v", got)
		}
		r.untilEditorShown(t)
		r.requireBlocked(t, "", "")
	})
	t.Run("tree custom summarization editor", func(t *testing.T) {
		r := newPromptRig(t)
		typed := ownerCall(r, func() choice { return askSummaryInstructions(r) })
		r.untilSlotShows(t, "Custom summarization instructions")
		r.requireBlocked(t, tui.ProgramStatusKindQuestion, "Custom summarization instructions")
		r.press(t, "x")
		r.press(t, "\r")
		if got := receive(t, typed, "the editor"); got != (choice{"x", true}) {
			t.Fatalf("the editor = %+v, want x", got)
		}
		r.untilEditorShown(t)
		r.requireBlocked(t, "", "")
	})
	t.Run("import confirmation", func(t *testing.T) {
		r := newPromptRig(t)
		var statuses []string
		imported := false
		done := ownerCall(r, func() error {
			sc := r.m.buildSlashContext(r.m.runCtx)
			sc.Args = "session.jsonl"
			sc.ShowStatus = func(text string) { statuses = append(statuses, text) }
			sc.ImportSession = func(string, string) (bool, error) { imported = true; return false, nil }
			return importHandler(sc)
		})
		r.untilSlotShows(t, "Import session")
		r.requireBlocked(t, tui.ProgramStatusKindPermission, "Import session")
		if got := r.openDialogs(t); got != 1 {
			t.Fatalf("%d dialogs on the stack with the import confirmation open, want it alone", got)
		}
		r.press(t, "\x1b")
		if err := receive(t, done, "/import"); err != nil {
			t.Fatal(err)
		}
		if imported || len(statuses) != 1 || statuses[0] != "Import cancelled" {
			t.Fatalf("imported=%v statuses=%q, want a cancelled import", imported, statuses)
		}
		r.untilEditorShown(t)
		r.requireBlocked(t, "", "")
	})
}

// Pi's /bug prompts are not stacked dialogs: bug-report.ts builds an ExtensionEditorComponent and ExtensionSelectorComponent,
// shows each in the editor slot with focus (showOverlay), puts the editor back when it closes (restoreEditor), and never reports
// a program status. The prompt is not on the extension dialog stack either.
func TestBugReportPromptsStayOutsideTheDialogStackAndReportNoStatus(t *testing.T) {
	r := newPromptRig(t)
	done := ownerCall(r, func() bool {
		sc := r.m.buildSlashContext(r.m.runCtx)
		_, ok := promptBugReportChoices(sc)
		return ok
	})
	r.untilSlotShows(t, "Report a bug")
	if got := r.openDialogs(t); got != 0 {
		t.Fatalf("%d dialogs on the extension stack while /bug asks for the description, want none", got)
	}
	r.press(t, "\r")
	r.untilSlotShows(t, "Include the session transcript?")
	if got := r.openDialogs(t); got != 0 {
		t.Fatalf("%d dialogs on the extension stack while /bug asks for the transcript, want none", got)
	}
	r.press(t, "\x1b")
	if receive(t, done, "/bug") {
		t.Fatal("Esc on the transcript question accepted the report")
	}
	r.untilEditorShown(t)
	for _, report := range r.reported(t) {
		if report.State == tui.ProgramStateBlocked {
			t.Fatalf("/bug reported %+v; Pi's bug-report.ts never reports a program status", report)
		}
	}
}

// resetExtensionUI disposes every stacked dialog, which in Pi takes the /tree summary prompt off the screen too. The Go caller
// that awaits it on the owner loop must not stay parked behind a dialog that no longer exists.
func TestSessionReplacementDismissesAnOpenTreeSummaryPrompt(t *testing.T) {
	r := newPromptRig(t)
	chosen := ownerCall(r, func() choice { return chooseSummary(r) })
	r.untilSlotShows(t, "Summarize branch?")

	onOwner(t, r, func() struct{} { r.m.resetExtensionDialogs(); return struct{}{} })
	if got := receive(t, chosen, "the summary prompt"); got.ok {
		t.Fatalf("the summary prompt chose %+v after the session was replaced", got)
	}
	r.untilEditorShown(t)
	r.requireBlocked(t, "", "")
}
