package tui

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
)

// Tests of tui_terminal.go: the Terminal a launcher runs on (Pi's `TerminalOptions.input` and `.output`, `process.stdin` and
// `process.stdout` by default; packages/chord/src/node/tui/launcher.ts:28-30,103,173-174,186-199 and the bridged terminal of
// packages/chord/src/tui/terminal.ts, whose size, input and resizes this side supplies). Pi's launcher.test.ts drives it
// through `PassThrough` streams; these cases drive the Go terminal itself: the process's own, and streams that have or lack
// the optional members (`setRawMode`, `isTTY`, `columns`, `rows`, "resize").

// swapStdio makes in and out the process's os.Stdin and os.Stdout until the test ends.
func swapStdio(t *testing.T, in, out *os.File) {
	t.Helper()
	savedIn, savedOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = in, out
	t.Cleanup(func() { os.Stdin, os.Stdout = savedIn, savedOut })
}

// pipeFile is the read and write ends of a pipe, closed at the test's end.
func pipeFile(t *testing.T) (reader, writer *os.File) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
	return reader, writer
}

func TestTheProcessTerminalIsStdinAndStdoutAndReportsNoSizeOrTTYForPipes(t *testing.T) {
	inRead, inWrite := pipeFile(t)
	outRead, outWrite := pipeFile(t)
	swapStdio(t, inRead, outWrite)
	var terminal Terminal = &ProcessTerminal{}
	if terminal.Input() != io.Reader(inRead) || terminal.Output() != io.Writer(outWrite) {
		t.Fatalf("input %v output %v, want the process's stdin and stdout", terminal.Input(), terminal.Output())
	}
	if _, err := inWrite.WriteString("typed"); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 5)
	if _, err := io.ReadFull(terminal.Input(), got); err != nil || string(got) != "typed" {
		t.Fatalf("read %q, %v", got, err)
	}
	if _, err := io.WriteString(terminal.Output(), "shown"); err != nil {
		t.Fatal(err)
	}
	got = make([]byte, 5)
	if _, err := io.ReadFull(outRead, got); err != nil || string(got) != "shown" {
		t.Fatalf("written %q, %v", got, err)
	}
	// a pipe has no size ("0 when it reports none") and is no TTY
	if terminal.Columns() != 0 || terminal.Rows() != 0 || terminal.IsTTY() {
		t.Fatalf("columns %d rows %d tty %v, want 0 0 false", terminal.Columns(), terminal.Rows(), terminal.IsTTY())
	}
}

func TestTheProcessTerminalClearingRawModeWithoutHavingSetItIsANoOpAndSettingItOnAPipeFails(t *testing.T) {
	inRead, _ := pipeFile(t)
	_, outWrite := pipeFile(t)
	swapStdio(t, inRead, outWrite)
	terminal := &ProcessTerminal{}
	if err := terminal.SetRaw(false); err != nil {
		t.Fatalf("clearing raw mode that was never set = %v", err)
	}
	if err := terminal.SetRaw(true); err == nil {
		t.Fatal("raw mode on a pipe succeeded; a pipe has no terminal mode")
	}
	// the failed attempt saved nothing to restore
	if err := terminal.SetRaw(false); err != nil {
		t.Fatalf("clearing after a failed set = %v", err)
	}
}

// rawInput is an Input with Node's `setRawMode` and `isTTY`.
type rawInput struct {
	io.Reader
	tty   bool
	err   error
	calls []bool
}

func (r *rawInput) SetRawMode(raw bool) error { r.calls = append(r.calls, raw); return r.err }

func (r *rawInput) IsTTY() bool { return r.tty }

// sizedOutput is an Output with Node's `columns`, `rows` and "resize".
type sizedOutput struct {
	io.Writer
	columns, rows int
	handlers      []func()
	stopped       int
}

func (s *sizedOutput) Columns() int { return s.columns }

func (s *sizedOutput) Rows() int { return s.rows }

func (s *sizedOutput) OnResize(handler func()) func() {
	s.handlers = append(s.handlers, handler)
	return func() { s.stopped++ }
}

func TestStreamsFindTheOptionalMembersOfTheirInputAndOutput(t *testing.T) {
	input := &rawInput{Reader: bytes.NewReader(nil), tty: true}
	output := &sizedOutput{Writer: io.Discard, columns: 120, rows: 40}
	terminal := terminalOf(input, output)
	if terminal.Input() != io.Reader(input) || terminal.Output() != io.Writer(output) {
		t.Fatal("the terminal's streams are not the ones it was made of")
	}
	if terminal.Columns() != 120 || terminal.Rows() != 40 || !terminal.IsTTY() {
		t.Fatalf("columns %d rows %d tty %v, want 120 40 true", terminal.Columns(), terminal.Rows(), terminal.IsTTY())
	}
	if err := terminal.SetRaw(true); err != nil {
		t.Fatal(err)
	}
	if err := terminal.SetRaw(false); err != nil {
		t.Fatal(err)
	}
	if len(input.calls) != 2 || !input.calls[0] || input.calls[1] {
		t.Fatalf("setRawMode calls = %v, want true then false", input.calls)
	}
	input.err = errors.New("no such mode")
	if err := terminal.SetRaw(true); err == nil || err.Error() != "no such mode" {
		t.Fatalf("SetRaw error = %v, want the stream's", err)
	}
	resized := 0
	stop := terminal.OnResize(func() { resized++ })
	if len(output.handlers) != 1 {
		t.Fatalf("handlers = %d, want the output's resize registered", len(output.handlers))
	}
	output.handlers[0]()
	if resized != 1 {
		t.Fatalf("resized = %d", resized)
	}
	stop()
	if output.stopped != 1 {
		t.Fatalf("stopped = %d, want the output's removal called", output.stopped)
	}
	// a TTY that says it is not one
	input.tty = false
	if terminal.IsTTY() {
		t.Fatal("IsTTY follows the input's answer at each call")
	}
}

func TestStreamsWithoutTheOptionalMembersHaveNoSizeNoRawModeAndNoResizes(t *testing.T) {
	terminal := terminalOf(bytes.NewReader(nil), io.Discard)
	if terminal.Columns() != 0 || terminal.Rows() != 0 || terminal.IsTTY() {
		t.Fatalf("columns %d rows %d tty %v, want 0 0 false (the launcher defaults them to 80x24)", terminal.Columns(), terminal.Rows(), terminal.IsTTY())
	}
	if err := terminal.SetRaw(true); err != nil {
		t.Fatalf("SetRaw on a stream without setRawMode = %v, want a no-op", err)
	}
	called := false
	stop := terminal.OnResize(func() { called = true })
	if stop == nil {
		t.Fatal("OnResize returned no removal")
	}
	stop()
	if called {
		t.Fatal("a resize handler ran for a stream that has none")
	}
}

func TestAStreamsTerminalFillsAMissingInputOrOutputWithTheProcess(t *testing.T) {
	inRead, inWrite := pipeFile(t)
	outRead, outWrite := pipeFile(t)
	swapStdio(t, inRead, outWrite)
	if _, ok := terminalOf(nil, nil).(*ProcessTerminal); !ok {
		t.Fatalf("terminalOf(nil, nil) is %T, want the process's terminal", terminalOf(nil, nil))
	}
	// no input: stdin, read at each call, with the process's raw mode and TTY answer (a pipe: none)
	output := &sizedOutput{Writer: io.Discard, columns: 90, rows: 30}
	terminal := terminalOf(nil, output)
	if _, err := inWrite.WriteString("abc"); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 3)
	if _, err := io.ReadFull(terminal.Input(), got); err != nil || string(got) != "abc" {
		t.Fatalf("stdin read %q, %v", got, err)
	}
	if terminal.IsTTY() {
		t.Fatal("a pipe on stdin is a TTY")
	}
	if terminal.Columns() != 90 || terminal.Rows() != 30 {
		t.Fatalf("size %dx%d, want the given output's", terminal.Columns(), terminal.Rows())
	}
	if terminal.Output() != io.Writer(output) {
		t.Fatal("the given output was replaced")
	}
	// no output: stdout; the input given keeps its own members
	input := &rawInput{Reader: bytes.NewReader(nil), tty: true}
	terminal = terminalOf(input, nil)
	if _, err := io.WriteString(terminal.Output(), "out"); err != nil {
		t.Fatal(err)
	}
	got = make([]byte, 3)
	if _, err := io.ReadFull(outRead, got); err != nil || string(got) != "out" {
		t.Fatalf("stdout got %q, %v", got, err)
	}
	if !terminal.IsTTY() || terminal.Input() != io.Reader(input) {
		t.Fatal("the given input's TTY answer or identity was lost")
	}
	// the process's output has no size from a pipe
	if terminal.Columns() != 0 || terminal.Rows() != 0 {
		t.Fatalf("size %dx%d, want none from a pipe", terminal.Columns(), terminal.Rows())
	}
}
