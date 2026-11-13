//go:build linux

package cli

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// driveInteractivePrompts runs cmd on a pseudo-terminal, types each prompt, and waits until the scripted model has
// answered requests more times than before, once per prompt's share of the script (requests is the total it must see).
func driveInteractivePrompts(t *testing.T, cmd *exec.Cmd, prompts []string, answered func() int, requests int, output *strings.Builder) {
	t.Helper()
	steps := make([]interactiveStep, len(prompts))
	for i, prompt := range prompts {
		steps[i] = interactiveStep{
			input:   prompt,
			done:    func() bool { return answered() >= requests },
			pending: func() string { return fmt.Sprintf("the model saw %d of %d requests", answered(), requests) },
		}
	}
	driveInteractiveSteps(t, cmd, steps, output)
}

// interactiveStep is one line typed into an interactive session. before, when set, runs before the line is typed; done reports that what the line started has finished, and pending says what is still missing when it has not. A step with shows is done only once the terminal has also printed that text after the line was typed.
type interactiveStep struct {
	input   string
	before  func()
	done    func() bool
	pending func() string
	shows   string
}

// driveInteractiveSteps runs cmd on a pseudo-terminal and types each step's line once the step before it is done.
func driveInteractiveSteps(t *testing.T, cmd *exec.Cmd, steps []interactiveStep, output *strings.Builder) {
	t.Helper()
	master, slave := openPTY(t, 50, 140)
	defer func() { _ = master.Close() }()
	defer func() { _ = slave.Close() }()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	screen := &ptyOutput{}
	copied := make(chan struct{})
	go func() {
		_, _ = io.Copy(screen, master)
		close(copied)
	}()
	t.Cleanup(func() {
		_ = master.Close()
		<-copied
	})
	// Wait for the editor to render before typing.
	screen.waitQuiet(0, []byte("Pi"), 500*time.Millisecond, testbudget.Wait(t))
	for i, step := range steps {
		if step.before != nil {
			step.before()
		}
		typed := screen.mark()
		if _, err := master.WriteString(step.input); err != nil {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
		if _, err := master.WriteString("\r"); err != nil {
			t.Fatal(err)
		}
		done := func() bool {
			return step.done() && (step.shows == "" || bytes.Contains(screen.since(typed), []byte(step.shows)))
		}
		deadline := time.Now().Add(testbudget.Wait(t))
		for !done() && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if !done() {
			t.Fatalf("%s after prompt %d\n%s", step.pending(), i, screen.since(0))
		}
	}
	// Let the run finish and the screen settle.
	time.Sleep(500 * time.Millisecond)
	output.Write(screen.since(0))
}
