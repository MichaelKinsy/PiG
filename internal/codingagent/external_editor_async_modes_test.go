//go:build linux || darwin

package codingagent

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

const (
	editorHandoffChildEnv  = "PIG_TEST_EDITOR_HANDOFF_CHILD"
	editorHandoffMarkerEnv = "PIG_TEST_EDITOR_HANDOFF_MARKER"
)

// TestExternalEditorHandoffOrdering proves the stop -> child -> start contract of
// handleOpenExternalEditor (interactive/components/extension-editor.ts:128): the
// renderer is stopped before the editor child runs, and after the child exits,
// on success and on failure alike, the renderer restarts and repaints from the
// owner loop; only a successful edit applies, a failed one reports Pi's
// message. The child half runs on a pty because raw mode needs a terminal.
func TestExternalEditorHandoffOrdering(t *testing.T) {
	for _, tc := range []struct{ name, script string }{
		{"success", "printf 'edited' > \"$1\"\n: > \"$" + editorHandoffMarkerEnv + "\"\n"},
		{"failure", ": > \"$" + editorHandoffMarkerEnv + "\"\nexit 3\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			master, slave := openTestPTY(t)
			dir := t.TempDir()
			marker := filepath.Join(dir, "ran")
			editor := filepath.Join(dir, "editor.sh")
			if err := os.WriteFile(editor, []byte("#!/bin/sh\n"+tc.script), 0o755); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), testbudget.Wait(t))
			defer cancel()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestExternalEditorHandoffChild$", "-test.count=1", "-test.v")
			cmd.Env = append(os.Environ(), editorHandoffChildEnv+"="+tc.name, editorHandoffMarkerEnv+"="+marker, "TERM=xterm-256color", "VISUAL="+editor, "EDITOR="+editor)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			finishPTY := testenv.ReadPTYUntilSentinel(t, master, slave, 30*time.Second)
			waitErr := cmd.Wait()
			var captured bytes.Buffer
			captured.Write(finishPTY())
			if waitErr != nil || !bytes.Contains(captured.Bytes(), []byte("--- PASS: TestExternalEditorHandoffChild")) {
				t.Fatalf("child: %v\n%s", waitErr, captured.Bytes())
			}
		})
	}
}

// editorOutputLog records each renderer write with whether the editor child had
// already run when it happened.
type editorOutputLog struct {
	marker string
	mu     sync.Mutex
	before int
	after  int
}

func (l *editorOutputLog) Write(p []byte) (int, error) {
	_, err := os.Stat(l.marker)
	l.mu.Lock()
	defer l.mu.Unlock()
	if err == nil {
		l.after++
	} else {
		l.before++
	}
	return len(p), nil
}

func (l *editorOutputLog) counts() (before, after int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.before, l.after
}

// TestExternalEditorHandoffChild is the child half of TestExternalEditorHandoffOrdering.
func TestExternalEditorHandoffChild(t *testing.T) {
	mode := os.Getenv(editorHandoffChildEnv)
	if mode == "" {
		t.Skip("runs only as the pty child of TestExternalEditorHandoffOrdering")
	}
	log := &editorOutputLog{marker: os.Getenv(editorHandoffMarkerEnv)}
	m := newUnmountedSwitchTuiProbe(t, InteractiveModeOptions{TuiMode: "regular"}, log)
	m.mountInteractiveTui(true)
	t.Cleanup(func() { m.teardownCurrentTui(); m.stopInteractiveTui(); m.backgroundTasks.Wait() })
	m.editor.SetText("draft")
	m.tuiInst.Render()

	var applied, reported []string
	apply := func(s string) { applied = append(applied, s) }
	report := func(s string) { reported = append(reported, s) }

	log.mu.Lock()
	log.before, log.after = 0, 0
	log.mu.Unlock()
	m.openExternalEditorBuffer(t.Context(), m.externalEditorCommand(), "draft", apply, report)
	if !m.externalEditorActive {
		t.Fatal("the handoff is not active while the editor owns the terminal")
	}
	stopWrites, _ := log.counts()
	if stopWrites == 0 {
		t.Fatal("the renderer was not stopped before the editor child ran")
	}
	// A second key press while the first handoff is active starts no second child.
	m.openExternalEditorBuffer(t.Context(), m.externalEditorCommand(), "again", apply, report)

	select {
	case task := <-m.uiTaskCh:
		if before, after := log.counts(); after != 0 || before != stopWrites {
			t.Fatalf("renderer writes around the child: before=%d (stop wrote %d), after=%d; nothing may restart it before the owner-loop task runs", before, stopWrites, after)
		}
		task()
	case <-time.After(testbudget.Wait(t)):
		t.Fatal("the editor child never posted its completion to the owner loop")
	}
	m.backgroundTasks.Wait()
	if m.externalEditorActive {
		t.Error("the handoff is still active after the owner-loop task")
	}
	if _, after := log.counts(); after == 0 {
		t.Error("the renderer was not restarted and repainted after the editor exited")
	}
	switch mode {
	case "success":
		if len(applied) != 1 || applied[0] != "edited" || len(reported) != 0 {
			t.Errorf("success: applied=%q reported=%q, want one apply of the edit and no report", applied, reported)
		}
	case "failure":
		if len(applied) != 0 || len(reported) != 1 || reported[0] != "External editor exited with code 3; prompt unchanged" {
			t.Errorf("failure: applied=%q reported=%q, want no apply and Pi's exit message", applied, reported)
		}
	}
}
