package codingagent

// pi: packages/coding-agent/src/cli/startup-ui.ts

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// themeProbe records the terminal theme the process holds each time the prompt receives a key.
type themeProbe struct {
	*tui.ExtensionSelectorComponent
	seen *[]string
}

func (p themeProbe) HandleInput(data string) {
	*p.seen = append(*p.seen, string(tui.GetTerminalTheme()))
	p.ExtensionSelectorComponent.HandleInput(data)
}

// Pi startup-ui.ts:110 requestTerminalColors resolves on the promise queue, so a
// completed query is applied before the next terminal input event reaches the
// prompt. The reply, its end marker and a key arrive back to back; the key
// must see the reported light background, never the pre-reply theme.
func TestStartupColorQueryAppliesBeforeTheNextInputChunk(t *testing.T) {
	restoreStartupTheme(t)
	for i := range 40 {
		tui.SetTerminalColors(tui.TerminalColors{})
		tui.SetTerminalColorScheme("")
		tui.SetThemeRegistry(tui.NewThemeRegistry())
		configureStartupTui(StartupUIOptions{})
		var seen []string
		selector := themeProbe{tui.NewExtensionSelectorComponent("Pick", []string{"a", "b"}, nil, nil), &seen}
		terminal := &fakeStartupTerminal{replies: append(whiteTerminalReplies(), []byte("x"), []byte("\r"))}
		completed, err := runStartupComponentWith(selector, StartupUIOptions{}, false, tui.NewWithOutput(startupQueryWriter{terminal}, 80, 24), terminal)
		if err != nil || !completed {
			t.Fatalf("iteration %d: completed=%v err=%v", i, completed, err)
		}
		if len(seen) == 0 || seen[0] != "light" {
			t.Fatalf("iteration %d: the first key saw terminal theme %q, want light (colors applied before the next chunk)", i, seen)
		}
	}
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

// Pi startup-ui.ts:118-122 clearStartupTui: on finish the frame is cleared, the
// render the clear queued is given 25 ms, and only then does the promise
// resolve. With clear the call returns no earlier than 25 ms after the
// confirming key and writes the cleared frame; without clear it does neither.
func TestRunStartupComponentClearsFrameAndSettlesBeforeReturning(t *testing.T) {
	restoreStartupTheme(t)
	run := func(clear bool) (time.Duration, int, int) {
		terminal := &fakeStartupTerminal{}
		out := &lockedBuffer{}
		selector := tui.NewExtensionSelectorComponent("Pick", []string{"a"}, nil, nil)
		ui := tui.NewWithOutput(out, 80, 24)
		type result struct {
			err     error
			elapsed time.Duration
		}
		done := make(chan result, 1)
		started := make(chan struct{})
		var confirmed time.Time
		go func() {
			<-started
			_, err := runStartupComponentWith(selector, StartupUIOptions{Settings: Settings{Theme: "dark"}}, clear, ui, terminal)
			done <- result{err, time.Since(confirmed)}
		}()
		confirmed = time.Now()
		close(started)
		terminal.send(t, "\r")
		r := <-done
		if r.err != nil {
			t.Fatal(r.err)
		}
		return r.elapsed, out.Len(), ui.ChildCount()
	}
	_, keptBytes, keptChildren := run(false)
	elapsed, clearedBytes, clearedChildren := run(true)
	if elapsed < 25*time.Millisecond {
		t.Fatalf("returned %v after the confirming key; Pi waits 25 ms after clearing", elapsed)
	}
	if keptChildren != 1 || clearedChildren != 0 {
		t.Fatalf("children after return: without clear %d, with clear %d; want 1 and 0 (the frame is cleared)", keptChildren, clearedChildren)
	}
	if clearedBytes <= keptBytes {
		t.Fatalf("clear wrote %d bytes, no clear wrote %d; the cleared frame is missing", clearedBytes, keptBytes)
	}
}
