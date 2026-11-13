package codingagent

// pi: packages/coding-agent/src/cli/startup-ui.ts

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// stopOrderTerminal records, when the prompt stops the terminal reader, the time and how much the prompt had drawn.
type stopOrderTerminal struct {
	fakeStartupTerminal
	out           *lockedBuffer
	stops         int
	stoppedAt     time.Time
	writtenAtStop int
}

func (f *stopOrderTerminal) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops++
	f.stoppedAt = time.Now()
	f.writtenAtStop = f.out.Len()
}

// Pi startup-ui.ts:163-164 (showStartupSelector) and :235-236 (showStartupInput): finish awaits clearStartupTui (:118-122: clear, requestRender, a 25 ms
// timer) and only then calls ui.stop(), which stops the terminal reader. The cleared frame is on the terminal and 25 ms have passed when the reader stops.
func TestRunStartupComponentClearsAndSettlesBeforeStoppingTheTerminalReader(t *testing.T) {
	restoreStartupTheme(t)
	run := func(clear bool) (int, time.Duration) {
		out := &lockedBuffer{}
		terminal := &stopOrderTerminal{out: out}
		selector := tui.NewExtensionSelectorComponent("Pick", []string{"a"}, nil, nil)
		done := make(chan error, 1)
		go func() {
			_, err := runStartupComponentWith(selector, StartupUIOptions{Settings: Settings{Theme: "dark"}}, clear, tui.NewWithOutput(out, 80, 24), terminal)
			done <- err
		}()
		confirmed := time.Now()
		terminal.send(t, "\r")
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		terminal.mu.Lock()
		defer terminal.mu.Unlock()
		if terminal.stops != 1 {
			t.Fatalf("the terminal reader was stopped %d times, want once", terminal.stops)
		}
		return terminal.writtenAtStop, terminal.stoppedAt.Sub(confirmed)
	}
	keptAtStop, _ := run(false)
	clearedAtStop, settled := run(true)
	if clearedAtStop <= keptAtStop {
		t.Fatalf("the reader stopped after %d bytes with clear and %d without: it stopped before the cleared frame was written", clearedAtStop, keptAtStop)
	}
	if settled < 25*time.Millisecond {
		t.Fatalf("the reader stopped %v after the confirming key; Pi stops it after the 25 ms that follow the clear", settled)
	}
}
