package codingagent

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// clipboard-command.test.ts "runs clipboard commands from /" (Pi 6454d07e, #11592): WSL cannot start Windows executables from a deep working directory, so clipboard helpers run from the root.
func TestClipboardCommandRunsFromTheRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX pwd")
	}
	t.Chdir(t.TempDir())
	out, err := runClipboardCommandContext(context.Background(), "pwd", nil, clipboardCommandOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(out)); got != "/" {
		t.Errorf("clipboard helper ran in %q, want /", got)
	}
}

// open-browser.ts (Pi 6454d07e, #11592): xdg-open on WSL starts explorer.exe, which fails from a deep working directory.
func TestBrowserLauncherRunsFromTheRoot(t *testing.T) {
	if dir := browserCommand("https://example.com").Dir; dir != "/" {
		t.Errorf("browser launcher Dir = %q, want /", dir)
	}
}

// external-editor.test.ts (Pi 6454d07e, #11592): a failed editor is reported with a message instead of silently.
func TestOpenExternalEditorReportsFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX exit codes and signals")
	}
	t.Setenv("VISUAL", "")
	cases := []struct{ name, editor, want string }{
		{"exit code", fakeEditor(t, "exit3"), "External editor exited with code 3; prompt unchanged"},
		{"signal", fakeEditor(t, "signal"), "External editor exited with SIGKILL; prompt unchanged"},
		{"missing binary", "pig-no-such-editor-binary", "Failed to launch external editor: pig-no-such-editor-binary"},
	}
	for _, tc := range cases {
		t.Setenv("EDITOR", tc.editor)
		_, err := OpenExternalEditor(context.Background(), "original", "")
		var failure *ExternalEditorFailure
		if !errors.As(err, &failure) {
			t.Fatalf("%s: err = %v, want an ExternalEditorFailure", tc.name, err)
		}
		if failure.Message != tc.want {
			t.Errorf("%s: message = %q, want %q", tc.name, failure.Message, tc.want)
		}
	}
}

// extension-editor.ts (Pi 6454d07e, #11592): an external editor failure renders under the editor and the next success clears it.
func TestExtensionEditorShowsExternalEditorFailures(t *testing.T) {
	c := newExtensionEditorProbe(t, "Edit", "text", map[string][]string{"app.editor.external": {"ctrl+x"}}, nil)
	var apply func(string)
	var report func(string)
	c.SetExternalEditor(func(_, _ string, a func(string), r func(string)) { apply, report = a, r })
	c.HandleInput("\x18")
	report("Failed to launch external editor: nope")
	if got := widthx.StripAnsi(strings.Join(c.Render(80), "\n")); !strings.Contains(got, "Failed to launch external editor: nope") {
		t.Fatalf("render lacks the failure:\n%s", got)
	}
	apply("fixed")
	if got := widthx.StripAnsi(strings.Join(c.Render(80), "\n")); strings.Contains(got, "Failed to launch") {
		t.Fatalf("a successful edit left the failure:\n%s", got)
	}
}
