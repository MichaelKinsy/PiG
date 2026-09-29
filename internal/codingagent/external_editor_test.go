package codingagent

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The fake editor is this test binary: an editor command names it, runs only
// TestEditorHelperProcess, and passes a mode after "--". The same fake runs on
// every platform, through cmd.exe on Windows as upstream's shell: true does.
const editorHelperEnv = "PIG_TEST_EDITOR_HELPER"

var editorHelperContent = map[string]string{
	"roundtrip":  "edited content from fake editor\n",
	"visual":     "from VISUAL\n",
	"editor":     "from EDITOR\n",
	"trailer":    "hello world\n",
	"configured": "from configured\n",
	"env":        "from env\n",
	"default":    "from the default editor",
}

func TestEditorHelperProcess(t *testing.T) {
	if os.Getenv(editorHelperEnv) != "1" {
		return
	}
	args := os.Args
	for i, arg := range args {
		if arg == "--" {
			args = args[i+1:]
			break
		}
	}
	mode, file := args[0], args[len(args)-1]
	switch mode {
	case "exit0":
	case "exit1":
		os.Exit(1)
	case "args":
		_ = os.WriteFile(file, []byte(strings.Join(args[1:], " ")), 0o600)
	default:
		_ = os.WriteFile(file, []byte(editorHelperContent[mode]), 0o600)
	}
	os.Exit(0)
}

// fakeEditor names the helper through PATH so Pi's literal-space command splitting also works when the test executable's directory or basename contains spaces.
func fakeEditor(t *testing.T, mode string) string {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir, name := filepath.Dir(binary), filepath.Base(binary)
	if strings.ContainsAny(name, " \t") {
		dir, name = t.TempDir(), "pig-editor-helper"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		target := filepath.Join(dir, name)
		if err := os.Link(binary, target); err != nil {
			copyEditorHelper(t, binary, target)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(editorHelperEnv, "1")
	return name + " -test.run=TestEditorHelperProcess -- " + mode
}

func copyEditorHelper(t *testing.T, source, target string) {
	t.Helper()
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = output.Close() }()
	if _, err := io.Copy(output, input); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenExternalEditorPrintsLaunchMessage(t *testing.T) {
	fake := fakeEditor(t, "exit0")

	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", fake+" --wait")

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	if _, err := OpenExternalEditor(context.Background(), "original", ""); err != nil {
		t.Fatalf("OpenExternalEditor: %v", err)
	}
	_ = w.Close()

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()

	got := buf.String()
	if !strings.Contains(got, "Launching external editor: "+fake+" --wait") {
		t.Fatalf("stdout missing launch message:\n%s", got)
	}
	if !strings.Contains(got, "Pi will resume when the editor exits.") {
		t.Fatalf("stdout missing resume message:\n%s", got)
	}
}

func TestOpenExternalEditorRoundTrip(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", fakeEditor(t, "roundtrip"))

	got, err := OpenExternalEditor(context.Background(), "original", "")
	if err != nil {
		t.Fatalf("OpenExternalEditor: %v", err)
	}
	if got != "edited content from fake editor" {
		t.Fatalf("got %q want edited content", got)
	}
}

func TestOpenExternalEditorVISUALWinsOverEDITOR(t *testing.T) {
	t.Setenv("VISUAL", fakeEditor(t, "visual"))
	t.Setenv("EDITOR", fakeEditor(t, "editor"))

	got, err := OpenExternalEditor(context.Background(), "", "")
	if err != nil {
		t.Fatalf("OpenExternalEditor: %v", err)
	}
	if got != "from VISUAL" {
		t.Errorf("VISUAL must win over EDITOR; got %q", got)
	}
}

// With no configured editor, $VISUAL or $EDITOR, upstream
// getExternalEditorCommand falls back to nano (notepad on Windows) instead of
// refusing to open an editor. A stub of that name first on PATH stands in
// for it, so the real editor never opens.
func TestOpenExternalEditorFallsBackToThePlatformDefault(t *testing.T) {
	helper := fakeEditor(t, "default")
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	dir := t.TempDir()
	stub, script := filepath.Join(dir, "nano"), "#!/bin/sh\nexec "+helper+" \"$@\"\n"
	if runtime.GOOS == "windows" {
		stub, script = filepath.Join(dir, "notepad.cmd"), "@"+helper+" %*\r\n"
	}
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := OpenExternalEditor(context.Background(), "keep me", "")
	if err != nil {
		t.Fatalf("OpenExternalEditor: %v", err)
	}
	if got != "from the default editor" {
		t.Errorf("result = %q, want the default editor's content", got)
	}
}

func TestOpenExternalEditorPreservesInitialOnError(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", fakeEditor(t, "exit1"))

	got, err := OpenExternalEditor(context.Background(), "INITIAL", "")
	if err == nil {
		t.Fatalf("expected error on non-zero exit")
	}
	if got != "INITIAL" {
		t.Errorf("non-zero exit must return initial text; got %q", got)
	}
}

func TestOpenExternalEditorStripsOneTrailingNewline(t *testing.T) {
	// The fake writes "hello world\n", ending with one \n as most editors do.
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", fakeEditor(t, "trailer"))

	got, err := OpenExternalEditor(context.Background(), "", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "hello world" {
		t.Errorf("trailing newline must be stripped; got %q", got)
	}
}

func TestOpenExternalEditorAcceptsArguments(t *testing.T) {
	// Common configuration: $EDITOR="code --wait". The helper splits on
	// whitespace and forwards the args; the fake records them into the file.
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", fakeEditor(t, "args")+" --extra-flag  --second")

	got, err := OpenExternalEditor(context.Background(), "", "")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	prefix := "--extra-flag  --second "
	if runtime.GOOS == "windows" {
		// Pi's shell:true lets cmd.exe parse the command line on Windows.
		prefix = "--extra-flag --second "
	}
	if !strings.HasPrefix(got, prefix) {
		t.Errorf("expected arguments %q forwarded; got %q", prefix, got)
	}
}

// Pi resolves the command through SettingsManager before editInExternalEditor spawns the child.
func TestOpenExternalEditorSettingsResolution(t *testing.T) {
	for _, configured := range []string{"", " \ufeff\t"} {
		t.Run(configured, func(t *testing.T) {
			visual := fakeEditor(t, "visual")
			t.Setenv("VISUAL", visual)
			t.Setenv("EDITOR", fakeEditor(t, "editor"))
			sm := NewSettingsManager(t.TempDir(), t.TempDir())
			if err := sm.UpdateGlobal(func(settings *Settings) { settings.ExternalEditor = configured }); err != nil {
				t.Fatal(err)
			}
			command := sm.GetExternalEditorCommand()
			if command != visual {
				t.Errorf("resolved command = %q, want %q", command, visual)
			}
			got, err := OpenExternalEditor(t.Context(), "original", command)
			if err != nil || got != "from VISUAL" {
				t.Fatalf("settings/editor result = %q, %v", got, err)
			}
		})
	}
}

func TestOpenExternalEditorConfiguredCommandWinsOverEnvironment(t *testing.T) {
	t.Setenv("VISUAL", fakeEditor(t, "env"))
	t.Setenv("EDITOR", fakeEditor(t, "env"))

	got, err := OpenExternalEditor(context.Background(), "", fakeEditor(t, "configured"))
	if err != nil {
		t.Fatalf("OpenExternalEditor: %v", err)
	}
	if got != "from configured" {
		t.Fatalf("configured editor must win over env; got %q", got)
	}
}

// Upstream spawns the editor with shell: true on Windows, so the command line
// goes through cmd.exe: a %VAR% reference expands as it would at a prompt.
func TestOpenExternalEditorRunsThroughTheWindowsShell(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("upstream uses a shell for the editor only on Windows")
	}
	fake := fakeEditor(t, "roundtrip")
	binary, rest, _ := strings.Cut(fake, " ")
	t.Setenv("PIG_TEST_EDITOR_BINARY", binary)
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "%PIG_TEST_EDITOR_BINARY% "+rest)

	got, err := OpenExternalEditor(context.Background(), "original", "")
	if err != nil {
		t.Fatalf("OpenExternalEditor: %v", err)
	}
	if got != "edited content from fake editor" {
		t.Fatalf("got %q, want the editor named through %%PIG_TEST_EDITOR_BINARY%%", got)
	}
}
