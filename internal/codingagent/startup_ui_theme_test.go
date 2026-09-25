package codingagent

import (
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// restoreStartupTheme restores the process-global theme state a startup
// prompt changes.
func restoreStartupTheme(t *testing.T) {
	t.Helper()
	previousRegistry := tui.ActiveThemeRegistry()
	previousName := tui.ActiveTheme().Name
	t.Cleanup(func() {
		tui.SetThemeRegistry(previousRegistry)
		tui.SetThemeByName(previousName)
	})
}

func TestStartupThemeDetectionColorSchemeReplyWins(t *testing.T) {
	detection := newStartupThemeDetection("", map[string]string{"COLORFGBG": "0;15"})
	if got := detection.start(); got != "\x1b[?996n\x1b]11;?\x07" {
		t.Fatalf("queries = %q", got)
	}
	consumed, settled := detection.consume("\x1b]11;rgb:0000/0000/0000\x07")
	if !consumed || settled {
		t.Fatalf("OSC 11 reply consumed=%v settled=%v; want consumed, not settled", consumed, settled)
	}
	consumed, settled = detection.consume("\x1b[?997;2n")
	if !consumed || !settled {
		t.Fatalf("color-scheme reply consumed=%v settled=%v", consumed, settled)
	}
	if got := detection.themeName(); got != "light" {
		t.Fatalf("theme = %q; the color-scheme reply outranks the dark background", got)
	}
	if detection.timeout() {
		t.Fatal("timeout after settling must not reapply")
	}
	if consumed, settled := detection.consume("\x1b[?997;1n"); !consumed || settled {
		t.Fatalf("later scheme report consumed=%v settled=%v; want consumed only", consumed, settled)
	}
}

func TestStartupThemeDetectionFallsBackToBackgroundThenEnvironment(t *testing.T) {
	detection := newStartupThemeDetection("", map[string]string{"COLORFGBG": "15;0"})
	detection.start()
	if consumed, _ := detection.consume("\x1b]11;#ffffff\x1b\\"); !consumed {
		t.Fatal("ST-terminated OSC 11 reply not consumed")
	}
	if !detection.timeout() {
		t.Fatal("timeout did not settle")
	}
	if got := detection.themeName(); got != "light" {
		t.Fatalf("theme = %q; want the light OSC 11 background", got)
	}

	silent := newStartupThemeDetection("", map[string]string{"COLORFGBG": "0;15"})
	silent.start()
	silent.timeout()
	if got := silent.themeName(); got != "light" {
		t.Fatalf("theme = %q; want COLORFGBG light background", got)
	}

	unparsable := newStartupThemeDetection("", map[string]string{})
	unparsable.start()
	if consumed, _ := unparsable.consume("\x1b]11;garbage\x07"); !consumed {
		t.Fatal("unparsable OSC 11 reply must still be consumed")
	}
	unparsable.timeout()
	if got := unparsable.themeName(); got != "dark" {
		t.Fatalf("theme = %q; want the dark fallback", got)
	}
}

func TestStartupThemeDetectionConsumesOnlyRepliesItAwaits(t *testing.T) {
	detection := newStartupThemeDetection("", nil)
	if consumed, _ := detection.consume("\x1b]11;rgb:ffff/ffff/ffff\x07"); consumed {
		t.Fatal("OSC 11 reply consumed before a query was sent")
	}
	detection.start()
	detection.timeout()
	// A reply arriving after the timeout is still swallowed.
	if consumed, settled := detection.consume("\x1b]11;rgb:ffff/ffff/ffff\x07"); !consumed || settled {
		t.Fatalf("late reply consumed=%v settled=%v", consumed, settled)
	}
	if consumed, _ := detection.consume("\x1b]11;rgb:ffff/ffff/ffff\x07"); consumed {
		t.Fatal("second reply to one query consumed")
	}
	for _, key := range []string{"a", "\r", "\x1b[A", "\x1b"} {
		if consumed, _ := detection.consume(key); consumed {
			t.Fatalf("key %q consumed", key)
		}
	}
}

func TestStartupThemeDetectionHonorsThemeSetting(t *testing.T) {
	if newStartupThemeDetection("dark", nil) != nil || newStartupThemeDetection("custom", nil) != nil {
		t.Fatal("a fixed theme must not query the terminal")
	}
	detection := newStartupThemeDetection("paper/night", nil)
	if detection == nil {
		t.Fatal("automatic setting must query the terminal")
	}
	detection.start()
	detection.consume("\x1b[?997;1n")
	if got := detection.themeName(); got != "night" {
		t.Fatalf("theme = %q; want the dark half of the automatic setting", got)
	}
}

// fakeStartupTerminal records writes and feeds scripted input once the
// queries are written, like a terminal answering them.
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

func TestStartupPromptAppliesTerminalReplyAndKeepsItOutOfInput(t *testing.T) {
	restoreStartupTheme(t)
	tui.SetThemeRegistry(tui.NewThemeRegistry())
	tui.SetTheme("dark")
	terminal := &fakeStartupTerminal{replies: [][]byte{
		[]byte("\x1b]11;rgb:ffff/ffff/ffff\x07"),
		[]byte("\x1b[?997;2n"),
		[]byte("hi"),
		[]byte("\r"),
	}}
	input := tui.NewExtensionInputComponent("Name", "")
	opts := StartupUIOptions{Settings: Settings{}}
	completed, err := runStartupComponentWith(input, opts, false, tui.NewWithOutput(io.Discard, 80, 24), terminal, map[string]string{})
	if err != nil || !completed {
		t.Fatalf("completed=%v err=%v", completed, err)
	}
	if got := input.Text(); got != "hi" {
		t.Fatalf("input = %q; terminal replies leaked into the prompt", got)
	}
	if got := tui.ActiveTheme().Name; got != "light" {
		t.Fatalf("theme = %q; want the light color-scheme reply", got)
	}
	if writes := terminal.written(); len(writes) != 1 || writes[0] != "\x1b[?996n\x1b]11;?\x07" {
		t.Fatalf("writes = %q", writes)
	}
}

func TestStartupPromptFallsBackToBackgroundReplyAtTimeout(t *testing.T) {
	restoreStartupTheme(t)
	tui.SetThemeRegistry(tui.NewThemeRegistry())
	tui.SetTheme("dark")
	selector := tui.NewExtensionSelector("Pick", []string{"a", "b"})
	terminal := &fakeStartupTerminal{replies: [][]byte{[]byte("\x1b]11;rgb:ffff/ffff/ffff\x07")}}
	done := make(chan error, 1)
	ui := tui.NewWithOutput(io.Discard, 80, 24)
	go func() {
		_, err := runStartupComponentWith(selector, StartupUIOptions{}, false, ui, terminal, map[string]string{})
		done <- err
	}()
	// Answer only after the 100 ms query timeout has settled detection.
	time.Sleep(3 * startupThemeQueryTimeout)
	terminal.send(t, "\r")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := tui.ActiveTheme().Name; got != "light" {
		t.Fatalf("theme = %q; want the light OSC 11 background after the timeout", got)
	}
	if selector.SelectedIndex() != 0 {
		t.Fatalf("selected %d", selector.SelectedIndex())
	}
}

func TestStartupPromptWithFixedThemeSendsNoQuery(t *testing.T) {
	restoreStartupTheme(t)
	terminal := &fakeStartupTerminal{}
	selector := tui.NewExtensionSelector("Pick", []string{"a"})
	done := make(chan error, 1)
	go func() {
		_, err := runStartupComponentWith(selector, StartupUIOptions{Settings: Settings{Theme: "dark"}}, false, tui.NewWithOutput(io.Discard, 80, 24), terminal, nil)
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
	if writes := terminal.written(); len(writes) != 0 {
		t.Fatalf("writes = %q; a fixed theme must not query the terminal", writes)
	}
	if !selector.Cancelled() {
		t.Fatal("Escape did not cancel the selector")
	}
}
