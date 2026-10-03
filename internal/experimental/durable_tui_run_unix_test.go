//go:build linux

package experimental

import (
	"bytes"
	"context"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/experimental/durableagent"
)

// publishingSource is a view source a test publishes to.
type publishingSource struct {
	mu        sync.Mutex
	state     durableagent.DurableView
	listeners []func()
}

func (source *publishingSource) Current() durableagent.DurableView {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.state
}

func (source *publishingSource) Subscribe(listener func()) func() {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.listeners = append(source.listeners, listener)
	return func() {}
}

func (source *publishingSource) publish(state durableagent.DurableView) {
	source.mu.Lock()
	source.state = state
	listeners := append([]func(){}, source.listeners...)
	source.mu.Unlock()
	for _, listener := range listeners {
		listener()
	}
}

// outputBuffer collects what the TUI writes to the terminal.
type outputBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (buffer *outputBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.data.Write(data)
}

func (buffer *outputBuffer) contains(text string) bool {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return strings.Contains(buffer.data.String(), text)
}

func waitUntil(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// tui.ts:560-655: on a real terminal the durable TUI renders its view, sends typed lines and slash commands to the controller, follows the view when it changes, and ends when the user exits with Ctrl+D.
func TestRunDurableTuiOnARealTerminal(t *testing.T) {
	agentDir := isolateExperimentalTest(t)
	project := t.TempDir()
	settings := codingagent.NewSettingsManager(project, agentDir)
	source := &publishingSource{state: stateOf([]durable.EntryRecord{userEntry(1, "first question")}, nil, nil)}
	controller := &recordingController{}

	master, slave := openExperimentalPTY(t)
	screen := &outputBuffer{}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(screen, master)
	}()
	stdin, stdout := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = slave, slave
	restore := func() { os.Stdin, os.Stdout = stdin, stdout }
	t.Cleanup(func() {
		restore()
		_ = slave.Close()
		_ = master.Close()
		<-drained
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	returned := false
	t.Cleanup(func() {
		if !returned {
			cancel()
			<-finished
		}
	})
	go func() { finished <- RunDurableTui(ctx, source, controller, settings) }()

	waitUntil(t, "the first view", func() bool { return screen.contains("first question") && screen.contains("thinking:low") })
	write := func(text string) {
		t.Helper()
		if _, err := master.WriteString(text); err != nil {
			t.Fatal(err)
		}
	}
	waitForCall := func(call string) {
		t.Helper()
		waitUntil(t, call, func() bool {
			controller.mu.Lock()
			defer controller.mu.Unlock()
			return slices.Contains(controller.calls, call)
		})
	}
	write("hello")
	write("\r")
	waitForCall("submit:hello:steer")
	write("/tasks\r")
	waitForCall("toggleTasks")
	write("queued")
	write("\x1b\r")
	waitForCall("submit:queued:followUp")
	write("\x1b")
	waitForCall("abort")
	write("\x1b[Z")
	waitForCall("cycleThinking")

	// A published view reaches the screen.
	answered := stateOf([]durable.EntryRecord{userEntry(1, "first question"), assistantEntry(2, textMessage("an answer from the pty"))}, nil, nil)
	source.publish(answered)
	waitUntil(t, "the published answer", func() bool { return screen.contains("an answer from the pty") })

	write("\x04")
	select {
	case err := <-finished:
		returned = true
		if err != nil {
			t.Fatalf("RunDurableTui = %v after Ctrl+D", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("RunDurableTui did not return after Ctrl+D")
	}
	restore()
}
