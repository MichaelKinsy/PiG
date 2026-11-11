package tui

// Ports packages/chord/src/node/tui/launcher.ts

import (
	"io"
	"os"
	"os/signal"
	"sync"

	"golang.org/x/term"
)

// Terminal is the terminal a launcher runs on: its input and output, its size, raw mode (when a TTY) and its resizes. Pi takes
// Node's `stdin` and `stdout` (or any streams); a test's terminal is a pair of pipes.
type Terminal interface {
	// Input is what the user types. The launcher reads it on a goroutine until it fails or ends (a read blocked on a process's
	// stdin cannot be cancelled: it ends with the process).
	Input() io.Reader
	// Output is where the launcher writes; the launcher is its only writer while it runs.
	Output() io.Writer
	// Columns and Rows are the terminal's size; 0 when it reports none (a pseudo-terminal may: Opus E4).
	Columns() int
	Rows() int
	// IsTTY is whether the input is a terminal, which the launcher puts in raw mode while it runs.
	IsTTY() bool
	// SetRaw sets or clears raw mode (only called when IsTTY).
	SetRaw(raw bool) error
	// OnResize registers handler for the terminal's resizes; the returned function removes it.
	OnResize(handler func()) (stop func())
}

// ProcessTerminal is the process's own terminal: stdin and stdout, raw mode when stdin is a TTY, resizes by SIGWINCH.
type ProcessTerminal struct {
	state *rawState
}

var _ Terminal = (*ProcessTerminal)(nil)

// Input is stdin.
func (*ProcessTerminal) Input() io.Reader { return os.Stdin }

// Output is stdout.
func (*ProcessTerminal) Output() io.Writer { return os.Stdout }

// Columns is stdout's width (0 when it reports none).
func (*ProcessTerminal) Columns() int {
	columns, _, _ := term.GetSize(int(os.Stdout.Fd()))
	return columns
}

// Rows is stdout's height (0 when it reports none).
func (*ProcessTerminal) Rows() int { _, rows, _ := term.GetSize(int(os.Stdout.Fd())); return rows }

// IsTTY is whether stdin is a terminal.
func (*ProcessTerminal) IsTTY() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// SetRaw sets or clears stdin's raw mode.
func (t *ProcessTerminal) SetRaw(raw bool) error {
	if raw {
		state, err := makeRaw(int(os.Stdin.Fd()))
		if err == nil {
			t.state = state
		}
		return err
	}
	if t.state == nil {
		return nil
	}
	state := t.state
	t.state = nil
	return restoreRaw(int(os.Stdin.Fd()), state)
}

// OnResize calls handler for each resize signal.
func (*ProcessTerminal) OnResize(handler func()) func() {
	signals := make(chan os.Signal, 1)
	notifyResize(signals)
	stopped := make(chan struct{})
	var once sync.Once
	// Node's output.on("resize", handler) has the listener installed when it returns: the goroutine is listening before OnResize does.
	listening := make(chan struct{})
	//portlint:allow golifetime ends when the returned stop function runs
	go func() {
		close(listening)
		for {
			select {
			case <-signals:
				handler()
			case <-stopped:
				return
			}
		}
	}()
	<-listening
	return func() {
		once.Do(func() {
			signal.Stop(signals)
			close(stopped)
		})
	}
}

// inputRaw is what an Input may have for the launcher to put it in raw mode (Node's `setRawMode?` and `isTTY?`).
type inputRaw interface {
	SetRawMode(raw bool) error
	IsTTY() bool
}

// outputSized is what an Output may have to report its size and resizes (Node's `columns?`, `rows?` and "resize").
type outputSized interface {
	Columns() int
	Rows() int
	OnResize(handler func()) (stop func())
}

// streams is the Terminal of an Input and an Output: the optional members are found by interface.
type streams struct {
	input  io.Reader
	output io.Writer
}

// terminalOf is the terminal of input and output, the process's for a nil one.
func terminalOf(input io.Reader, output io.Writer) Terminal {
	if input == nil && output == nil {
		return &ProcessTerminal{}
	}
	process := &ProcessTerminal{}
	if input == nil {
		input = processInput{process}
	}
	if output == nil {
		output = processOutput{process}
	}
	return streams{input: input, output: output}
}

type processInput struct{ *ProcessTerminal }

func (processInput) Read(p []byte) (int, error) { return os.Stdin.Read(p) }

func (p processInput) SetRawMode(raw bool) error { return p.SetRaw(raw) }

type processOutput struct{ *ProcessTerminal }

func (processOutput) Write(p []byte) (int, error) { return os.Stdout.Write(p) }

func (s streams) Input() io.Reader { return s.input }

func (s streams) Output() io.Writer { return s.output }

func (s streams) Columns() int {
	if sized, ok := s.output.(outputSized); ok {
		return sized.Columns()
	}
	return 0
}

func (s streams) Rows() int {
	if sized, ok := s.output.(outputSized); ok {
		return sized.Rows()
	}
	return 0
}

func (s streams) IsTTY() bool {
	raw, ok := s.input.(inputRaw)
	return ok && raw.IsTTY()
}

func (s streams) SetRaw(raw bool) error {
	if r, ok := s.input.(inputRaw); ok {
		return r.SetRawMode(raw)
	}
	return nil
}

func (s streams) OnResize(handler func()) func() {
	if sized, ok := s.output.(outputSized); ok {
		return sized.OnResize(handler)
	}
	return func() {}
}
