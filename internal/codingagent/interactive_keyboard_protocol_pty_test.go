//go:build linux

package codingagent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/tui"
)

const keyboardProtocolChildEnv = "PIG_TEST_KEYBOARD_PROTOCOL_CHILD"

// vtKeyboardModel is the part of a terminal that the Kitty keyboard protocol
// depends on: the main and alternate screens each keep their own flag stack
// (https://sw.kovidgoyal.net/kitty/keyboard-protocol/#progressive-enhancement).
// A pop on an empty stack is recorded because a terminal ignores it silently
// while PiG would be disturbing whatever the user's shell had pushed.
type vtKeyboardModel struct {
	alt       bool
	main, top []int
	underflow int
	marks     []vtKeyboardMark
}

type vtKeyboardMark struct {
	name     string
	alt      bool
	main, up []int
}

func (m *vtKeyboardModel) stack() *[]int {
	if m.alt {
		return &m.top
	}
	return &m.main
}

// feed interprets only CSI h/l ?1049, CSI > n u, CSI < n u and the private OSC
// 777 markers the child writes between steps. It skips every other string
// introducer (OSC, APC) so image payloads cannot be misread as CSI.
func (m *vtKeyboardModel) feed(data []byte) {
	for i := 0; i < len(data); i++ {
		if data[i] != 0x1b || i+1 >= len(data) {
			continue
		}
		switch data[i+1] {
		case '[':
			j := i + 2
			for j < len(data) && (data[j] < 0x40 || data[j] > 0x7e) {
				j++
			}
			if j >= len(data) {
				return
			}
			m.csi(string(data[i+2:j]), data[j])
			i = j
		case ']', '_', 'P', '^', 'X':
			j := i + 2
			for j < len(data) && data[j] != 0x07 && (data[j] != 0x1b || j+1 >= len(data) || data[j+1] != '\\') {
				j++
			}
			if j >= len(data) {
				return
			}
			if data[i+1] == ']' {
				if name, ok := strings.CutPrefix(string(data[i+2:j]), "777;mark;"); ok {
					m.marks = append(m.marks, vtKeyboardMark{name, m.alt, slicesCloneInts(m.main), slicesCloneInts(m.top)})
				}
			}
			i = j
		default:
			i++
		}
	}
}

func slicesCloneInts(in []int) []int { return append([]int{}, in...) }

func (m *vtKeyboardModel) csi(params string, final byte) {
	switch final {
	case 'h', 'l':
		for p := range strings.SplitSeq(strings.TrimPrefix(params, "?"), ";") {
			if strings.HasPrefix(params, "?") && p == "1049" {
				m.alt = final == 'h'
			}
		}
	case 'u':
		switch {
		case strings.HasPrefix(params, ">"):
			flags, _ := strconv.Atoi(params[1:])
			*m.stack() = append(*m.stack(), flags)
		case strings.HasPrefix(params, "<"):
			count := 1
			if n, err := strconv.Atoi(params[1:]); err == nil {
				count = n
			}
			for range count {
				if len(*m.stack()) == 0 {
					m.underflow++
					continue
				}
				*m.stack() = (*m.stack())[:len(*m.stack())-1]
			}
		}
	}
}

// TestKeyboardProtocolDriverBalancesPerScreenStacks drives the real owner path
// (raw mode, fullscreen mount, switchTuiMode, suspend/resume, the external
// editor, stopInteractiveTui) in a child process whose stdin and stdout are a pty, and
// replays the child's terminal output through vtKeyboardModel. Fullscreen must
// run with Kitty flags 7 on the alternate screen, the shell and the external
// editor must see no flags, and both stacks must be empty after exit for both
// fullscreenExitOutput values. Pi reaches the same states by pushing on the
// active screen (tui.ts start/stop, terminal.ts queryAndEnableKittyProtocol).
//
// The child process is required because the process terminal is bound to
// os.Stdout at package initialization.
func TestKeyboardProtocolDriverBalancesPerScreenStacks(t *testing.T) {
	for _, exitOutput := range []string{"transcript", "resume-hint"} {
		t.Run(exitOutput, func(t *testing.T) {
			model, output := runKeyboardProtocolChild(t, exitOutput)
			type stacks struct {
				alt         bool
				main, upper []int
			}
			seven := []int{7}
			want := []struct {
				name string
				stacks
			}{
				{"raw", stacks{false, seven, nil}},
				{"fullscreen", stacks{true, nil, seven}},
				{"regular", stacks{false, seven, nil}},
				{"fullscreen-again", stacks{true, nil, seven}},
				{"suspended", stacks{false, nil, nil}},
				{"resumed", stacks{true, nil, seven}},
				{"editor", stacks{false, nil, nil}},
				{"edited", stacks{true, nil, seven}},
				{"exited", stacks{false, nil, nil}},
			}
			if len(model.marks) != len(want) {
				t.Fatalf("marks = %d, want %d; output %q", len(model.marks), len(want), output)
			}
			for i, w := range want {
				got := model.marks[i]
				if got.name != w.name || got.alt != w.alt || fmt.Sprint(got.main) != fmt.Sprint(w.main) || fmt.Sprint(got.up) != fmt.Sprint(w.upper) {
					t.Errorf("mark %q: alt=%v main=%v alt-stack=%v; want %q alt=%v main=%v alt-stack=%v",
						got.name, got.alt, got.main, got.up, w.name, w.alt, w.main, w.upper)
				}
			}
			if model.alt || len(model.main) != 0 || len(model.top) != 0 {
				t.Errorf("final state alt=%v main=%v alt-stack=%v, want empty main screen", model.alt, model.main, model.top)
			}
			if model.underflow != 0 {
				t.Errorf("%d pops hit an empty stack", model.underflow)
			}
		})
	}
}

func runKeyboardProtocolChild(t *testing.T, exitOutput string) (*vtKeyboardModel, []byte) {
	t.Helper()
	master, slave := openTestPTY(t)

	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestKeyboardProtocolDriverChild$", "-test.count=1")
	cmd.Env = append(os.Environ(), keyboardProtocolChildEnv+"="+exitOutput, "TERM=xterm-256color", "VISUAL=true", "EDITOR=true")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = slave.Close()

	var captured bytes.Buffer
	copied := make(chan struct{})
	go func() {
		defer close(copied)
		_, _ = io.Copy(&captured, master)
	}()
	waitErr := cmd.Wait()
	select {
	case <-copied:
	case <-time.After(5 * time.Second):
		_ = master.Close()
		<-copied
	}
	if waitErr != nil {
		t.Fatalf("child failed: %v\n%q", waitErr, captured.Bytes())
	}
	model := &vtKeyboardModel{}
	model.feed(captured.Bytes())
	return model, captured.Bytes()
}

// TestKeyboardProtocolDriverChild is the child half of
// TestKeyboardProtocolDriverBalancesPerScreenStacks.
func TestKeyboardProtocolDriverChild(t *testing.T) {
	exitOutput := os.Getenv(keyboardProtocolChildEnv)
	if exitOutput == "" {
		t.Skip("runs only as the pty child of TestKeyboardProtocolDriverBalancesPerScreenStacks")
	}
	mark := func(name string) { _, _ = os.Stdout.WriteString("\x1b]777;mark;" + name + "\x07") }

	opts := InteractiveModeOptions{
		CWD: t.TempDir(), AgentDir: t.TempDir(),
		Model:    &ai.Model{ID: "m", DisplayName: "m", Capabilities: ai.ModelCapabilities{ContextWindow: 8000}},
		Settings: Settings{TuiMode: "fullscreen", FullscreenExitOutput: FullscreenExitOutput(exitOutput)},
	}
	opts.TuiMode = "fullscreen"
	m := newUnmountedSwitchTuiProbe(t, opts, os.Stdout)

	// Run enters raw mode (pushing on the main screen) before the first mount.
	restore, drain, err := tui.EnterRawModeWithDrain()
	if err != nil {
		t.Fatal(err)
	}
	m.rawRestore, m.rawDrain = restore, drain
	mark("raw")
	m.mountInteractiveTui(true)
	mark("fullscreen")

	if !m.switchTuiMode("regular", false, true) {
		t.Fatal("switch to regular refused")
	}
	mark("regular")
	if !m.switchTuiMode("fullscreen", false, true) {
		t.Fatal("switch to fullscreen refused")
	}
	mark("fullscreen-again")

	// Suspend and resume.
	ops := m.suspendOperations()
	ops.stop()
	mark("suspended")
	if err := ops.start(); err != nil {
		t.Fatal(err)
	}
	mark("resumed")

	// External editor: the stop runs before the editor process starts and the
	// restart runs as an owner-loop task after it exits. The child has no owner
	// loop, so it runs that task here.
	edited := make(chan struct{})
	m.openExternalEditorBuffer(t.Context(), m.externalEditorCommand(), "draft", func(string) { close(edited) })
	mark("editor")
	select {
	case task := <-m.uiTaskCh:
		task()
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("external editor did not post its restart")
	}
	m.backgroundTasks.Wait()
	select {
	case <-edited:
	default:
		t.Fatal("external editor result was not applied")
	}
	mark("edited")

	m.stopInteractiveTui()
	mark("exited")
}
