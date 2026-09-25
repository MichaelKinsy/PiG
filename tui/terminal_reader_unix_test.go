//go:build unix

package tui

import (
	"context"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A stopped startup terminal must leave later stdin bytes for the interactive
// reader. Upstream removes its data listener without draining process.stdin.
func TestStoppedTerminalReaderDoesNotEatNextKeystroke(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()

	terminal := NewProcessTerminalWithOutput(r, nil, &strings.Builder{})
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan []byte, 1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		terminal.forwardInput(ctx, func(data []byte) { got <- data }, nil)
	}()

	if _, err := w.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	if data := <-got; string(data) != "\r" {
		t.Fatalf("startup reader got %q, want Enter", data)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		stack := make([]byte, 1<<20)
		stack = stack[:runtime.Stack(stack, true)]
		if strings.Contains(string(stack), "unix.Poll") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("startup reader did not wait for its next input")
		}
		runtime.Gosched()
	}

	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stopped terminal left a reader blocked in stdin.Read")
	}

	mainInput := make(chan []byte, 1)
	go func() {
		data, err := ReadInput(r)
		if err == nil {
			mainInput <- data
		}
	}()
	if _, err := w.Write([]byte("/")); err != nil {
		t.Fatal(err)
	}
	select {
	case data := <-mainInput:
		if string(data) != "/" {
			t.Fatalf("first keystroke after startup prompt = %q, want %q", data, "/")
		}
	case <-time.After(time.Second):
		t.Fatal("interactive reader did not receive the first post-startup key")
	}
}
