//go:build windows

package tui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const (
	terminalInputCaseEnv = "PIG_TUI_TERMINAL_INPUT_CASE"
	terminalInputKeysEnv = "PIG_TUI_TERMINAL_INPUT_KEYS"
)

// withTerminalInput runs body in a child process attached to a pseudo console,
// which is how interactive input reaches pig on Windows. An anonymous pipe is
// not a Windows terminal: waiting on its handle signals no readiness, so a
// pipe cannot show whether the console reader stops without another key. The
// child reads the console in raw mode, and each send asks this process to type
// the keys into the pseudo console. index selects the calling subtest in the
// child, which runs test again.
func withTerminalInput(t *testing.T, test string, index int, body func(*testing.T, interactiveTestInput)) {
	t.Helper()
	if keys := os.Getenv(terminalInputKeysEnv); keys != "" {
		if os.Getenv(terminalInputCaseEnv) != strconv.Itoa(index) {
			return
		}
		restore, err := EnterRawMode()
		if err != nil {
			t.Fatal(err)
		}
		defer restore()
		sent := 0
		body(t, interactiveTestInput{
			file: os.Stdin,
			send: func(t *testing.T, text string) {
				t.Helper()
				sent++
				path := fmt.Sprintf("%s.%d", keys, sent)
				if err := os.WriteFile(path+".tmp", []byte(text), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(path+".tmp", path); err != nil {
					t.Fatal(err)
				}
			},
			release: func() {},
		})
		return
	}
	keys := filepath.Join(t.TempDir(), "keys")
	t.Setenv(terminalInputKeysEnv, keys)
	t.Setenv(terminalInputCaseEnv, strconv.Itoa(index))
	console := startInPseudoConsole(t, os.Args[0], "-test.run=^"+test+"$", "-test.count=1")
	console.typeRequestedKeys(t, keys)
	console.wait(t)
}

// typeRequestedKeys types each numbered keys file the child writes, in order,
// until the child exits or 60s pass.
func (p *pseudoConsole) typeRequestedKeys(t *testing.T, keys string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for next := 1; time.Now().Before(deadline); {
		text, err := readSharingDelete(fmt.Sprintf("%s.%d", keys, next))
		if err == nil {
			p.write(t, string(text))
			next++
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if event, _ := windows.WaitForSingleObject(p.process, 0); event == windows.WAIT_OBJECT_0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// readSharingDelete reads a keys file the child publishes with os.Rename.
// MoveFileEx renames through a handle opened with DELETE access, and that
// handle can still be open once the new name is visible. os.ReadFile opens
// without FILE_SHARE_DELETE, so a read racing the rename failed with
// ERROR_SHARING_VIOLATION ("being used by another process"). Sharing delete
// access lets the read coexist with the rename's handle.
func readSharingDelete(path string) ([]byte, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
		}
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	file := os.NewFile(uintptr(handle), path)
	defer func() { _ = file.Close() }()
	return io.ReadAll(file)
}
