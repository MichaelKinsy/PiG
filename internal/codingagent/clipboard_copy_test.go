package codingagent

// pi: packages/coding-agent/src/utils/clipboard.ts

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/tui"
)

// These tests cover command, WSL interop, and OSC 52 paths from packages/coding-agent/test/clipboard.test.ts. Command-failure fixtures do not assert NativeClipboard.setText behavior.

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
	// native is the helper returned by getNativeClipboard; nil models a missing native module.
	native       *tui.NativeClipboard
	nativeLookup int
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
		native: func() *tui.NativeClipboard {
			f.nativeLookup++
			return f.native
		},
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

// Ports packages/coding-agent/test/clipboard.test.ts "Termux on Android writes
// through termux-clipboard-set" (#10391).
func TestCopyToClipboardTermuxOnAndroidWritesThroughTermuxClipboardSet(t *testing.T) {
	f := newClipboardFixture(t, "android", map[string]string{"TERMUX_VERSION": "0.119"})
	if err := f.copier.copy("hello"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.run.calls, []string{"termux-clipboard-set"}) || len(f.run.args[0]) != 0 ||
		f.run.input[0] == nil || *f.run.input[0] != "hello" || f.run.timeouts[0] != 5*time.Second {
		t.Fatalf("calls=%v args=%q input=%v timeouts=%v", f.run.calls, f.run.args, f.run.input, f.run.timeouts)
	}
	if f.osc52Writes() != 0 {
		t.Fatal("local success emitted OSC 52")
	}
}

// Ports packages/coding-agent/test/clipboard.test.ts "reports the Termux:API
// requirement on Android" (#10391): the hint is not Linux-only.
func TestCopyToClipboardReportsTheTermuxAPIRequirementOnAndroid(t *testing.T) {
	for _, platform := range []string{"android", "linux"} {
		t.Run(platform, func(t *testing.T) {
			f := newClipboardFixture(t, platform, map[string]string{"TERMUX_VERSION": "0.119"})
			f.run.reply = func(string, []string) ([]byte, bool) { return nil, false }
			err := f.copier.copy("hello")
			if err == nil || err.Error() != "Clipboard unavailable: install the Termux:API app and `termux-api` package" {
				t.Fatalf("err = %v", err)
			}
			if f.osc52Writes() != 0 {
				t.Fatal("Termux failure emitted OSC 52")
			}
		})
	}
}

// Ports packages/coding-agent/test/clipboard.test.ts:179.
func TestCopyToClipboardDisplayLessLinuxFallsBackToOSC52(t *testing.T) {
	f := newClipboardFixture(t, "linux", nil)
	if err := f.copier.copy("hello"); err != nil {
		t.Fatal(err)
	}
	if len(f.run.calls) != 0 || f.out.String() != "\x1b]52;c;aGVsbG8=\x07" {
		t.Fatalf("calls = %v, OSC 52 = %q", f.run.calls, f.out)
	}
}

// Ports packages/coding-agent/test/clipboard.test.ts:186.
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
	wantScript := "Set-Clipboard -Value ([System.IO.File]::ReadAllText('\\\\wsl.localhost\\Ubuntu\\tmp\\clip.txt', [System.Text.Encoding]::UTF8))"
	if !slices.Equal(f.run.args[0], []string{"-w", tmpPath}) || !slices.Equal(f.run.args[1], []string{"-NoProfile", "-Command", wantScript}) {
		t.Fatalf("command args = %q", f.run.args)
	}
	if !slices.Equal(f.run.timeouts, []time.Duration{time.Second, 5 * time.Second}) || f.run.input[0] != nil || f.run.input[1] != nil {
		t.Fatalf("command options = timeouts %v, input %v", f.run.timeouts, f.run.input)
	}
	if f.out.Len() != 0 {
		t.Fatalf("PowerShell success also emitted output: %q", f.out)
	}
}

// utils/clipboard.ts:38 a single quote in the Windows path is doubled so it cannot end the PowerShell string literal.
func TestCopyToClipboardWSLEscapesSingleQuotesInThePowerShellPath(t *testing.T) {
	f := newClipboardFixture(t, "linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"})
	f.run.reply = func(name string, _ []string) ([]byte, bool) {
		if name == "wslpath" {
			return []byte("C:\\Users\\o'brien\\clip.txt\r\n"), true
		}
		return nil, true
	}
	if err := f.copier.copy("text"); err != nil {
		t.Fatal(err)
	}
	want := "Set-Clipboard -Value ([System.IO.File]::ReadAllText('C:\\Users\\o''brien\\clip.txt', [System.Text.Encoding]::UTF8))"
	if len(f.run.args) != 2 || f.run.args[1][2] != want {
		t.Fatalf("script = %q, want %q", f.run.args, want)
	}
}

// Ports packages/coding-agent/test/clipboard.test.ts:206.
func TestCopyToClipboardWSLFallsBackToOSC52WithoutInterop(t *testing.T) {
	f := newClipboardFixture(t, "linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"})
	f.run.reply = func(string, []string) ([]byte, bool) { return nil, false }
	if err := f.copier.copy("hello"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.run.calls, []string{"wslpath"}) || f.out.String() != "\x1b]52;c;aGVsbG8=\x07" {
		t.Fatalf("calls = %v, OSC 52 = %q", f.run.calls, f.out)
	}
	if _, err := os.Stat(f.run.args[0][1]); !os.IsNotExist(err) {
		t.Fatalf("temporary file survived failed interop: %v", err)
	}
}

// Ports packages/coding-agent/test/clipboard.test.ts:214,222.
func TestCopyToClipboardWSLInWindowsTerminalPrefersOSC52(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
	}{
		{"local", map[string]string{"WSL_DISTRO_NAME": "Ubuntu", "WT_SESSION": "session"}},
		{"remote", map[string]string{"WSL_DISTRO_NAME": "Ubuntu", "WT_SESSION": "session", "SSH_CONNECTION": "client server"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newClipboardFixture(t, "linux", tc.env)
			if err := f.copier.copy("hello"); err != nil {
				t.Fatal(err)
			}
			if len(f.run.calls) != 0 || f.out.String() != "\x1b]52;c;aGVsbG8=\x07" {
				t.Fatalf("calls = %v, OSC 52 = %q; want exactly one complete OSC 52", f.run.calls, f.out)
			}
		})
	}
}

// Ports packages/coding-agent/test/clipboard.test.ts:231.
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

// Ports packages/coding-agent/test/clipboard.test.ts:242.
func TestCopyToClipboardWSLWithADisplayPrefersLinuxTools(t *testing.T) {
	f := newClipboardFixture(t, "linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu", "WAYLAND_DISPLAY": "wayland-0"})
	if err := f.copier.copy("hello"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.run.calls, []string{"wl-copy"}) || f.out.Len() != 0 {
		t.Fatalf("calls = %v, output = %q", f.run.calls, f.out)
	}
	if len(f.run.args[0]) != 0 || f.run.input[0] == nil || *f.run.input[0] != "hello" || f.run.timeouts[0] != 5*time.Second {
		t.Fatalf("wl-copy options = args %q, input %v, timeouts %v", f.run.args, f.run.input, f.run.timeouts)
	}
}

// Ports packages/coding-agent/test/clipboard.test.ts:250.
func TestCopyToClipboardReportsWaylandInsteadOfX11(t *testing.T) {
	f := newClipboardFixture(t, "linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"})
	f.run.reply = func(string, []string) ([]byte, bool) { return nil, false }
	err := f.copier.copy("hello")
	if err == nil || err.Error() != "Clipboard unavailable: install `wl-clipboard` (`wl-copy`) or check Wayland access" {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(f.run.calls, []string{"wl-copy", "xclip", "xsel"}) || f.out.Len() != 0 {
		t.Fatalf("calls = %v, output = %q", f.run.calls, f.out)
	}
}

// The command-failure/OSC path in packages/coding-agent/test/clipboard.test.ts:260; this fixture does not exercise the preceding rejected native write.
func TestCopyToClipboardRemoteFailureUsesOSC52(t *testing.T) {
	f := newClipboardFixture(t, "darwin", map[string]string{"SSH_CONNECTION": "client server"})
	f.run.reply = func(string, []string) ([]byte, bool) { return nil, false }
	if err := f.copier.copy("hello"); err != nil {
		t.Fatal(err)
	}
	if f.out.String() != "\x1b]52;c;aGVsbG8=\x07" {
		t.Fatalf("OSC 52 = %q", f.out)
	}
}

// The oversized-OSC path in packages/coding-agent/test/clipboard.test.ts:267; this fixture does not exercise the preceding rejected native write.
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

func clipboardJavaScriptTextCases() []struct{ name, text, utf8, base64 string } {
	// Pi utils/clipboard.ts:17,33 uses Buffer.from(text) and writeFileSync(..., "utf8"). Node replaces lone UTF-16 units and joins adjacent surrogate halves at both byte boundaries.
	return []struct{ name, text, utf8, base64 string }{
		{"empty", "", "", ""},
		{"unicode", "héllo", "héllo", "aMOpbGxv"},
		{"high", "\xed\xa0\x80", "�", "77+9"},
		{"low", "\xed\xbf\xbf", "�", "77+9"},
		{"paired", "\xed\xa0\xbd\xed\xb8\x80", "😀", "8J+YgA=="},
		{"mixed", "before\xed\xa0\x80after", "before�after", "YmVmb3Jl77+9YWZ0ZXI="},
		{"replacement", "�", "�", "77+9"},
	}
}

func TestCopyToClipboardOSC52EncodesJavaScriptUTF8(t *testing.T) {
	for _, tc := range clipboardJavaScriptTextCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := newClipboardFixture(t, "linux", nil)
			if err := f.copier.copy(tc.text); err != nil {
				t.Fatal(err)
			}
			want := "\x1b]52;c;" + tc.base64 + "\x07"
			if f.out.String() != want || len(f.run.calls) != 0 {
				t.Fatalf("OSC 52 = %q, calls = %v; want %q without commands", f.out, f.run.calls, want)
			}
		})
	}
}

func TestCopyViaWindowsClipboardEncodesJavaScriptUTF8(t *testing.T) {
	for _, tc := range clipboardJavaScriptTextCases() {
		t.Run(tc.name, func(t *testing.T) {
			f := newClipboardFixture(t, "linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"})
			var tempPath string
			f.run.reply = func(name string, args []string) ([]byte, bool) {
				if name == "wslpath" {
					tempPath = args[1]
					data, err := os.ReadFile(tempPath)
					if err != nil {
						t.Fatal(err)
					}
					if string(data) != tc.utf8 {
						t.Fatalf("temporary file bytes = %x, want %x", data, tc.utf8)
					}
					return []byte(`C:\clip.txt`), true
				}
				return nil, true
			}
			if err := f.copier.copy(tc.text); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(f.run.calls, []string{"wslpath", "powershell.exe"}) || f.out.Len() != 0 {
				t.Fatalf("calls = %v, output = %q", f.run.calls, f.out)
			}
			if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
				t.Fatalf("temporary file survived: %v", err)
			}
		})
	}
}

func TestCopyToClipboardOSC52PayloadBoundary(t *testing.T) {
	// Pi clipboard.ts:10,18 checks the encoded UTF-8 length, not JavaScript units or internal WTF-8 bytes. 100,000 base64 bytes carry at most 75,000 UTF-8 bytes.
	for _, tc := range []struct {
		name, text, wantEncoded string
		oversized               bool
	}{
		{"below", strings.Repeat("x", 74_999), strings.Repeat("eHh4", 24_999) + "eHg=", false},
		{"at", strings.Repeat("x", 75_000), strings.Repeat("eHh4", 25_000), false},
		{"over", strings.Repeat("x", 75_001), "", true},
		{"paired_surrogates_at_limit", strings.Repeat("\xed\xa0\xbd\xed\xb8\x80", 18_750), strings.Repeat("8J+YgPCfmIDwn5iA", 6_250), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newClipboardFixture(t, "linux", nil)
			err := f.copier.copy(tc.text)
			if tc.oversized {
				if err == nil || err.Error() != "Clipboard unavailable: text exceeds the OSC 52 size limit" || f.out.Len() != 0 {
					t.Fatalf("oversized copy: err = %v, output bytes = %d", err, f.out.Len())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if want := "\x1b]52;c;" + tc.wantEncoded + "\x07"; f.out.String() != want {
				t.Fatalf("OSC 52 differs: got %d bytes, want %d", f.out.Len(), len(want))
			}
		})
	}
}

func TestRunClipboardCommandEncodesJavaScriptUTF8(t *testing.T) {
	// Pi clipboard-command.ts writes the input string to child.stdin, which applies Node's UTF-8 encoding rather than sending internal WTF-8 bytes.
	t.Setenv(clipboardHelperEnv, "1")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The deadline is not under test here. Starting the helper binary takes seconds on a loaded host (a race build also waits GORACE atexit_sleep_ms at exit), so use the generous deadline TestRunClipboardCommand uses; the default 3 s of clipboard-command.ts applies to Pi's callers, not to this child.
	const slow = 30 * time.Second
	for _, tc := range clipboardJavaScriptTextCases() {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"-test.run=^TestClipboardHelperProcess$", "--", "stdin", tc.utf8}
			if output, ok := runClipboardCommand(self, args, clipboardCommandOptions{input: &tc.text, timeout: slow}); !ok || len(output) != 0 {
				t.Fatalf("command did not receive UTF-8 %x: output = %q, ok = %t", tc.utf8, output, ok)
			}
		})
	}
}

func BenchmarkClipboardCopyUTF8(b *testing.B) {
	for _, tc := range []struct{ name, text string }{
		{"ordinary", "héllo"},
		{"ascii_limit", strings.Repeat("x", 75_000)},
		{"paired_surrogate_limit", strings.Repeat("\xed\xa0\xbd\xed\xb8\x80", 18_750)},
	} {
		b.Run(tc.name, func(b *testing.B) {
			copier := clipboardCopier{
				platform: "linux",
				getenv:   func(string) string { return "" },
				isWSL:    func() bool { return false },
				stdout:   io.Discard,
			}
			b.ReportAllocs()
			for b.Loop() {
				if err := copier.copy(tc.text); err != nil {
					b.Fatal(err)
				}
			}
		})
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
	case "sleepms":
		ms, _ := strconv.Atoi(args[1])
		time.Sleep(time.Duration(ms) * time.Millisecond)
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

// utils/clipboard-command.ts:25 options?.timeoutMs ?? 3000: with no timeout a clipboard command is killed after three seconds. The helper
// would finish after 3.5s, so success proves the default is longer than upstream's.
func TestRunClipboardCommandDefaultTimeoutIsThreeSeconds(t *testing.T) {
	t.Setenv(clipboardHelperEnv, "1")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, ok := runClipboardCommand(self, []string{"-test.run=^TestClipboardHelperProcess$", "--", "sleepms", "3500"}, clipboardCommandOptions{})
	if elapsed := time.Since(start); ok || elapsed < 2900*time.Millisecond {
		t.Fatalf("ok=%v after %v; want the default timeout to kill the command at 3s", ok, elapsed)
	}
}

// nativeSetTextRecorder returns a native helper whose SetText records its text and returns result.
func nativeSetTextRecorder(texts *[]string, result func() error) *tui.NativeClipboard {
	return &tui.NativeClipboard{SetText: func(_ context.Context, text string) error {
		*texts = append(*texts, text)
		return result()
	}}
}

// Ports packages/coding-agent/test/clipboard.test.ts:114 (local native success skips OSC 52 and commands).
func TestCopyToClipboardLocalNativeSuccessSkipsOSC52AndCommands(t *testing.T) {
	f := newClipboardFixture(t, "darwin", nil)
	var texts []string
	f.native = nativeSetTextRecorder(&texts, func() error { return nil })
	if err := f.copier.copy("hello"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(texts, []string{"hello"}) || f.osc52Writes() != 0 || len(f.run.calls) != 0 {
		t.Fatalf("native writes=%q OSC 52=%d commands=%v", texts, f.osc52Writes(), f.run.calls)
	}
}

// Ports packages/coding-agent/test/clipboard.test.ts:120 (Linux skips the native writer) with the exact xclip options.
func TestCopyToClipboardLinuxSkipsTheNativeWriter(t *testing.T) {
	f := newClipboardFixture(t, "linux", map[string]string{"DISPLAY": ":0"})
	var texts []string
	f.native = nativeSetTextRecorder(&texts, func() error { return nil })
	if err := f.copier.copy("hello"); err != nil {
		t.Fatal(err)
	}
	if f.nativeLookup != 0 || len(texts) != 0 {
		t.Fatalf("native lookups=%d writes=%q", f.nativeLookup, texts)
	}
	if !slices.Equal(f.run.calls, []string{"xclip"}) || !slices.Equal(f.run.args[0], []string{"-selection", "clipboard"}) ||
		f.run.input[0] == nil || *f.run.input[0] != "hello" || f.run.timeouts[0] != 5*time.Second {
		t.Fatalf("calls=%v args=%q input=%v timeouts=%v", f.run.calls, f.run.args, f.run.input, f.run.timeouts)
	}
}

// Ports packages/coding-agent/test/clipboard.test.ts:130 (waits for the native write before emitting remote OSC 52). Upstream awaits setText, so the copy call must not return or emit while the native write is pending.
func TestCopyToClipboardWaitsForTheNativeWriteBeforeRemoteOSC52(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := newClipboardFixture(t, "darwin", map[string]string{"SSH_CONNECTION": "client server"})
		release := make(chan struct{})
		f.native = &tui.NativeClipboard{SetText: func(context.Context, string) error { <-release; return nil }}
		done := make(chan error, 1)
		go func() { done <- f.copier.copy("hello") }()
		synctest.Wait()
		if f.osc52Writes() != 0 {
			t.Fatal("OSC 52 emitted before the native write completed")
		}
		select {
		case err := <-done:
			t.Fatalf("copy returned before the native write completed: %v", err)
		default:
		}
		close(release)
		synctest.Wait()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if f.osc52Writes() != 1 || len(f.run.calls) != 0 {
			t.Fatalf("OSC 52=%d commands=%v", f.osc52Writes(), f.run.calls)
		}
	})
}

// Ports packages/coding-agent/test/clipboard.test.ts:145 (a rejected native write falls back to pbcopy).
func TestCopyToClipboardRejectedNativeWriteFallsBackToPbcopy(t *testing.T) {
	f := newClipboardFixture(t, "darwin", nil)
	var texts []string
	f.native = nativeSetTextRecorder(&texts, func() error { return errors.New("native failed") })
	if err := f.copier.copy("hello"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(texts, []string{"hello"}) || !slices.Equal(f.run.calls, []string{"pbcopy"}) ||
		len(f.run.args[0]) != 0 || f.run.input[0] == nil || *f.run.input[0] != "hello" || f.run.timeouts[0] != 5*time.Second {
		t.Fatalf("native=%q calls=%v args=%q input=%v timeouts=%v", texts, f.run.calls, f.run.args, f.run.input, f.run.timeouts)
	}
	if f.osc52Writes() != 0 {
		t.Fatal("OSC 52 emitted after a verified pbcopy write")
	}
}

// Ports packages/coding-agent/test/clipboard.test.ts:151 (a read-only native clipboard uses the command writer).
func TestCopyToClipboardReadOnlyNativeClipboardUsesTheCommandWriter(t *testing.T) {
	f := newClipboardFixture(t, "darwin", nil)
	f.native = &tui.NativeClipboard{GetText: func(context.Context) (*string, bool, error) { return nil, true, nil }}
	if err := f.copier.copy("hello"); err != nil {
		t.Fatal(err)
	}
	if len(f.run.calls) != 1 {
		t.Fatalf("commands=%v, want exactly one", f.run.calls)
	}
}

// Ports packages/coding-agent/test/clipboard.test.ts:260,267 with the preceding rejected native write.
func TestCopyToClipboardRemoteNativeAndCommandFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		text    string
		wantErr string
		osc52   int
	}{
		{"uses OSC 52 when native and command writes fail in a remote session", "hello", "", 1},
		{"does not emit oversized OSC 52 payloads", strings.Repeat("x", 80_000), "Clipboard unavailable: text exceeds the OSC 52 size limit", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newClipboardFixture(t, "darwin", map[string]string{"SSH_CONNECTION": "client server"})
			var texts []string
			f.native = nativeSetTextRecorder(&texts, func() error { return errors.New("native failed") })
			f.run.reply = func(string, []string) ([]byte, bool) { return nil, false }
			err := f.copier.copy(tc.text)
			if (tc.wantErr == "") != (err == nil) || (err != nil && err.Error() != tc.wantErr) {
				t.Fatalf("err=%v, want %q", err, tc.wantErr)
			}
			if len(texts) != 1 || f.osc52Writes() != tc.osc52 {
				t.Fatalf("native writes=%d OSC 52=%d, want %d", len(texts), f.osc52Writes(), tc.osc52)
			}
		})
	}
}
