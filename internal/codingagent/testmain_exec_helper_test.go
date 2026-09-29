package codingagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// execEchoHelperEnv, when set to "1", makes this test binary behave like a
// portable `echo`: print the remaining arguments space-joined with a
// trailing newline, then exit, without running any tests. A test that needs
// to exec a real, natively executable command (through pi.exec, which spawns
// the requested command directly with no shell) sets this in the child's
// environment and passes os.Executable() (this same test binary) as the
// command, so the same test works on every OS the test binary runs on,
// including native Windows, instead of depending on /bin/echo or a
// cmd.exe-only builtin. Mirrors cmd/pig/testmain_test.go's identical helper.
const execEchoHelperEnv = "PIG_TEST_EXEC_ECHO_HELPER"

// argvRecordEnv names a file to which this test binary, run under another program's name, appends that name and its arguments as one JSON line, then exits. A test copies the binary over a launcher such as rundll32.exe to observe exactly what production code would pass to it.
const argvRecordEnv = "PIG_TEST_ARGV_RECORD"

type recordedArgv struct {
	Name string
	Args []string
}

// fakeTrashEnv, when set, makes this test binary, copied onto PATH as `trash`, act as the trash command. "move" removes its last argument and exits 0; "fail" writes fakeTrashStderr and exits 1; "fail-removed" removes its last argument, then fails like "fail"; "fail-long" writes one 300-unit line and exits 1. fakeTrashArgvEnv names a file that receives the arguments as JSON.
const (
	fakeTrashEnv     = "PIG_TEST_FAKE_TRASH"
	fakeTrashArgvEnv = "PIG_TEST_FAKE_TRASH_ARGV"
	fakeTrashStderr  = "  trash: fake refusal\r\nsecond line\r\n"
)

func runFakeTrash(mode string) int {
	args := os.Args[1:]
	if record := os.Getenv(fakeTrashArgvEnv); record != "" {
		data, err := json.Marshal(args)
		if err == nil {
			err = os.WriteFile(record, data, 0o600)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}
	removeTarget := func() {
		if len(args) > 0 {
			_ = os.Remove(args[len(args)-1])
		}
	}
	switch mode {
	case "move":
		removeTarget()
		return 0
	case "fail-removed":
		removeTarget()
		fmt.Fprint(os.Stderr, fakeTrashStderr)
		return 1
	case "overflow-combined":
		_, _ = os.Stdout.Write([]byte(strings.Repeat("x", 768*1024)))
		_, _ = os.Stderr.Write([]byte(strings.Repeat("x", 768*1024)))
		return 1
	case "overflow-stdout", "overflow-stderr":
		out := os.Stdout
		if mode == "overflow-stderr" {
			out = os.Stderr
		}
		_, _ = out.Write([]byte(strings.Repeat("x", 2*1024*1024)))
		return 1
	case "fail-long":
		fmt.Fprint(os.Stderr, strings.Repeat("\U0001F600", 150)+"\n")
		return 1
	default:
		fmt.Fprint(os.Stderr, fakeTrashStderr)
		return 1
	}
}

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeTrashEnv); mode != "" {
		os.Exit(runFakeTrash(mode))
	}
	reportStartupIfRequested()
	if path := os.Getenv(argvRecordEnv); path != "" {
		data, err := json.Marshal(recordedArgv{Name: filepath.Base(os.Args[0]), Args: os.Args[1:]})
		if err == nil {
			var file *os.File
			file, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err == nil {
				_, err = file.Write(append(data, '\n'))
				err = errors.Join(err, file.Close())
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv(execEchoHelperEnv) == "1" {
		fmt.Println(strings.Join(os.Args[1:], " "))
		os.Exit(0)
	}
	if pidFile := os.Getenv(clipboardDescendantEnv); pidFile != "" {
		os.Exit(runClipboardDescendantHelper(pidFile))
	}
	if os.Getenv(clipboardDescendantHoldEnv) == "1" {
		holdClipboardDescendantPipes()
	}
	// Package tests never reach the host's native clipboard: on macOS and
	// Windows it is the real system pasteboard regardless of clipboardGOOS, so
	// a test faking another platform's commands would read whatever the user
	// last copied. Tests that exercise the native reader install a fake.
	getNativeClipboard = func() *tui.NativeClipboard { return nil }
	os.Exit(m.Run())
}
