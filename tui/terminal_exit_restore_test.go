//go:build linux || darwin

package tui

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const (
	exitRestoreModeEnv = "PIG_TUI_EXIT_RESTORE_MODE"
	// exitRestoreReady ends the startup output: the fixture starts the terminal and the alternate screen, writes it, then exits.
	exitRestoreReady = "\x1b]0;exit-restore-ready\x07"
)

// runExitRestoreFixture re-executes the test binary as packages/tui/test/fixtures/exit-without-stop.ts does with node: a
// process that starts a TUI on a pseudo-terminal (alternate screen, mouse, raw mode, bracketed paste, Kitty keyboard
// protocol) and exits with os.Exit, with or without stopping first. It returns what the process wrote after startup.
func runExitRestoreFixture(t *testing.T, mode string, wantStatus int) string {
	t.Helper()
	master, slave := openTestPTY(t)
	helper := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestExitRestoreFixture$", "-test.count=1")
	// TMUX, STY and ZELLIJ are unset and TERM is xterm-256color as in the Pi test's environment.
	env := make([]string, 0, len(os.Environ())+3)
	for _, entry := range os.Environ() {
		if name, _, _ := strings.Cut(entry, "="); name != "TMUX" && name != "STY" && name != "ZELLIJ" && name != "TERM" && name != "PI_PROGRAM_STATUS" {
			env = append(env, entry)
		}
	}
	env = append(env, "TERM=xterm-256color", "PI_PROGRAM_STATUS=0", exitRestoreModeEnv+"="+mode)
	helper.Env = env
	helper.Stdin, helper.Stdout = slave, slave
	var stderr bytes.Buffer
	helper.Stderr = &stderr
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()
	collected := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(master) // ends with EIO once the helper, the last holder of the slave, is gone
		collected <- data
	}()
	waited := make(chan error, 1)
	go func() { waited <- helper.Wait() }()
	select {
	case err := <-waited:
		if status := helper.ProcessState.ExitCode(); status != wantStatus {
			t.Fatalf("fixture exited %d (%v), want %d\n%s", status, err, wantStatus, &stderr)
		}
	case <-time.After(30 * time.Second):
		_ = helper.Process.Kill()
		t.Fatal("fixture did not exit")
	}
	output := string(<-collected)
	_, after, ok := strings.Cut(output, exitRestoreReady)
	if !ok {
		t.Fatalf("fixture never reached its ready marker; output %q, stderr %s", output, &stderr)
	}
	return after
}

// TestExitRestoreFixture is the helper process of the tests below. It does nothing unless the mode is set.
func TestExitRestoreFixture(t *testing.T) {
	mode := os.Getenv(exitRestoreModeEnv)
	if mode == "" {
		t.Skip("helper process of TestTerminalRestoreOnProcessExit")
	}
	// exit-without-stop.ts: new TuiAltScreen(new ProcessTerminal(), false, undefined, { mouse: true }); tui.start().
	// The interactive driver's order: raw mode (bracketed paste, Kitty push) first, then the renderer.
	restore, drain, err := EnterRawModeWithDrain()
	if err != nil {
		t.Fatal(err)
	}
	mouse := true
	alt := NewTuiAltScreen(nil, nil, "", TuiAltScreenOptions{Mouse: &mouse})
	alt.Start() // paints the first frame before the marker; StartRequestingRender would leave it to a later turn
	_, _ = os.Stdout.WriteString(exitRestoreReady)
	switch mode {
	case "return":
		// Main returning: the deferred handler runs, as Pi's 'exit' event fires when the event loop drains.
		func() { defer RestoreTerminalOnExit() }()
		os.Exit(0)
	case "panic":
		// A panic that unwinds through the deferred handler, as an uncaught exception reaches Pi's 'exit' event. The runtime exits 2 after it.
		func() {
			defer RestoreTerminalOnExit()
			panic("exit-restore fixture panic")
		}()
	}
	if mode == "stop" {
		// A normal stop, in the driver's order: drain (which pops the keyboard protocol), the renderer, then raw-mode restore.
		_ = drain()
		alt.StopWithOptions(StopOptions{PreserveScreen: true})
		restore()
	}
	// process.exit(0) after stop() must not restore a second time, and without it the exit handler must.
	RestoreTerminalOnExit()
	os.Exit(0)
}

func countOf(haystack, needle string) int { return strings.Count(haystack, needle) }

// Regression for #11170 (packages/tui/test/exit-restore.test.ts "restores terminal modes when the process exits without
// stop()"): exit paths that skip stop() left mouse tracking, focus reporting, the Kitty keyboard protocol, bracketed paste,
// and the alternate screen enabled.
func TestTerminalRestoreOnProcessExit(t *testing.T) {
	assertExitRestoreOutput(t, runExitRestoreFixture(t, "exit", 0))
}

// Pi's 'exit' event also fires when the event loop drains and when an uncaught exception ends the process; the Go handler is
// deferred in coding/cli.Main, so it runs on a return and on a panic unwinding through it, with the restore before the
// runtime's panic report.
func TestTerminalRestoreOnReturnAndOnPanicUnwinding(t *testing.T) {
	assertExitRestoreOutput(t, runExitRestoreFixture(t, "return", 0))
	// The testing package reports the panic on the terminal too; that report must come after the restore, not before it.
	output := runExitRestoreFixture(t, "panic", 2)
	const suffix = "\x1b[?1049l\x1b[?25h"
	end := strings.Index(output, suffix)
	if end < 0 {
		t.Fatalf("panic run never restored: %q", output)
	}
	end += len(suffix)
	if report := output[end:]; !strings.Contains(report, "TestExitRestoreFixture") {
		t.Errorf("the panic report did not follow the restore: %q", output)
	}
	assertExitRestoreOutput(t, output[:end])
}

func assertExitRestoreOutput(t *testing.T, output string) {
	t.Helper()
	for _, sequence := range []string{"\x1b[?1006l", "\x1b[?1004l", "\x1b[?1003l", "\x1b[?1000l", "\x1b[<u", "\x1b[?2004l"} {
		if got := countOf(output, sequence); got != 1 {
			t.Errorf("%q written %d times, want 1; output %q", sequence, got, output)
		}
	}
	if strings.Index(output, "\x1b[<u") > strings.Index(output, "\x1b[?1049l") {
		t.Errorf("Kitty pop must precede leaving alt screen: %q", output)
	}
	if !strings.HasSuffix(output, "\x1b[?1049l\x1b[?25h") {
		t.Errorf("output must end with the alternate-screen exit and cursor show: %q", output)
	}
	// TuiAltScreen's prefix, in Pi's order: end synchronized output, mouse off, autowrap on, cursor shown, then the terminal's resets.
	want := "\x1b[?2026l\x1b[?1006l\x1b[?1004l\x1b[?1003l\x1b[?1002l\x1b[?1000l\x1b[?7h\x1b[?25h\x1b[?2004l\x1b[<u\x1b[?1049l\x1b[?25h"
	if output != want {
		t.Errorf("exit restore wrote %q, want %q", output, want)
	}
}

// packages/tui/test/exit-restore.test.ts "does not restore twice after a normal stop()": the exit handler is a no-op after
// a stop, so each reset appears exactly once.
func TestTerminalRestoreOnProcessExitDoesNotRepeatAfterStop(t *testing.T) {
	output := runExitRestoreFixture(t, "stop", 0)
	for _, sequence := range []string{"\x1b[?1006l", "\x1b[?1004l", "\x1b[<u", "\x1b[?2004l", "\x1b[?1049l"} {
		if got := countOf(output, sequence); got != 1 {
			t.Errorf("%q written %d times after a normal stop, want 1; output %q", sequence, got, output)
		}
	}
}

type exitRestoreBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *exitRestoreBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}

func (b *exitRestoreBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}

// isolateExitRestoreRegistry gives a test that calls RestoreTerminalOnExit in-process the fresh registry Pi's per-process 'exit'
// handlers have: renderers other tests started without stopping stay out of its output, and its own entries do not leak out.
func isolateExitRestoreRegistry(t *testing.T) {
	t.Helper()
	reset := func() {
		exitRestoreRegistry.mu.Lock()
		exitRestoreRegistry.terminals, exitRestoreRegistry.altScreens = nil, nil
		exitRestoreRegistry.mu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

// The main-screen renderer has no alternate screen: Pi's TuiBase prefix is the color-scheme report reset and the cursor
// show, then the terminal's progress clear, bracketed-paste reset and Kitty pop. A second call writes nothing, the terminal is
// cooked again, and the restore that a normal stop runs afterwards finds nothing registered.
func TestRestoreTerminalOnExitMainScreenWritesOnceAndRestoresCookedMode(t *testing.T) {
	isolateExitRestoreRegistry(t)
	t.Setenv("PI_PROGRAM_STATUS", "0")
	master, slave := openTestPTY(t)
	_ = master
	var out exitRestoreBuffer
	terminal := NewProcessTerminalWithOutput(slave, slave, &out)
	terminal.progressKeepalive = time.Hour
	restore, err := terminal.enterRawMode(false)
	if err != nil {
		t.Fatal(err)
	}
	screen := NewTuiMainScreen(terminal, nil, "")
	screen.SetTerminalColorSchemeNotifications(true)
	terminal.SetProgress(true)
	started := len(out.String())

	RestoreTerminalOnExit()
	want := "\x1b[?2031l\x1b[?25h" + terminalProgressClearSeq + "\x1b[?2004l" + keyboardProtocolPop
	if got := out.String()[started:]; got != want {
		t.Errorf("exit restore wrote %q, want %q", got, want)
	}
	if state, err := unix.IoctlGetTermios(int(slave.Fd()), ttyGetAttributes); err != nil || state.Lflag&unix.ICANON == 0 {
		t.Errorf("terminal not back in cooked mode: %v, %v", state, err)
	}
	afterExit := len(out.String())
	RestoreTerminalOnExit()
	if got := out.String()[afterExit:]; got != "" {
		t.Errorf("a second exit restore wrote %q", got)
	}
	terminal.SetProgress(false)
	restore()
}

// The alternate screen's prefix and suffix wrap the base prefix (tui-alt-screen.ts getExitRestorePrefix/Suffix) and the color-scheme
// report reset leads the base prefix; modifyOtherKeys (no Kitty reply) follows the Kitty pop (terminal.ts restoreOnExit). A frame
// a renderer still draws after the handler ran writes nothing: it must not repaint over a crash report or hide the cursor again.
func TestRestoreTerminalOnExitAltScreenWithColorSchemeReportsAndModifyOtherKeys(t *testing.T) {
	isolateExitRestoreRegistry(t)
	t.Setenv("PI_PROGRAM_STATUS", "0")
	master, slave := openTestPTY(t)
	_ = master
	var out exitRestoreBuffer
	terminal := NewProcessTerminalWithOutput(slave, slave, &out)
	restore, err := terminal.enterRawMode(false)
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	modifyOtherKeysActive.Store(true)
	mouse := true
	alt := NewTuiAltScreen(terminal, nil, "", TuiAltScreenOptions{Mouse: &mouse})
	alt.SetTerminalColorSchemeNotifications(true)
	alt.Start()
	defer alt.StopWithOptions(StopOptions{PreserveScreen: true})
	started := len(out.String())

	RestoreTerminalOnExit()
	want := "\x1b[?2026l" + altDisableMouse + "\x1b[?7h" + "\x1b[?2031l\x1b[?25h" + "\x1b[?2004l" + keyboardProtocolPop + modifyOtherKeysDisable + "\x1b[?1049l\x1b[?25h"
	if got := out.String()[started:]; got != want {
		t.Errorf("exit restore wrote %q, want %q", got, want)
	}
	afterExit := len(out.String())
	alt.Render()
	alt.RequestImmediateRender()
	alt.RepaintAll()
	terminal.HideCursor()
	terminal.Write("late frame")
	if got := out.String()[afterExit:]; got != "" {
		t.Errorf("writes after the exit restore reached the terminal: %q", got)
	}
}

// packages/tui/src/terminal.ts:89 restoreOnExit(before, after): the Terminal member writes before, then its own resets, then after, in
// one write, so a caller holding a Terminal gets the same bytes the registry's exit handler writes. The surface terminal has no
// process modes of its own and writes nothing.
func TestTerminalInterfaceRestoreOnExitWritesBeforeOwnResetsAfter(t *testing.T) {
	t.Setenv("PI_PROGRAM_STATUS", "0")
	master, slave := openTestPTY(t)
	_ = master
	var out exitRestoreBuffer
	process := NewProcessTerminalWithOutput(slave, slave, &out)
	restore, err := process.enterRawMode(false)
	if err != nil {
		t.Fatal(err)
	}
	var terminal Terminal = process
	started := len(out.String())
	terminal.RestoreOnExit("<before>", "<after>")
	want := "<before>" + "\x1b[?2004l" + keyboardProtocolPop + "<after>"
	if got := out.String()[started:]; got != want {
		t.Errorf("Terminal.RestoreOnExit wrote %q, want %q", got, want)
	}
	restore()
	(&surfaceTerminal{}).RestoreOnExit("<before>", "<after>") // writes nothing and must not panic
}
