package codingagent

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Ports the copyToClipboard cases of coding-agent test/clipboard.test.ts.
// PiG has no native clipboard module, so the native-writer cases become the
// platform command (pbcopy) they fall back to upstream.

type fakeClipboardRun struct {
	t     *testing.T
	calls []string
	args  [][]string
	input []*string
	// timeouts records each call's timeout option.
	timeouts []time.Duration
	reply    func(name string, args []string) ([]byte, bool)
}

func (f *fakeClipboardRun) run(name string, args []string, options clipboardCommandOptions) ([]byte, bool) {
	f.calls = append(f.calls, name)
	f.args = append(f.args, args)
	f.input = append(f.input, options.input)
	f.timeouts = append(f.timeouts, options.timeout)
	if f.reply == nil {
		return nil, true
	}
	return f.reply(name, args)
}

type clipboardFixture struct {
	copier clipboardCopier
	run    *fakeClipboardRun
	out    *bytes.Buffer
}

func newClipboardFixture(t *testing.T, platform string, env map[string]string) *clipboardFixture {
	t.Helper()
	f := &clipboardFixture{run: &fakeClipboardRun{t: t}, out: &bytes.Buffer{}}
	getenv := fakeEnvLookup(env)
	tmp := t.TempDir()
	f.copier = clipboardCopier{
		platform: platform,
		getenv:   getenv,
		isWSL:    func() bool { return IsWSL(getenv, func(string) ([]byte, error) { return nil, os.ErrNotExist }) },
		run:      f.run.run,
		stdout:   f.out,
		tempDir:  func() string { return tmp },
	}
	return f
}

func (f *clipboardFixture) osc52Writes() int {
	return strings.Count(f.out.String(), "\x1b]52;c;")
}

func TestCopyToClipboardMacOSUsesPbcopy(t *testing.T) {
	f := newClipboardFixture(t, "darwin", nil)
	if err := f.copier.copy("hello"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.run.calls, []string{"pbcopy"}) || *f.run.input[0] != "hello" {
		t.Fatalf("calls = %v, want pbcopy with the text", f.run.calls)
	}
	if f.osc52Writes() != 0 {
		t.Fatal("local success emitted OSC 52")
	}
}

func TestCopyToClipboardLinuxUsesX11Tools(t *testing.T) {
	f := newClipboardFixture(t, "linux", map[string]string{"DISPLAY": ":0"})
	if err := f.copier.copy("hello"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.run.calls, []string{"xclip"}) || !slices.Equal(f.run.args[0], []string{"-selection", "clipboard"}) {
		t.Fatalf("calls = %v %v, want xclip -selection clipboard", f.run.calls, f.run.args)
	}
}

func TestCopyToClipboardRemoteEmitsOSC52AfterTheLocalWrite(t *testing.T) {
	f := newClipboardFixture(t, "darwin", map[string]string{"SSH_CONNECTION": "client server"})
	if err := f.copier.copy("hello"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.run.calls, []string{"pbcopy"}) || f.osc52Writes() != 1 {
		t.Fatalf("calls = %v, OSC 52 writes = %d; want pbcopy then one OSC 52", f.run.calls, f.osc52Writes())
	}
}

func TestCopyToClipboardTriesXclipAndXselAfterWlCopy(t *testing.T) {
	f := newClipboardFixture(t, "linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"})
	f.run.reply = func(name string, _ []string) ([]byte, bool) { return nil, name == "xsel" }
	if err := f.copier.copy("hello"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.run.calls, []string{"wl-copy", "xclip", "xsel"}) || f.osc52Writes() != 0 {
		t.Fatalf("calls = %v, OSC 52 = %d", f.run.calls, f.osc52Writes())
	}
}

func TestCopyToClipboardLocalLinuxFailureReportsX11(t *testing.T) {
	f := newClipboardFixture(t, "linux", map[string]string{"DISPLAY": ":0"})
	f.run.reply = func(string, []string) ([]byte, bool) { return nil, false }
	err := f.copier.copy("hello")
	if err == nil || err.Error() != "Clipboard unavailable: install `xclip` or `xsel`, or check X11 access" {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(f.run.calls, []string{"xclip", "xsel"}) || f.osc52Writes() != 0 {
		t.Fatalf("calls = %v, OSC 52 = %d", f.run.calls, f.osc52Writes())
	}
}

func TestCopyToClipboardDisplayLessLinuxFallsBackToOSC52(t *testing.T) {
	f := newClipboardFixture(t, "linux", nil)
	if err := f.copier.copy("hello"); err != nil {
		t.Fatal(err)
	}
	if len(f.run.calls) != 0 || f.osc52Writes() != 1 {
		t.Fatalf("calls = %v, OSC 52 = %d", f.run.calls, f.osc52Writes())
	}
}

func TestCopyToClipboardWSLWritesWindowsClipboardThroughPowerShell(t *testing.T) {
	f := newClipboardFixture(t, "linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"})
	var written string
	var tmpPath string
	f.run.reply = func(name string, args []string) ([]byte, bool) {
		if name != "wslpath" {
			return nil, true
		}
		tmpPath = args[1]
		data, err := os.ReadFile(tmpPath)
		if err != nil {
			t.Fatal(err)
		}
		written = string(data)
		return []byte("\\\\wsl.localhost\\Ubuntu\\tmp\\clip.txt\n"), true
	}
	if err := f.copier.copy("héllo"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.run.calls, []string{"wslpath", "powershell.exe"}) || written != "héllo" {
		t.Fatalf("calls = %v, written = %q", f.run.calls, written)
	}
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf("temp file %s survived: %v", tmpPath, err)
	}
	script := f.run.args[1][2]
	if !strings.Contains(script, "Set-Clipboard") || !strings.Contains(script, "'\\\\wsl.localhost\\Ubuntu\\tmp\\clip.txt'") {
		t.Fatalf("PowerShell script = %q", script)
	}
	if f.osc52Writes() != 0 {
		t.Fatal("PowerShell success also emitted OSC 52")
	}
}

func TestCopyToClipboardWSLFallsBackToOSC52WithoutInterop(t *testing.T) {
	f := newClipboardFixture(t, "linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"})
	f.run.reply = func(string, []string) ([]byte, bool) { return nil, false }
	if err := f.copier.copy("hello"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.run.calls, []string{"wslpath"}) || f.osc52Writes() != 1 {
		t.Fatalf("calls = %v, OSC 52 = %d", f.run.calls, f.osc52Writes())
	}
}

func TestCopyToClipboardWSLInWindowsTerminalPrefersOSC52(t *testing.T) {
	for _, env := range []map[string]string{
		{"WSL_DISTRO_NAME": "Ubuntu", "WT_SESSION": "session"},
		{"WSL_DISTRO_NAME": "Ubuntu", "WT_SESSION": "session", "SSH_CONNECTION": "client server"},
	} {
		f := newClipboardFixture(t, "linux", env)
		if err := f.copier.copy("hello"); err != nil {
			t.Fatal(err)
		}
		if len(f.run.calls) != 0 || f.osc52Writes() != 1 {
			t.Fatalf("%v: calls = %v, OSC 52 = %d; want exactly one OSC 52", env, f.run.calls, f.osc52Writes())
		}
	}
}

func TestCopyToClipboardWSLInWindowsTerminalUsesPowerShellForOversizedText(t *testing.T) {
	f := newClipboardFixture(t, "linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu", "WT_SESSION": "session"})
	f.run.reply = func(name string, _ []string) ([]byte, bool) {
		if name == "wslpath" {
			return []byte("C:\\clip.txt"), true
		}
		return nil, true
	}
	if err := f.copier.copy(strings.Repeat("x", 80_000)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.run.calls, []string{"wslpath", "powershell.exe"}) || f.osc52Writes() != 0 {
		t.Fatalf("calls = %v, OSC 52 = %d", f.run.calls, f.osc52Writes())
	}
}

func TestCopyToClipboardWSLWithADisplayPrefersLinuxTools(t *testing.T) {
	f := newClipboardFixture(t, "linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu", "WAYLAND_DISPLAY": "wayland-0"})
	if err := f.copier.copy("hello"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.run.calls, []string{"wl-copy"}) || f.osc52Writes() != 0 {
		t.Fatalf("calls = %v, OSC 52 = %d", f.run.calls, f.osc52Writes())
	}
}

func TestCopyToClipboardReportsWaylandInsteadOfX11(t *testing.T) {
	f := newClipboardFixture(t, "linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"})
	f.run.reply = func(string, []string) ([]byte, bool) { return nil, false }
	err := f.copier.copy("hello")
	if err == nil || err.Error() != "Clipboard unavailable: install `wl-clipboard` (`wl-copy`) or check Wayland access" {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(f.run.calls, []string{"wl-copy", "xclip", "xsel"}) {
		t.Fatalf("calls = %v", f.run.calls)
	}
}

func TestCopyToClipboardRemoteFailureUsesOSC52(t *testing.T) {
	f := newClipboardFixture(t, "darwin", map[string]string{"SSH_CONNECTION": "client server"})
	f.run.reply = func(string, []string) ([]byte, bool) { return nil, false }
	if err := f.copier.copy("hello"); err != nil {
		t.Fatal(err)
	}
	if f.osc52Writes() != 1 {
		t.Fatalf("OSC 52 = %d, want 1", f.osc52Writes())
	}
}

func TestCopyToClipboardDoesNotEmitOversizedOSC52(t *testing.T) {
	f := newClipboardFixture(t, "darwin", map[string]string{"SSH_CONNECTION": "client server"})
	f.run.reply = func(string, []string) ([]byte, bool) { return nil, false }
	err := f.copier.copy(strings.Repeat("x", 80_000))
	if err == nil || err.Error() != "Clipboard unavailable: text exceeds the OSC 52 size limit" {
		t.Fatalf("err = %v", err)
	}
	if f.osc52Writes() != 0 {
		t.Fatal("oversized text emitted OSC 52")
	}
}

// clipboardHelperEnv makes this test binary, run as TestClipboardHelperProcess,
// the child runClipboardCommand starts, so every platform runs the same child.
const clipboardHelperEnv = "PIG_TEST_CLIPBOARD_HELPER"

// TestClipboardHelperProcess is the child TestRunClipboardCommand runs. After
// "--" it takes a mode: print <text>, exit <code>, stdin <want> (succeeds only
// if stdin is exactly want), or sleep.
func TestClipboardHelperProcess(t *testing.T) {
	if os.Getenv(clipboardHelperEnv) != "1" {
		return
	}
	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	switch args[0] {
	case "print":
		fmt.Print(args[1])
	case "exit":
		code, _ := strconv.Atoi(args[1])
		os.Exit(code)
	case "stdin":
		data, err := io.ReadAll(os.Stdin)
		if err != nil || string(data) != args[1] {
			os.Exit(1)
		}
	case "sleep":
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

// runClipboardCommand reports success with output, failure for a failing
// command, and passes input on stdin (utils/clipboard-command.ts).
// Real children exercise exit status, bounded stdout, stdin ownership and process
// deadline cancellation; a fake command would bypass the exec boundary.
func TestRunClipboardCommand(t *testing.T) {
	t.Setenv(clipboardHelperEnv, "1")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := func(mode ...string) []string {
		return append([]string{"-test.run=^TestClipboardHelperProcess$", "--"}, mode...)
	}
	// A generous timeout: process start alone can take seconds on a loaded host.
	const slow = 30 * time.Second
	if out, ok := runClipboardCommand(self, helper("print", "hi"), clipboardCommandOptions{timeout: slow}); !ok || string(out) != "hi" {
		t.Fatalf("print: out %q ok %v", out, ok)
	}
	if _, ok := runClipboardCommand(self, helper("exit", "3"), clipboardCommandOptions{timeout: slow}); ok {
		t.Fatal("a failing command reported success")
	}
	if _, ok := runClipboardCommand(self, helper("print", "0123456789"), clipboardCommandOptions{timeout: slow, maxBytes: 4}); ok {
		t.Fatal("output over maxBytes reported success")
	}
	input := "piped"
	if out, ok := runClipboardCommand(self, helper("stdin", input), clipboardCommandOptions{timeout: slow, input: &input}); !ok || len(out) != 0 {
		t.Fatalf("stdin writer: out %q ok %v", out, ok)
	}
	if _, ok := runClipboardCommand(self, helper("sleep"), clipboardCommandOptions{timeout: 100 * time.Millisecond}); ok {
		t.Fatal("a command past its timeout reported success")
	}
}
