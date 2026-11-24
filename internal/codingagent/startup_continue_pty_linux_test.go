//go:build linux

package codingagent

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"regexp"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/testenv"
	"github.com/MichaelKinsy/PiG/tui"
)

const (
	startupContinueChildEnv          = "PIG_TEST_STARTUP_CONTINUE_CHILD"
	startupArmedContinueChildEnv     = "PIG_TEST_STARTUP_ARMED_CONTINUE_CHILD"
	rawRestoredShellContinueChildEnv = "PIG_TEST_RAW_RESTORED_SHELL_CONTINUE_CHILD"
	surfaceRearmKittyChildEnv        = "PIG_TEST_SURFACE_REARM_KITTY_CHILD"
)

func stdinIsRawLinux(t *testing.T) bool {
	t.Helper()
	termios, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TCGETS)
	if err != nil {
		childFatalf(t, "read stdin termios: %v", err)
	}
	const rawClearedL = unix.ECHO | unix.ICANON | unix.IEXTEN | unix.ISIG
	const rawClearedI = unix.BRKINT | unix.ICRNL | unix.INPCK | unix.ISTRIP | unix.IXON
	return termios.Lflag&rawClearedL == 0 && termios.Iflag&rawClearedI == 0
}

// setStdinCookedLinux simulates a job-control shell changing termios to cooked mode after an external SIGTSTP stops pig (pig itself never ran rawRestore).
func setStdinCookedLinux(t *testing.T) {
	t.Helper()
	termios, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TCGETS)
	if err != nil {
		childFatalf(t, "read stdin termios: %v", err)
	}
	termios.Lflag |= unix.ICANON | unix.ECHO | unix.ISIG | unix.IEXTEN
	termios.Iflag |= unix.ICRNL | unix.IXON
	if err := unix.IoctlSetTermios(int(os.Stdin.Fd()), unix.TCSETS, termios); err != nil {
		childFatalf(t, "set cooked termios: %v", err)
	}
}

// A SIGCONT that arrives inside the Init startup window (right after tui-started, before interactive-ready) must find the continue handler already installed
// and re-arm the terminal when the owner loop runs.
func TestInitInstallsContinueHandlerBeforeTerminalStart(t *testing.T) {
	runInitPTYChild(t, "TestInitInstallsContinueHandlerBeforeTerminalStartChild", startupContinueChildEnv)
}

func TestInitInstallsContinueHandlerBeforeTerminalStartChild(t *testing.T) {
	report := os.Getenv(startupContinueChildEnv)
	if report == "" {
		t.Skip("runs only as the pty child of TestInitInstallsContinueHandlerBeforeTerminalStart")
	}
	t.Cleanup(func() {
		if t.Failed() {
			_ = os.WriteFile(report, []byte(childFailure), 0o600)
		}
	})
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), NoThemes: true})
	t.Cleanup(func() { _ = m.Stop() })

	// Deterministically pause right after tui-started, restore cooked mode to simulate an external SIGTSTP+fg, and send SIGCONT before Init reaches
	// interactive-ready. Verify by draining uiTaskCh until raw mode is actually restored (not merely checking len(uiTaskCh) > 0, which a first-frame task
	// could also satisfy).
	prev := startupWindowPauseHook
	startupWindowPauseHook = func() {
		setStdinCookedLinux(t)
		if stdinIsRawLinux(t) {
			childFatalf(t, "expected cooked tty after shell stop simulation")
		}
		if err := syscall.Kill(os.Getpid(), syscall.SIGCONT); err != nil {
			childFatalf(t, "send SIGCONT in startup window: %v", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for !stdinIsRawLinux(t) {
			select {
			case task := <-m.uiTaskCh:
				task()
			case <-time.After(10 * time.Millisecond):
				if time.Now().After(deadline) {
					childFatalf(t, "SIGCONT in startup window did not re-arm raw mode via uiTaskCh")
				}
			}
		}
	}
	t.Cleanup(func() { startupWindowPauseHook = prev })

	_ = m.Init(t.Context())
	if !stdinIsRawLinux(t) {
		childFatalf(t, "terminal not in raw mode after Init following startup-window SIGCONT")
	}
}

// Terminal ownership for SIGCONT begins when startTerminal arms the terminal (raw mode, alternate screen, paste, kitty),
// not later at startInteractiveTui(). A SIGTSTP+fg landing between startTerminal and startInteractiveTui re-arms raw mode and writes
// bracketed-paste enable instead of leaving the TUI running in cooked mode on the alternate screen.
func TestInitOwnsTerminalForSIGCONTAsSoonAsStartTerminalArmsIt(t *testing.T) {
	for _, mode := range []string{"fullscreen", "regular"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("PIG_TEST_STARTUP_TUI_MODE", mode)
			captured := runInitPTYChild(t, "TestInitOwnsTerminalForSIGCONTAsSoonAsStartTerminalArmsItChild", startupArmedContinueChildEnv)
			const sentinel = "ARMED_WINDOW_SENTINEL"
			idx := bytes.Index(captured, []byte(sentinel))
			if idx < 0 {
				t.Fatalf("missing %s in child output", sentinel)
			}
			after := captured[idx+len(sentinel):]
			if !bytes.Contains(after, []byte("\x1b[?2004h")) {
				t.Fatalf("mode=%s: SIGCONT right after startTerminal did not re-emit bracketed-paste enable: %q", mode, tailBytes(after, 400))
			}
		})
	}
}

func TestInitOwnsTerminalForSIGCONTAsSoonAsStartTerminalArmsItChild(t *testing.T) {
	report := os.Getenv(startupArmedContinueChildEnv)
	if report == "" {
		t.Skip("runs only as the pty child of TestInitOwnsTerminalForSIGCONTAsSoonAsStartTerminalArmsIt")
	}
	t.Cleanup(func() {
		if t.Failed() {
			_ = os.WriteFile(report, []byte(childFailure), 0o600)
		}
	})
	tuiMode := os.Getenv("PIG_TEST_STARTUP_TUI_MODE")
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), NoThemes: true, TuiMode: tuiMode})
	t.Cleanup(func() { _ = m.Stop() })

	prev := startupTerminalArmedHook
	startupTerminalArmedHook = func() {
		if m.rawRestore == nil {
			childFatalf(t, "rawRestore nil immediately after startTerminal")
		}
		// Simulate the shell setting cooked mode while pig was stopped between startTerminal and startInteractiveTui.
		// Unlike Ctrl+Z, an external SIGTSTP stops pig directly without calling m.rawRestore(), so pig still owns the terminal.
		setStdinCookedLinux(t)
		if stdinIsRawLinux(t) {
			childFatalf(t, "expected cooked tty after shell stop simulation")
		}
		_, _ = os.Stdout.WriteString("ARMED_WINDOW_SENTINEL")
		if err := syscall.Kill(os.Getpid(), syscall.SIGCONT); err != nil {
			childFatalf(t, "send SIGCONT after startTerminal: %v", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for !stdinIsRawLinux(t) {
			select {
			case task := <-m.uiTaskCh:
				task()
			case <-time.After(10 * time.Millisecond):
				if time.Now().After(deadline) {
					childFatalf(t, "mode=%s: SIGCONT right after startTerminal did not queue re-arm (ownership started too late)", tuiMode)
				}
			}
		}
	}
	t.Cleanup(func() { startupTerminalArmedHook = prev })

	_ = m.Init(t.Context())
}

// Simulate a shell that saves a stopped job's raw termios, runs its prompt (writing ESC[?2004l in cooked mode),
// restores the saved raw termios before sending SIGCONT (as fish, mksh/pdksh, and the glibc manual's sample shell do), and verify that
// pig still re-arms ESC[?2004h and redraws its frame on SIGCONT even though termios is already raw.
func TestSIGCONTUnderRawTermiosRestoringShellRearmsAndRedraws(t *testing.T) {
	captured := runInitPTYChild(t, "TestSIGCONTUnderRawTermiosRestoringShellRearmsAndRedrawsChild", rawRestoredShellContinueChildEnv)
	const sentinel = "RAW_SHELL_FG_SENTINEL"
	idx := bytes.Index(captured, []byte(sentinel))
	if idx < 0 {
		t.Fatalf("missing %s in child output", sentinel)
	}
	after := captured[idx+len(sentinel):]
	if !bytes.Contains(after, []byte("\x1b[?2004h")) {
		t.Fatalf("SIGCONT with raw termios already restored did not re-enable bracketed paste: %q", tailBytes(after, 400))
	}
	if !bytes.Contains(after, []byte("\x1b[?1049h")) {
		t.Fatalf("SIGCONT with raw termios already restored did not re-enter alternate screen: %q", tailBytes(after, 400))
	}
	enterIdx := bytes.LastIndex(after, []byte("\x1b[?1049h"))
	if !bytes.Contains(after[enterIdx:], []byte("RAW-SHELL-FRAME-NEEDLE")) {
		t.Fatalf("SIGCONT with raw termios already restored did not redraw the frame after ?1049h: %q", tailBytes(after[enterIdx:], 600))
	}
}

func TestSIGCONTUnderRawTermiosRestoringShellRearmsAndRedrawsChild(t *testing.T) {
	report := os.Getenv(rawRestoredShellContinueChildEnv)
	if report == "" {
		t.Skip("runs only as the pty child of TestSIGCONTUnderRawTermiosRestoringShellRearmsAndRedraws")
	}
	t.Cleanup(func() {
		if t.Failed() {
			_ = os.WriteFile(report, []byte(childFailure), 0o600)
		}
	})
	m := NewInteractiveMode(nil, InteractiveModeOptions{CWD: t.TempDir(), AgentDir: t.TempDir(), NoThemes: true, TuiMode: "fullscreen"})
	t.Cleanup(func() { _ = m.Stop() })

	prev := startupWindowPauseHook
	startupWindowPauseHook = func() {
		m.editor.SetText("RAW-SHELL-FRAME-NEEDLE")
		// Save pig's raw termios, switch to cooked (simulating shell prompt), write ESC[?2004l, then restore the saved raw termios
		// (exactly what fish/mksh/glibc sample shell do on fg) before delivering SIGCONT.
		savedRaw, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), unix.TCGETS)
		if err != nil {
			childFatalf(t, "save raw termios: %v", err)
		}
		cooked := *savedRaw
		cooked.Lflag |= unix.ICANON | unix.ECHO | unix.ISIG | unix.IEXTEN
		cooked.Iflag |= unix.ICRNL | unix.IXON
		if err := unix.IoctlSetTermios(int(os.Stdin.Fd()), unix.TCSETS, &cooked); err != nil {
			childFatalf(t, "set cooked termios: %v", err)
		}
		_, _ = os.Stdout.WriteString("\x1b[?2004l")
		if err := unix.IoctlSetTermios(int(os.Stdin.Fd()), unix.TCSETS, savedRaw); err != nil {
			childFatalf(t, "restore saved raw termios: %v", err)
		}
		if !stdinIsRawLinux(t) {
			childFatalf(t, "expected raw termios before SIGCONT")
		}
		redrawsBefore := m.tuiInst.FullRedraws()
		_, _ = os.Stdout.WriteString("RAW_SHELL_FG_SENTINEL")
		if err := syscall.Kill(os.Getpid(), syscall.SIGCONT); err != nil {
			childFatalf(t, "send SIGCONT: %v", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for m.tuiInst.FullRedraws() <= redrawsBefore {
			select {
			case task := <-m.uiTaskCh:
				task()
			case <-time.After(10 * time.Millisecond):
				if time.Now().After(deadline) {
					childFatalf(t, "SIGCONT with shell-restored raw termios did not trigger re-arm and full redraw (fullRedraws=%d)", m.tuiInst.FullRedraws())
				}
			}
		}
	}
	t.Cleanup(func() { startupWindowPauseHook = prev })

	_ = m.Init(t.Context())
}

// On a frontend surface, rearmTerminal must pop the previous Kitty keyboard stack push before pushing again so repeated external SIGTSTP+fg
// cycles and final exit leave pushes - pops == 0 (never stranding CSI-u flags at the user's shell prompt).
func TestFrontendSurfaceRearmBalancesKittyStack(t *testing.T) {
	master, slave := openTestPTY(t)
	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestFrontendSurfaceRearmBalancesKittyStackChild$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), surfaceRearmKittyChildEnv+"=1", "TERM=xterm-256color", "PI_PROGRAM_STATUS=0")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	finishPTY := testenv.ReadPTYUntilSentinel(t, master, slave, 30*time.Second)
	waitErr := cmd.Wait()
	captured := finishPTY()
	if waitErr != nil || !bytes.Contains(captured, []byte("--- PASS: TestFrontendSurfaceRearmBalancesKittyStackChild")) {
		t.Fatalf("child failed: %v\n%q", waitErr, captured)
	}
	const startMarker = "SURFACE_REARM_BEGIN"
	const endMarker = "SURFACE_REARM_END"
	i0 := bytes.Index(captured, []byte(startMarker))
	i1 := bytes.Index(captured, []byte(endMarker))
	if i0 < 0 || i1 < 0 || i1 <= i0 {
		t.Fatalf("missing markers in child output: %q", captured)
	}
	seg := string(captured[i0+len(startMarker) : i1])
	pushRe := regexp.MustCompile(`\x1b\[>[0-9]+u`)
	popRe := regexp.MustCompile(`\x1b\[<u`)
	pushes := len(pushRe.FindAllString(seg, -1))
	pops := len(popRe.FindAllString(seg, -1))
	if pushes != 4 || pops != 4 {
		t.Fatalf("unbalanced Kitty keyboard stack across 3 surface re-arms + exit: pushes=%d pops=%d (diff=%d, want 4/4)", pushes, pops, pushes-pops)
	}
}

func TestFrontendSurfaceRearmBalancesKittyStackChild(t *testing.T) {
	if os.Getenv(surfaceRearmKittyChildEnv) == "" {
		t.Skip("runs only as the pty child of TestFrontendSurfaceRearmBalancesKittyStack")
	}
	session := &fakeFrontendSession{}
	m, _ := newFrontendProbe(t, &fakeFrontend{session: session}, "regular")
	if m.surface == nil {
		t.Fatal("no surface renderer")
	}
	_, _ = os.Stdout.WriteString("SURFACE_REARM_BEGIN")
	restore, drain, err := tui.EnterRawModeWithDrain()
	if err != nil {
		t.Fatal(err)
	}
	m.rawRestore, m.rawDrain = restore, drain
	for range 3 {
		m.rearmTerminal()
	}
	m.surface.Stop()
	if m.rawRestore != nil {
		m.rawRestore()
		m.rawRestore = nil
	}
	_, _ = os.Stdout.WriteString("SURFACE_REARM_END")
}
