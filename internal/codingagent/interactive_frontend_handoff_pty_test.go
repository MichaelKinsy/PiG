//go:build linux || darwin

package codingagent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/tui"
)

const (
	frontendHandoffChildEnv  = "PIG_TEST_FRONTEND_HANDOFF_CHILD"
	frontendHandoffMarkerEnv = "PIG_TEST_FRONTEND_HANDOFF_MARKER"
)

// TestFrontendSessionLendsTheTerminal drives the real owner path in a child
// process whose stdin and stdout are a pty: the external editor (Ctrl+G) and
// the job-control stop and continue (Ctrl+Z) with a frontend session drawing.
// The session hears Suspend while the terminal is raw and PiG still reads
// it, before the editor runs, and Resume once raw mode is back, after the
// editor ran; the one frame after Resume updates only the editor node with
// the text the handoff left, with no full resend. The child is required
// because the process terminal is bound to os.Stdin at package
// initialization.
func TestFrontendSessionLendsTheTerminal(t *testing.T) {
	master, slave := openTestPTY(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	editor := filepath.Join(dir, "editor.sh")
	script := "#!/bin/sh\nprintf 'edited by editor' > \"$1\"\n: > \"$" + frontendHandoffMarkerEnv + "\"\n"
	if err := os.WriteFile(editor, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestFrontendHandoffChild$", "-test.count=1", "-test.v")
	cmd.Env = append(os.Environ(), frontendHandoffChildEnv+"=1", frontendHandoffMarkerEnv+"="+marker, "TERM=xterm-256color", "VISUAL="+editor, "EDITOR="+editor)
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
	if waitErr != nil || !bytes.Contains(captured.Bytes(), []byte("--- PASS: TestFrontendHandoffChild")) {
		t.Fatalf("child: %v\n%s", waitErr, captured.Bytes())
	}
}

// TestFrontendHandoffChild is the child half of
// TestFrontendSessionLendsTheTerminal.
func TestFrontendHandoffChild(t *testing.T) {
	if os.Getenv(frontendHandoffChildEnv) == "" {
		t.Skip("runs only as the pty child of TestFrontendSessionLendsTheTerminal")
	}
	marker := os.Getenv(frontendHandoffMarkerEnv)
	session := &fakeFrontendSession{}
	m, _ := newFrontendProbe(t, &fakeFrontend{session: session}, "regular")
	if m.surface == nil {
		t.Fatal("no surface renderer")
	}
	restore, drain, err := tui.EnterRawModeWithDrain()
	if err != nil {
		t.Fatal(err)
	}
	m.rawRestore, m.rawDrain = restore, drain
	reader := newInteractiveTerminalReader(t.Context(), os.Stdin)
	m.inputReader = reader
	t.Cleanup(reader.pause)

	raw := func() bool {
		termios, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), termiosRead)
		return err == nil && termios.Lflag&unix.ICANON == 0
	}
	var events []string
	session.onCall = func(call string) {
		if call == "apply" {
			events = append(events, call)
			return
		}
		_, statErr := os.Stat(marker)
		events = append(events, fmt.Sprintf("%s raw=%t reading=%t edited=%t", call, raw(), reader.cancel != nil, statErr == nil))
	}
	editorOps := func(frame frontend.Frame) string {
		var out []string
		for _, op := range frame.Ops {
			text := ""
			if node, ok := op.Node.(frontend.Editor); ok {
				text = node.Text
			}
			out = append(out, fmt.Sprintf("%s %s %s %q", op.Kind, op.Region, op.ID, text))
		}
		return fmt.Sprint(out)
	}

	// Ctrl+G: the editor result arrives as an owner-loop task.
	m.editor.SetText("draft")
	m.tuiInst.Render()
	events = nil
	frames := len(session.frames)
	m.openExternalEditor(t.Context())
	deadline := time.After(testbudget.Wait(t))
	for m.externalEditorActive {
		select {
		case task := <-m.uiTaskCh:
			task()
		case <-deadline:
			t.Fatal("external editor did not post its restart")
		}
	}
	want := []string{"suspend raw=true reading=true edited=false", "resume raw=true reading=false edited=true", "apply"}
	if !slices.Equal(events, want) {
		t.Errorf("editor handoff events = %q, want %q", events, want)
	}
	if len(session.frames) != frames+1 {
		t.Fatalf("editor handoff frames = %d, want 1", len(session.frames)-frames)
	}
	if got, want := editorOps(session.frames[frames]), fmt.Sprint([]string{`update dock editor "edited by editor"`}); got != want {
		t.Errorf("frame after the editor = %s, want %s", got, want)
	}

	// Ctrl+Z: the stop, a change while stopped, and the continuation.
	events = nil
	frames = len(session.frames)
	_ = os.Remove(marker)
	ops := m.suspendOperations()
	ops.stop()
	m.editor.SetText("typed while stopped")
	if err := ops.start(); err != nil {
		t.Fatal(err)
	}
	ops.requestRender()
	want = []string{"suspend raw=true reading=true edited=false", "resume raw=true reading=true edited=false", "apply"}
	if !slices.Equal(events, want) {
		t.Errorf("job-control events = %q, want %q", events, want)
	}
	if len(session.frames) != frames+1 {
		t.Fatalf("job-control frames = %d, want 1", len(session.frames)-frames)
	}
	if got, want := editorOps(session.frames[frames]), fmt.Sprint([]string{`update dock editor "typed while stopped"`}); got != want {
		t.Errorf("frame after the continuation = %s, want %s", got, want)
	}
}
