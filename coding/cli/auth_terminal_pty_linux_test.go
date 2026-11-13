//go:build linux

package cli

// pi: packages/coding-agent/src/cli/auth-terminal.ts (readSecret and the process terminal, on a pseudo-terminal)

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/internal/terminalquestion"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

const secretPrompt = "Secret: "

// syncBuffer is an output writer the test reads while readSecret's goroutine may still write.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func termiosOf(t *testing.T, tty *os.File) unix.Termios {
	t.Helper()
	termios, err := unix.IoctlGetTermios(int(tty.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatalf("read terminal modes: %v", err)
	}
	return *termios
}

// waitForRawMode returns once readSecret has put the terminal in raw mode: it writes its prompt first, so the prompt is no proof.
func waitForRawMode(t *testing.T, tty *os.File) unix.Termios {
	t.Helper()
	deadline := time.Now().Add(testbudget.Wait(t))
	for {
		termios := termiosOf(t, tty)
		if termios.Lflag&(unix.ECHO|unix.ICANON) == 0 {
			return termios
		}
		if time.Now().After(deadline) {
			t.Fatal("terminal never entered raw mode")
		}
		time.Sleep(time.Millisecond)
	}
}

type secretResult struct {
	value string
	err   error
}

func startReadSecret(ctx context.Context, input *os.File, output *syncBuffer, onInterrupt func()) <-chan secretResult {
	done := make(chan secretResult, 1)
	go func() {
		value, err := readSecret(ctx, input, output, secretPrompt, onInterrupt)
		done <- secretResult{value, err}
	}()
	return done
}

func awaitSecret(t *testing.T, done <-chan secretResult) secretResult {
	t.Helper()
	select {
	case got := <-done:
		return got
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("readSecret did not return")
		return secretResult{}
	}
}

func writeMaster(t *testing.T, master *os.File, data string) {
	t.Helper()
	if _, err := master.WriteString(data); err != nil {
		t.Fatalf("type into the terminal: %v", err)
	}
}

func TestReadSecretReadsAHiddenLineInRawModeAndRestoresTheTerminal(t *testing.T) {
	master, slave := openPTY(t, 24, 80)
	defer func() { _ = master.Close(); _ = slave.Close() }()
	cooked := termiosOf(t, slave)
	if cooked.Lflag&unix.ECHO == 0 || cooked.Lflag&unix.ICANON == 0 {
		t.Fatalf("a new terminal is not cooked: %#x", cooked.Lflag)
	}
	var out syncBuffer
	interrupts := atomic.Int32{}
	done := startReadSecret(t.Context(), slave, &out, func() { interrupts.Add(1) })
	raw := waitForRawMode(t, slave)
	if raw.Lflag&unix.ECHO != 0 || raw.Lflag&unix.ICANON != 0 {
		t.Fatalf("typed secrets echo: %#x", raw.Lflag)
	}
	writeMaster(t, master, "pass\x7fw\r")
	got := awaitSecret(t, done)
	if got.err != nil || got.value != "pasw" {
		t.Fatalf("secret %q, %v", got.value, got.err)
	}
	if after := termiosOf(t, slave); after != cooked {
		t.Fatalf("terminal left raw: %#v, was %#v", after, cooked)
	}
	if text := out.String(); text != secretPrompt+"\n" {
		t.Fatalf("output %q", text)
	}
	if interrupts.Load() != 0 {
		t.Fatalf("interrupt handler ran %d times", interrupts.Load())
	}
}

func TestReadSecretStopsAtEnterAndDropsTheRestOfTheChunk(t *testing.T) {
	master, slave := openPTY(t, 24, 80)
	defer func() { _ = master.Close(); _ = slave.Close() }()
	var out syncBuffer
	done := startReadSecret(t.Context(), slave, &out, func() {})
	waitForRawMode(t, slave)
	writeMaster(t, master, "ab\ncd")
	if got := awaitSecret(t, done); got.err != nil || got.value != "ab" {
		t.Fatalf("secret %q, %v", got.value, got.err)
	}
}

func TestReadSecretCtrlCRunsTheInterruptHandlerWhichEndsThePrompt(t *testing.T) {
	master, slave := openPTY(t, 24, 80)
	defer func() { _ = master.Close(); _ = slave.Close() }()
	cooked := termiosOf(t, slave)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	interrupts := atomic.Int32{}
	var out syncBuffer
	done := startReadSecret(ctx, slave, &out, func() { interrupts.Add(1); cancel() })
	waitForRawMode(t, slave)
	writeMaster(t, master, "ab\x03")
	got := awaitSecret(t, done)
	if !errors.Is(got.err, errLoginCancelled) || got.value != "" {
		t.Fatalf("secret %q, %v", got.value, got.err)
	}
	if interrupts.Load() != 1 {
		t.Fatalf("interrupt handler ran %d times", interrupts.Load())
	}
	if after := termiosOf(t, slave); after != cooked {
		t.Fatalf("terminal left raw: %#v", after)
	}
	if text := out.String(); text != secretPrompt+"\n" {
		t.Fatalf("output %q", text)
	}
}

func TestReadSecretCtrlCDoesNotEndThePromptWhenTheHandlerLeavesItRunning(t *testing.T) {
	master, slave := openPTY(t, 24, 80)
	defer func() { _ = master.Close(); _ = slave.Close() }()
	interrupted := make(chan struct{}, 4)
	var out syncBuffer
	done := startReadSecret(t.Context(), slave, &out, func() { interrupted <- struct{}{} })
	waitForRawMode(t, slave)
	writeMaster(t, master, "ab\x03")
	select {
	case <-interrupted:
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("Ctrl+C did not reach the interrupt handler")
	}
	writeMaster(t, master, "c\r")
	if got := awaitSecret(t, done); got.err != nil || got.value != "abc" {
		t.Fatalf("secret %q, %v", got.value, got.err)
	}
}

func TestReadSecretContextCancellationRestoresTheTerminalWithoutTheInterruptHandler(t *testing.T) {
	master, slave := openPTY(t, 24, 80)
	defer func() { _ = master.Close(); _ = slave.Close() }()
	cooked := termiosOf(t, slave)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	interrupts := atomic.Int32{}
	var out syncBuffer
	done := startReadSecret(ctx, slave, &out, func() { interrupts.Add(1) })
	waitForRawMode(t, slave)
	writeMaster(t, master, "half")
	cancel()
	got := awaitSecret(t, done)
	if !errors.Is(got.err, errLoginCancelled) || got.value != "" {
		t.Fatalf("secret %q, %v", got.value, got.err)
	}
	if after := termiosOf(t, slave); after != cooked {
		t.Fatalf("terminal left raw: %#v", after)
	}
	if text := out.String(); text != secretPrompt+"\n" {
		t.Fatalf("output %q", text)
	}
	if interrupts.Load() != 0 {
		t.Fatalf("interrupt handler ran %d times", interrupts.Load())
	}
}

func TestReadSecretDropsAByteTypedAfterTheContextEnded(t *testing.T) {
	master, slave := openPTY(t, 24, 80)
	defer func() { _ = master.Close(); _ = slave.Close() }()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	interrupts := atomic.Int32{}
	var out syncBuffer
	done := startReadSecret(ctx, slave, &out, func() { interrupts.Add(1) })
	waitForRawMode(t, slave)
	cancel()
	if got := awaitSecret(t, done); !errors.Is(got.err, errLoginCancelled) {
		t.Fatalf("secret %q, %v", got.value, got.err)
	}
	// The abandoned read is still blocked in the kernel. Cooked again, it returns when a line arrives; Ctrl+C is then ordinary data and must not reach the handler.
	termios := termiosOf(t, slave)
	termios.Lflag &^= unix.ISIG
	if err := unix.IoctlSetTermios(int(slave.Fd()), unix.TCSETS, &termios); err != nil {
		t.Fatalf("clear ISIG: %v", err)
	}
	writeMaster(t, master, "\x03\n")
	time.Sleep(200 * time.Millisecond)
	if interrupts.Load() != 0 {
		t.Fatalf("a byte typed after the prompt ended reached the interrupt handler")
	}
}

func TestReadSecretNeverTouchesTheTerminalWhenTheContextHasEnded(t *testing.T) {
	master, slave := openPTY(t, 24, 80)
	defer func() { _ = master.Close(); _ = slave.Close() }()
	cooked := termiosOf(t, slave)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var out syncBuffer
	got := awaitSecret(t, startReadSecret(ctx, slave, &out, func() { t.Error("interrupt handler ran") }))
	if !errors.Is(got.err, errLoginCancelled) || got.value != "" {
		t.Fatalf("secret %q, %v", got.value, got.err)
	}
	if text := out.String(); text != "" {
		t.Fatalf("prompt written for a cancelled login: %q", text)
	}
	if after := termiosOf(t, slave); after != cooked {
		t.Fatalf("terminal modes changed: %#v", after)
	}
}

func TestReadSecretFailsWithoutRawModeWhenInputIsNotATerminal(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close(); _ = writer.Close() }()
	var out syncBuffer
	got := awaitSecret(t, startReadSecret(t.Context(), reader, &out, func() {}))
	if got.err == nil || errors.Is(got.err, errLoginCancelled) {
		t.Fatalf("secret %q, err %v", got.value, got.err)
	}
	if text := out.String(); text != secretPrompt {
		t.Fatalf("output %q", text)
	}
}

func TestReadSecretReturnsAWriteErrorBeforeTouchingTheTerminal(t *testing.T) {
	master, slave := openPTY(t, 24, 80)
	defer func() { _ = master.Close(); _ = slave.Close() }()
	cooked := termiosOf(t, slave)
	broken := errors.New("output closed")
	if _, err := readSecret(t.Context(), slave, failingWriter{broken}, secretPrompt, func() {}); !errors.Is(err, broken) {
		t.Fatalf("err = %v", err)
	}
	if after := termiosOf(t, slave); after != cooked {
		t.Fatalf("terminal modes changed: %#v", after)
	}
}

func TestReadSecretReturnsTheInputErrorAndWritesTheNewlineWhenTheTerminalCloses(t *testing.T) {
	master, slave := openPTY(t, 24, 80)
	defer func() { _ = slave.Close() }()
	var out syncBuffer
	done := startReadSecret(t.Context(), slave, &out, func() { t.Error("interrupt handler ran") })
	waitForRawMode(t, slave)
	if err := master.Close(); err != nil {
		t.Fatal(err)
	}
	got := awaitSecret(t, done)
	if got.err == nil || errors.Is(got.err, errLoginCancelled) || got.value != "" {
		t.Fatalf("secret %q, err %v", got.value, got.err)
	}
	if text := out.String(); text != secretPrompt+"\n" {
		t.Fatalf("output %q", text)
	}
}

func TestLoginTerminalSecretReadsStdinAndWritesTheNewlineToItsOutput(t *testing.T) {
	master, slave := openPTY(t, 24, 80)
	defer func() { _ = master.Close(); _ = slave.Close() }()
	stdin := os.Stdin
	os.Stdin = slave
	defer func() { os.Stdin = stdin }()
	var out syncBuffer
	interrupts := atomic.Int32{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	terminal := processLoginTerminal{reader: terminalquestion.NewReader(bytes.NewReader(nil)), output: &out, onInterrupt: func() { interrupts.Add(1); cancel() }}
	type answer struct {
		value string
		err   error
	}
	typed := make(chan answer, 1)
	go func() {
		value, err := terminal.Secret(ctx, secretPrompt)
		typed <- answer{value, err}
	}()
	waitForRawMode(t, slave)
	writeMaster(t, master, "hunter2\r")
	select {
	case got := <-typed:
		if got.err != nil || got.value != "hunter2" || out.String() != secretPrompt+"\n" {
			t.Fatalf("secret %q, %v, output %q", got.value, got.err, out.String())
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("Secret did not return")
	}

	again := make(chan answer, 1)
	go func() {
		value, err := terminal.Secret(ctx, secretPrompt)
		again <- answer{value, err}
	}()
	waitForRawMode(t, slave)
	writeMaster(t, master, "\x03")
	select {
	case got := <-again:
		if !errors.Is(got.err, errLoginCancelled) || interrupts.Load() != 1 {
			t.Fatalf("secret %q, %v, interrupts %d", got.value, got.err, interrupts.Load())
		}
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("Secret did not return after Ctrl+C")
	}
}

func TestLoginTerminalInteractiveNeedsBothStdinAndStdoutToBeTerminals(t *testing.T) {
	master, slave := openPTY(t, 24, 80)
	defer func() { _ = master.Close(); _ = slave.Close() }()
	file, err := os.Create(filepath.Join(t.TempDir(), "plain"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	stdin, stdout := os.Stdin, os.Stdout
	defer func() { os.Stdin, os.Stdout = stdin, stdout }()
	tests := []struct {
		name          string
		in, out       *os.File
		wantInteracts bool
	}{
		{"both terminals", slave, slave, true},
		{"stdin only", slave, file, false},
		{"stdout only", file, slave, false},
		{"neither", file, file, false},
	}
	for _, tt := range tests {
		os.Stdin, os.Stdout = tt.in, tt.out
		if got := (processLoginTerminal{}).Interactive(); got != tt.wantInteracts {
			t.Errorf("%s: Interactive = %v", tt.name, got)
		}
	}
}
