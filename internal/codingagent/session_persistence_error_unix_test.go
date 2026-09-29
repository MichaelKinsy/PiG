//go:build !windows

package codingagent

import (
	"errors"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi SessionManager exposes Node filesystem errors without Go-specific wrappers. The append case is _persist's appendFileSync (session-manager.ts:1166,1185). The flush case is writeSessionLines, whose Node counterpart is openSync(file, "w") in _rewriteFile (session-manager.ts:1126); Node 24.19.0 reports EISDIR for both on a directory. _persist's first flush opens with "wx" (session-manager.ts:1175), which reports EEXIST for an existing path; writeSessionLines truncates instead, so this test does not claim that path. An attempted file open on a directory fails regardless of the test user's privileges.
func TestSessionPersistenceErrorsMatchNode(t *testing.T) {
	for _, op := range []string{"append", "flush"} {
		t.Run(op, func(t *testing.T) {
			path := t.TempDir()
			var err error
			if op == "append" {
				err = appendSessionLine(path, []byte("{}"))
			} else {
				err = writeSessionLines(path, [][]byte{[]byte("{}")})
			}
			want := "EISDIR: illegal operation on a directory, open '" + path + "'"
			if err == nil || err.Error() != want {
				t.Fatalf("%s error=%v, want %s", op, err, want)
			}
			if !errors.Is(err, syscall.EISDIR) {
				t.Fatalf("filesystem errno was lost: %v", err)
			}
			var pathError *os.PathError
			if !errors.As(err, &pathError) || pathError.Path != path {
				t.Fatalf("filesystem path was lost: %v", err)
			}
		})
	}
}

// The /resume callback appends session_info through Session persistence. Drive a real append failure through the selector owner and crash reporter rather than substituting a synthetic error.
func TestSessionSelectorRenamePersistenceFailureReachesCrashLog(t *testing.T) {
	path := t.TempDir()
	session := NewSession("11111111-1111-4111-8111-111111111111", t.TempDir())
	session.path, session.flushed = path, true
	selector := newLoadedSessionSelector(
		func() ([]SessionInfo, error) { return []SessionInfo{{Path: path, ID: session.ID(), Name: "old"}}, nil },
		func() ([]SessionInfo, error) { return nil, nil },
		func(_ string, name string) error { _, err := session.AppendSessionInfo(name); return err },
		nil, "", sessionSelectorInputBindings(t))
	selector.enterRenameMode()
	selector.renameInput.SetText("new")
	ui := tui.NewWithOutput(io.Discard, 120, 30)
	defer ui.Stop()
	editor := tui.NewEditor()
	container := tui.NewContainer(editor)
	mode := &InteractiveMode{opts: InteractiveOptions{AgentDir: t.TempDir()}, tuiInst: ui, editor: editor, editorContainer: container, layout: tui.NewContainer(container)}
	ui.Add(mode.layout)
	input := make(chan []byte, 1)
	input <- []byte("\r")
	mode.setModalInputChannel(input)
	var raised any
	func() {
		defer func() { raised = recover() }()
		mode.runEditorSlotSessionSelector(selector)
	}()
	rejection, ok := raised.(uncaughtError)
	if !ok {
		t.Fatalf("raised %T (%v), want uncaughtError", raised, raised)
	}
	want := "EISDIR: illegal operation on a directory, open '" + path + "'"
	if rejection.Error() != want || !errors.Is(rejection, syscall.EISDIR) {
		t.Fatalf("rejection = %v, want %s with errno", rejection, want)
	}
	var stderr strings.Builder
	mode.uncaughtCrash(rejection, []byte("stack\n"), &stderr)
	if !strings.Contains(stderr.String(), "Error: "+want+"\n") {
		t.Fatalf("diagnostic = %s", stderr.String())
	}
	records := ReadCrashLog(CrashLogPath(mode.opts.AgentDir))
	if len(records) != 1 || records[0].Message != want || records[0].Kind != "uncaught_exception" {
		t.Fatalf("records = %+v", records)
	}
}
