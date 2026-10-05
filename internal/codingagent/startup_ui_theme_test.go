package codingagent

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// restoreStartupTheme restores the process-global theme state a startup
// prompt changes.
func restoreStartupTheme(t testing.TB) {
	t.Helper()
	previousRegistry := tui.ActiveThemeRegistry()
	previousTheme, previousCaps := tui.ActiveTheme(), tui.GetCapabilities()
	t.Cleanup(func() {
		tui.SetThemeRegistry(previousRegistry)
		tui.SetTerminalColors(tui.TerminalColors{})
		tui.SetTerminalColorScheme("")
		// Rebuild the previous theme in its own color mode, which can differ from the capabilities' (see pinHeaderTerminal).
		tui.SetCapabilities(tui.TerminalCapabilities{TrueColor: previousTheme.ColorMode() == tui.TerminalColorModeTrueColor})
		tui.SetThemeByName(previousTheme.Name)
		tui.SetCapabilities(previousCaps)
	})
}

// terminalColorQueryPrefix begins the OSC 10 request that starts a terminal color query.
const terminalColorQueryPrefix = "\x1b]10;?"

type fakeStartupTerminal struct {
	mu      sync.Mutex
	writes  []string
	onInput func([]byte)
	replies [][]byte
}

func (f *fakeStartupTerminal) StartWithReadError(onInput func([]byte), _ func(), _ func(error)) error {
	f.mu.Lock()
	f.onInput = onInput
	f.mu.Unlock()
	return nil
}

// send delivers input once the prompt has started reading.
func (f *fakeStartupTerminal) send(t *testing.T, data string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		f.mu.Lock()
		onInput := f.onInput
		f.mu.Unlock()
		if onInput != nil {
			onInput([]byte(data))
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("prompt never started reading input")
		}
		time.Sleep(time.Millisecond)
	}
}

func (f *fakeStartupTerminal) written() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.writes...)
}

func (f *fakeStartupTerminal) Stop() {}

func (f *fakeStartupTerminal) Write(data string) {
	f.mu.Lock()
	f.writes = append(f.writes, data)
	replies, onInput := f.replies, f.onInput
	f.replies = nil
	f.mu.Unlock()
	go func() {
		for _, reply := range replies {
			onInput(reply)
		}
	}()
}

// startupQueryWriter forwards the terminal color query, the only control write the renderer makes here, to the fake terminal so it can reply.
type startupQueryWriter struct{ terminal *fakeStartupTerminal }

func (w startupQueryWriter) Write(data []byte) (int, error) {
	if bytes.HasPrefix(data, []byte(terminalColorQueryPrefix)) {
		w.terminal.Write(string(data))
	}
	return len(data), nil
}

// whiteTerminalReplies answers a color query for a black-on-white terminal without a palette; the DA1 reply ends the query.
func whiteTerminalReplies() [][]byte {
	return [][]byte{
		[]byte("\x1b]10;rgb:0000/0000/0000\x07"),
		[]byte("\x1b]11;rgb:ffff/ffff/ffff\x07"),
		[]byte("\x1b[?62c"),
	}
}

// upstream 0.99.1 startup-ui.ts startStartupTui and theme-controller.ts requestTerminalColors: the prompt renders at once, the replies record the terminal's colors, and none reaches the prompt as input.
func TestStartupPromptAppliesTerminalColorsAndKeepsRepliesOutOfInput(t *testing.T) {
	restoreStartupTheme(t)
	tui.SetThemeRegistry(tui.NewThemeRegistry())
	configureStartupTheme(Settings{}, nil)
	terminal := &fakeStartupTerminal{replies: append(whiteTerminalReplies(), []byte("h"), []byte("i"), []byte("\r"))}
	input := tui.NewExtensionInputComponent("Name", "")
	completed, err := runStartupComponentWith(input, StartupUIOptions{Settings: Settings{}}, false, tui.NewWithOutput(startupQueryWriter{terminal}, 80, 24), terminal)
	if err != nil || !completed {
		t.Fatalf("completed=%v err=%v", completed, err)
	}
	if got := input.Text(); got != "hi" {
		t.Fatalf("input = %q; terminal replies leaked into the prompt", got)
	}
	if got := tui.ActiveTheme().Name; got != tui.SystemThemeName {
		t.Fatalf("theme = %q; want the system theme", got)
	}
	if got := tui.GetTerminalTheme(); got != "light" {
		t.Fatalf("terminal theme = %q; want the light reported background", got)
	}
	if writes := terminal.written(); len(writes) != 1 || writes[0][:len(terminalColorQueryPrefix)] != terminalColorQueryPrefix {
		t.Fatalf("writes = %q; want one color query", writes)
	}
}

// upstream 0.99.1 theme-controller.ts requestTerminalColors passes onLateReply: colors that arrive after the timeout still apply.
func TestStartupPromptAppliesColorsThatArriveAfterTheTimeout(t *testing.T) {
	restoreStartupTheme(t)
	tui.SetThemeRegistry(tui.NewThemeRegistry())
	configureStartupTheme(Settings{}, nil)
	selector := tui.NewExtensionSelector("Pick", []string{"a", "b"})
	terminal := &fakeStartupTerminal{}
	done := make(chan error, 1)
	ui := tui.NewWithOutput(startupQueryWriter{terminal}, 80, 24)
	go func() {
		_, err := runStartupComponentWith(selector, StartupUIOptions{}, false, ui, terminal)
		done <- err
	}()
	time.Sleep(3 * terminalColorQueryTimeout)
	if got := tui.GetTerminalTheme(); got == "light" {
		t.Fatal("the terminal theme was light before any reply arrived")
	}
	for _, reply := range whiteTerminalReplies() {
		terminal.send(t, string(reply))
	}
	terminal.send(t, "\r")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := tui.GetTerminalTheme(); got != "light" {
		t.Fatalf("terminal theme = %q; want the late light background", got)
	}
	if selector.SelectedIndex() != 0 {
		t.Fatalf("selected %d", selector.SelectedIndex())
	}
}

// upstream 0.99.1 startup-ui.ts startStartupTui queries the terminal whatever the setting is, and re-resolves the setting with the reported appearance.
func TestStartupPromptWithFixedThemeQueriesAndKeepsTheTheme(t *testing.T) {
	restoreStartupTheme(t)
	tui.SetThemeRegistry(tui.NewThemeRegistry())
	settings := Settings{Theme: "dark"}
	configureStartupTheme(settings, nil)
	terminal := &fakeStartupTerminal{replies: append(whiteTerminalReplies(), []byte("\r"))}
	selector := tui.NewExtensionSelector("Pick", []string{"a"})
	if _, err := runStartupComponentWith(selector, StartupUIOptions{Settings: settings}, false, tui.NewWithOutput(startupQueryWriter{terminal}, 80, 24), terminal); err != nil {
		t.Fatal(err)
	}
	if got := tui.ActiveTheme().Name; got != "dark" {
		t.Fatalf("theme = %q; a fixed theme does not follow the terminal", got)
	}
	if writes := terminal.written(); len(writes) != 1 {
		t.Fatalf("writes = %q; want one color query", writes)
	}
}

func TestStartupPromptFlushesALoneEscape(t *testing.T) {
	restoreStartupTheme(t)
	terminal := &fakeStartupTerminal{}
	selector := tui.NewExtensionSelector("Pick", []string{"a"})
	done := make(chan error, 1)
	go func() {
		_, err := runStartupComponentWith(selector, StartupUIOptions{Settings: Settings{Theme: "dark"}}, false, tui.NewWithOutput(io.Discard, 80, 24), terminal)
		done <- err
	}()
	terminal.send(t, "\x1b")
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a lone Escape was not flushed to the prompt")
	}
	if !selector.Cancelled() {
		t.Fatal("Escape did not cancel the selector")
	}
}
