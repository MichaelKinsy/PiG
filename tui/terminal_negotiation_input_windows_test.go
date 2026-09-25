//go:build windows

package tui

import (
	"errors"
	"fmt"
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
		text, err := os.ReadFile(fmt.Sprintf("%s.%d", keys, next))
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
