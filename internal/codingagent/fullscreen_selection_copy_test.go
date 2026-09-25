package codingagent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

type synchronizedOutput struct {
	mu sync.Mutex
	bytes.Buffer
}

func (output *synchronizedOutput) Write(data []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.Buffer.Write(data)
}

func (output *synchronizedOutput) Reset() {
	output.mu.Lock()
	defer output.mu.Unlock()
	output.Buffer.Reset()
}

func (output *synchronizedOutput) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.Buffer.String()
}

func TestCtrlXFallsBackToLastAssistantMessageWithoutActiveSelection(t *testing.T) {
	for _, test := range []struct {
		name       string
		fullscreen bool
	}{
		{name: "regular"},
		{name: "fullscreen", fullscreen: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var copied string
			mode := &InteractiveMode{
				editor:            tui.NewEditor(),
				keybindings:       DefaultKeybindingsManager(),
				isIdle:            true,
				lastAssistantText: "last assistant message",
				chatContainer:     tui.NewContainer(),
				copyClipboard: func(text string) error {
					copied = text
					return nil
				},
			}
			if test.fullscreen {
				renderer := tui.NewTuiAltScreenWithOutput(io.Discard, 40, 6, tui.TuiAltScreenOptions{})
				renderer.SetCopyOnSelect(false)
				mode.tuiInst = renderer
				mode.altScreen = renderer
			} else {
				mode.tuiInst = tui.NewWithOutput(io.Discard, 40, 6)
			}

			if err := mode.dispatchKey(context.Background(), "\x18"); err != nil {
				t.Fatal(err)
			}
			if copied != "last assistant message" {
				t.Fatalf("copied text = %q, want last assistant message", copied)
			}
		})
	}
}

func TestCtrlXPrefersActiveFullscreenSelectionWhenAutomaticCopyIsDisabled(t *testing.T) {
	var output synchronizedOutput
	renderer := tui.NewTuiAltScreenWithOutput(&output, 40, 6, tui.TuiAltScreenOptions{})
	t.Cleanup(renderer.Stop)
	renderer.SetCopyOnSelect(false)
	renderer.Add(tui.NewText("selected text"))
	renderer.Start()
	output.Reset()

	renderer.HandleViewportInput("\x1b[<0;1;1M")
	renderer.HandleViewportInput("\x1b[<32;6;1M")
	renderer.HandleViewportInput("\x1b[<0;6;1m")
	if strings.Contains(output.String(), "\x1b]52;c;") {
		t.Fatalf("selection release copied while automatic copy was disabled: %q", output.String())
	}
	if !renderer.HasActiveSelection() {
		t.Fatal("selection release did not retain an active selection")
	}

	mode := &InteractiveMode{
		tuiInst:     renderer,
		altScreen:   renderer,
		editor:      tui.NewEditor(),
		keybindings: DefaultKeybindingsManager(),
		isIdle:      true,
	}
	if err := mode.dispatchKey(context.Background(), "\x18"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "\x1b]52;c;") {
		t.Fatalf("Ctrl+X did not copy the active selection: %q", output.String())
	}
}

// Ports handleCopyCommand's failure branches: both surface through showError.
func TestCtrlXReportsCopyFailuresAsErrors(t *testing.T) {
	for _, test := range []struct {
		name      string
		last      string
		clipboard error
		want      string
	}{
		{name: "no assistant message", want: "Error: No agent messages to copy yet."},
		{name: "clipboard failure", last: "answer", clipboard: errors.New("Clipboard unavailable"), want: "Error: Clipboard unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			mode := &InteractiveMode{
				editor:            tui.NewEditor(),
				keybindings:       DefaultKeybindingsManager(),
				isIdle:            true,
				lastAssistantText: test.last,
				chatContainer:     tui.NewContainer(),
				tuiInst:           tui.NewWithOutput(io.Discard, 60, 6),
				copyClipboard:     func(string) error { return test.clipboard },
			}
			if err := mode.dispatchKey(context.Background(), "\x18"); err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(mode.chatContainer.Render(60), "\n"); !strings.Contains(got, test.want) {
				t.Fatalf("chat = %q, want %q", got, test.want)
			}
		})
	}
}

// With automatic copy-on-select on, a selection was already copied when it
// was made, so Ctrl+X copies the last assistant message instead.
func TestCtrlXCopiesLastMessageWhenCopyOnSelectIsOn(t *testing.T) {
	renderer := tui.NewTuiAltScreenWithOutput(io.Discard, 40, 6, tui.TuiAltScreenOptions{})
	t.Cleanup(renderer.Stop)
	renderer.SetCopyOnSelect(true)
	renderer.Add(tui.NewText("selected text"))
	renderer.Start()
	renderer.HandleViewportInput("\x1b[<0;1;1M")
	renderer.HandleViewportInput("\x1b[<32;6;1M")
	var copied string
	mode := &InteractiveMode{
		tuiInst:           renderer,
		altScreen:         renderer,
		editor:            tui.NewEditor(),
		keybindings:       DefaultKeybindingsManager(),
		isIdle:            true,
		lastAssistantText: "last assistant message",
		chatContainer:     tui.NewContainer(),
		copyClipboard: func(text string) error {
			copied = text
			return nil
		},
	}
	if !renderer.HasActiveSelection() {
		t.Fatal("drag did not leave an active selection")
	}
	if err := mode.dispatchKey(context.Background(), "\x18"); err != nil {
		t.Fatal(err)
	}
	if copied != "last assistant message" {
		t.Fatalf("copied %q, want the last assistant message", copied)
	}
}
