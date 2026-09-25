package codingagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// staleChatCap is a fixed transcript cap unrelated to the dialog's size.
const staleChatCap = 5

func newExtensionDialogProbe(t *testing.T) (*InteractiveMode, *bytes.Buffer) {
	t.Helper()
	return newExtensionDialogProbeSized(t, 100, 30)
}

func newExtensionDialogProbeSized(t *testing.T, width, height int) (*InteractiveMode, *bytes.Buffer) {
	t.Helper()
	m := NewInteractiveMode(InteractiveOptions{CWD: t.TempDir()})
	m.editor = tui.NewEditor()
	m.editorContainer = tui.NewContainer()
	m.editorContainer.Add(m.editor)
	m.chatContainer = tui.NewContainer()
	m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
	var output bytes.Buffer
	m.tuiInst = tui.NewWithOutput(&output, width, height)
	m.tuiInst.Add(m.layout)
	return m, &output
}

func runExtensionDialogProbe(
	t *testing.T,
	m *InteractiveMode,
	call func() (string, error),
	input []string,
) (string, error) {
	t.Helper()
	type callResult struct {
		value string
		err   error
	}
	resultCh := make(chan callResult, 1)
	go func() {
		value, err := call()
		resultCh <- callResult{value: value, err: err}
	}()

	select {
	case task := <-m.uiTaskCh:
		task()
	case <-time.After(time.Second):
		t.Fatal("dialog did not post its mutation to the UI loop")
	}
	if m.extensionDialog == nil {
		t.Fatal("dialog was not installed on the UI loop")
	}
	for _, key := range input {
		if err := m.dispatchKey(context.Background(), key); err != nil {
			t.Fatal(err)
		}
	}

	select {
	case result := <-resultCh:
		return result.value, result.err
	case <-time.After(time.Second):
		t.Fatal("dialog did not return its result")
		return "", nil
	}
}

func TestAC48SubprocessDialogsRunOnUILoop(t *testing.T) {
	m, output := newExtensionDialogProbe(t)
	ui := &ExtUIContext{m: m}

	selected, err := runExtensionDialogProbe(t, m, func() (string, error) {
		return ui.Select(context.Background(), "Pick one", []string{"first", "second"}, nil)
	}, []string{"\x1b[B", "\r"})
	if err != nil || selected != "second" {
		t.Fatalf("select = %q, %v; want second", selected, err)
	}
	if !bytes.Contains(output.Bytes(), []byte("Pick one")) {
		t.Fatal("select title was not rendered")
	}

	input, err := runExtensionDialogProbe(t, m, func() (string, error) {
		return ui.Input(context.Background(), "Your name", "name", nil)
	}, []string{"Ada", "\r"})
	if err != nil || input != "Ada" {
		t.Fatalf("input = %q, %v; want Ada", input, err)
	}

	edited, err := runExtensionDialogProbe(t, m, func() (string, error) {
		return ui.Editor(context.Background(), "Edit note", "start")
	}, []string{"-end", "\r"})
	if err != nil || edited != "start-end" {
		t.Fatalf("editor = %q, %v; want start-end", edited, err)
	}
}

func TestExtUIContextDialogCancellationIsExplicit(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(*ExtUIContext) (string, error)
	}{
		{"select", func(ui *ExtUIContext) (string, error) {
			return ui.Select(context.Background(), "Cancel me", []string{"first"}, nil)
		}},
		{"input", func(ui *ExtUIContext) (string, error) {
			return ui.Input(context.Background(), "Cancel me", "", nil)
		}},
		{"editor", func(ui *ExtUIContext) (string, error) {
			return ui.Editor(context.Background(), "Cancel me", "")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newExtensionDialogProbe(t)
			value, err := runExtensionDialogProbe(t, m, func() (string, error) {
				return tc.call(&ExtUIContext{m: m})
			}, []string{"\x1b"})
			if value != "" || !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel = %q, %v; want context.Canceled", value, err)
			}
		})
	}
}

func TestExtUIContextDialogContextCancellationRestoresEditorOnUILoop(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := (&ExtUIContext{m: m}).Input(ctx, "Cancel by context", "", nil)
		result <- err
	}()

	select {
	case task := <-m.uiTaskCh:
		task()
	case <-time.After(time.Second):
		t.Fatal("dialog did not open")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("context cancellation = %v", err)
	}
	select {
	case task := <-m.uiTaskCh:
		task()
	case <-time.After(time.Second):
		t.Fatal("dialog cleanup was not posted to the UI loop")
	}
	if m.extensionDialog != nil {
		t.Fatal("cancelled dialog remains installed")
	}
}

func TestExtensionSlashCommandDoesNotBlockUILoop(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	m, _ := newExtensionDialogProbe(t)
	m.slashRegistry = NewSlashRegistry()
	m.newRunner = inproc.NewRunner([]extension.Extension{{
		Commands: map[string]extension.RegisteredCommand{
			"probe": {
				Name: "probe",
				Handler: func(context.Context, string) error {
					close(started)
					<-release
					return nil
				},
			},
		},
	}}, t.TempDir())
	m.syncExtensionSlashCommands()

	dispatched := make(chan error, 1)
	go func() {
		dispatched <- m.slashRegistry.Dispatch(&SlashContext{}, "/probe", &ExtensionContext{})
	}()
	select {
	case err := <-dispatched:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("extension command blocked the UI dispatch path")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("extension command handler did not start")
	}
	close(release)
}

// TestExtensionDialogKeepsTranscriptVisible pins #667: every extension
// Select/Input/Editor reused setTreeViewMode, capping the transcript to
// treeContextLines (5) regardless of terminal size, so the user answered a
// clarifying question with the reasoning that prompted it scrolled off.
//
// The dialog must still fit, so the transcript is capped: but to the terminal
// height it can actually use, not to /tree's constant.
func TestExtensionDialogKeepsTranscriptVisible(t *testing.T) {
	const termHeight = 30
	for _, tc := range []struct {
		name string
		open func(*ExtUIContext) (string, error)
		keys []string
	}{
		{"select", func(ui *ExtUIContext) (string, error) {
			return ui.Select(context.Background(), "Pick one", []string{"first", "second"}, nil)
		}, []string{"\r"}},
		{"input", func(ui *ExtUIContext) (string, error) {
			return ui.Input(context.Background(), "Your name", "name", nil)
		}, []string{"\r"}},
		{"editor", func(ui *ExtUIContext) (string, error) {
			return ui.Editor(context.Background(), "Edit note", "start")
		}, []string{"\r"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newExtensionDialogProbe(t)
			for i := range 60 {
				m.chatContainer.Add(tui.NewText(fmt.Sprintf("transcript line %d", i)))
			}
			ui := &ExtUIContext{m: m}

			resultCh := make(chan struct{}, 1)
			go func() { _, _ = tc.open(ui); resultCh <- struct{}{} }()
			select {
			case task := <-m.uiTaskCh:
				task()
			case <-time.After(time.Second):
				t.Fatal("dialog did not post its mutation to the UI loop")
			}

			visible := len(m.chatContainer.Render(100))
			if visible <= staleChatCap {
				t.Fatalf("transcript capped to %d lines with a dialog open; "+
					"that is a fixed %d-line budget, not a dialog-sized one (#667)",
					visible, staleChatCap)
			}
			if visible >= termHeight {
				t.Fatalf("transcript rendered %d lines on a %d-line terminal; "+
					"the dialog has no room", visible, termHeight)
			}

			for _, key := range tc.keys {
				_ = m.dispatchKey(context.Background(), key)
			}
			<-resultCh
			if restored := len(m.chatContainer.Render(100)); restored != 60 {
				t.Fatalf("after the dialog closed the transcript renders %d lines; want all 60", restored)
			}
		})
	}
}

// The /tree selector takes the editor slot and leaves the transcript alone:
// upstream showSelector swaps only editorContainer, and the tree limits
// itself to max(5, floor(terminalHeight / 2)) rows. Interactive mode capped
// the transcript to its last 5 lines while the tree was open (GUARD-09).
func TestTreeSelectorKeepsTheTranscript(t *testing.T) {
	m, _ := newExtensionDialogProbe(t)
	for i := range 60 {
		m.chatContainer.Add(tui.NewText(fmt.Sprintf("transcript line %d", i)))
	}
	ts := tui.NewTreeSelect("Session tree", nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.runEditorSlotTreeSelector(ts)
	}()
	var input chan []byte
	deadline := time.Now().Add(5 * time.Second)
	for input == nil {
		input, _ = m.modalRoute()
		if time.Now().After(deadline) {
			t.Fatal("the tree selector did not open")
		}
		time.Sleep(5 * time.Millisecond)
	}
	visible := len(m.chatContainer.Render(100))
	input <- []byte("\x1b")
	<-done
	if visible != 60 {
		t.Fatalf("the transcript rendered %d of 60 lines with /tree open; upstream does not cap it", visible)
	}
}

// TestExtensionDialogCapScalesWithTerminalHeight closes the gap the first #667
// regression left: it fixed the terminal at 30 rows, so a hardcoded cap would
// have passed. The criterion is that the budget scales with the terminal.
func TestExtensionDialogCapScalesWithTerminalHeight(t *testing.T) {
	seen := map[int]int{}
	for _, height := range []int{20, 40, 80} {
		m, _ := newExtensionDialogProbeSized(t, 100, height)
		for i := range 200 {
			m.chatContainer.Add(tui.NewText(fmt.Sprintf("transcript line %d", i)))
		}
		ui := &ExtUIContext{m: m}
		go func() { _, _ = ui.Select(context.Background(), "Pick", []string{"a", "b"}, nil) }()
		select {
		case task := <-m.uiTaskCh:
			task()
		case <-time.After(time.Second):
			t.Fatal("dialog did not post to the UI loop")
		}
		seen[height] = len(m.chatContainer.Render(100))
	}
	if seen[20] >= seen[40] || seen[40] >= seen[80] {
		t.Fatalf("chat cap does not scale with terminal height: %v", seen)
	}
}

// TestExtensionDialogKeepsAFloorOnAShortTerminal exercises
// minExtensionDialogChatLines, which no other test reaches.
func TestExtensionDialogKeepsAFloorOnAShortTerminal(t *testing.T) {
	m, _ := newExtensionDialogProbeSized(t, 100, 6)
	for i := range 50 {
		m.chatContainer.Add(tui.NewText(fmt.Sprintf("line %d", i)))
	}
	ui := &ExtUIContext{m: m}
	go func() { _, _ = ui.Select(context.Background(), "Pick", []string{"a", "b", "c"}, nil) }()
	select {
	case task := <-m.uiTaskCh:
		task()
	case <-time.After(time.Second):
		t.Fatal("dialog did not post to the UI loop")
	}
	if visible := len(m.chatContainer.Render(100)); visible != minExtensionDialogChatLines {
		t.Fatalf("short terminal shows %d chat lines; want the %d-line floor",
			visible, minExtensionDialogChatLines)
	}
}

// TestExtensionDialogCapIsRecomputedOnHeightChange pins the defect the first
// fix shipped with: the cap was computed once at open, so a resize while a
// dialog was up left the previous height's budget in place.
//
// The probe TUI is fixed-size, so this drives the height handler directly and
// asserts it recomputes rather than simulating a real SIGWINCH. What it proves
// is that onTerminalHeightChange re-derives the cap while a dialog is open; the
// wiring that calls it on resize is asserted separately below.
func TestExtensionDialogCapIsRecomputedOnHeightChange(t *testing.T) {
	m, _ := newExtensionDialogProbeSized(t, 100, 40)
	for i := range 200 {
		m.chatContainer.Add(tui.NewText(fmt.Sprintf("line %d", i)))
	}
	ui := &ExtUIContext{m: m}
	go func() { _, _ = ui.Select(context.Background(), "Pick", []string{"a", "b"}, nil) }()
	select {
	case task := <-m.uiTaskCh:
		task()
	case <-time.After(time.Second):
		t.Fatal("dialog did not post to the UI loop")
	}
	want := len(m.chatContainer.Render(100))

	// Stand in for a resize having moved the cap off its correct value.
	m.chatContainer.SetMaxLines(staleChatCap)
	m.onTerminalHeightChange(m.tuiInst.Height())

	if got := len(m.chatContainer.Render(100)); got != want {
		t.Fatalf("height change left the cap at %d; want it recomputed to %d", got, want)
	}
}

// TestHeightChangesReachTheDialogCapWithoutSubprocessExtensions guards the
// wiring: the handler used to be registered only when a SubprocessHost existed,
// so with no extensions loaded a resize fired nothing at all.
func TestHeightChangesReachTheDialogCapWithoutSubprocessExtensions(t *testing.T) {
	m, _ := newExtensionDialogProbeSized(t, 100, 40)
	if m.opts.SubprocessHost != nil {
		t.Fatal("probe unexpectedly has a subprocess host")
	}
	m.chatContainer.SetMaxLines(staleChatCap)
	m.extensionDialog = &extensionDialog{component: tui.NewText("dialog")}
	m.onTerminalHeightChange(m.tuiInst.Height())
	if got := len(m.chatContainer.Render(100)); got == staleChatCap {
		t.Fatal("height change did not recompute the cap without a subprocess host")
	}
}

// TestCancelledDialogRestoresTheTranscript covers the third setExtensionDialog
// ViewMode call site. runDialog restores the cap on the ctx.Done() path as well
// as on completion, and only the completion path had a test: a dialog
// abandoned by a cancelled turn would have left the transcript capped for the
// rest of the session.
func TestCancelledDialogRestoresTheTranscript(t *testing.T) {
	m, _ := newExtensionDialogProbeSized(t, 100, 30)
	for i := range 60 {
		m.chatContainer.Add(tui.NewText(fmt.Sprintf("line %d", i)))
	}
	ui := &ExtUIContext{m: m}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := ui.Select(ctx, "Pick", []string{"a", "b"}, nil)
		done <- err
	}()

	select {
	case task := <-m.uiTaskCh:
		task()
	case <-time.After(time.Second):
		t.Fatal("dialog did not post to the UI loop")
	}
	if capped := len(m.chatContainer.Render(100)); capped == 60 {
		t.Fatal("dialog opened without capping the transcript; the test proves nothing")
	}

	cancel()
	// The cleanup runs on the UI loop, so pump it.
	select {
	case task := <-m.uiTaskCh:
		task()
	case <-time.After(time.Second):
		t.Fatal("cancellation did not post its cleanup to the UI loop")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v; want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled dialog did not return")
	}

	if restored := len(m.chatContainer.Render(100)); restored != 60 {
		t.Fatalf("cancelled dialog left the transcript capped at %d lines; want all 60", restored)
	}
}
