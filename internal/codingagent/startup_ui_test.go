package codingagent

import (
	"context"
	"io"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// The project-trust selector is a startup component. If Enter and the first
// prompt bytes share a terminal read, only Enter belongs to the selector; the
// remaining parsed sequences must reach the editor that receives focus next.
func TestStartupSelectorRetainsTypeaheadAfterConfirmation(t *testing.T) {
	selector := tui.NewExtensionSelector("Trust project folder?", []string{"Trust (this session only)"})
	chunks := []string{"\r", "/", "h", "e", "l", "l", "o"}
	got := dispatchStartupInput(selector, chunks)
	want := chunks[1:]
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("typeahead after project-trust confirmation = %q, want %q", got, want)
	}

	retainStartupInput(got)
	t.Cleanup(func() { takeStartupInput() })
	m := &InteractiveMode{}
	readCh := make(chan inputChunk)
	errCh := make(chan error, 1)
	go m.pumpTerminalInput(context.Background(), strings.NewReader(""), readCh, errCh)
	var delivered []string
	for chunk := range readCh {
		delivered = append(delivered, string(chunk.data))
		chunk.ticket.settle()
	}
	if !reflect.DeepEqual(delivered, want) {
		t.Fatalf("typeahead delivered to the editor = %q, want %q", delivered, want)
	}
}

type teardownInputTerminal struct {
	mu      sync.Mutex
	onInput func([]byte)
}

func (f *teardownInputTerminal) StartWithReadError(onInput func([]byte), _ func(), _ func(error)) error {
	f.mu.Lock()
	f.onInput = onInput
	f.mu.Unlock()
	return nil
}

func (f *teardownInputTerminal) send(t *testing.T, data string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		f.mu.Lock()
		onInput := f.onInput
		f.mu.Unlock()
		if onInput != nil {
			onInput([]byte(data))
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("startup prompt did not install its input callback")
		}
		runtime.Gosched()
	}
}

func (f *teardownInputTerminal) Stop() {
	f.mu.Lock()
	onInput := f.onInput
	f.onInput = nil
	f.mu.Unlock()
	// Model bytes that the terminal reader completed just before cancellation.
	// runStartupComponentWith drains this final callback after Stop returns.
	if onInput != nil {
		onInput([]byte("/"))
	}
}

func (*teardownInputTerminal) Write(string) {}

func TestRunStartupComponentRetainsSeparateReadDuringTeardown(t *testing.T) {
	takeStartupInput()
	t.Cleanup(func() { takeStartupInput() })
	terminal := &teardownInputTerminal{}
	selector := tui.NewExtensionSelector("Trust project folder?", []string{"Trust (this session only)"})
	done := make(chan error, 1)
	go func() {
		_, err := runStartupComponentWith(
			selector,
			StartupUIOptions{Settings: Settings{Theme: "dark"}},
			false,
			tui.NewWithOutput(io.Discard, 80, 24),
			terminal,
			nil,
		)
		done <- err
	}()
	terminal.send(t, "\r")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := takeStartupInput(); !reflect.DeepEqual(got, []string{"/"}) {
		t.Fatalf("separate-read typeahead retained after startup Stop = %q, want %q", got, []string{"/"})
	}
}
