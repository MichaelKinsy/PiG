package subprocess

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBuildExtCommand_GoBinary(t *testing.T) {
	cmd := buildExtCommand(context.Background(), "/usr/local/bin/my-ext", "go")
	if cmd.Path != "/usr/local/bin/my-ext" {
		t.Errorf("go binary: Path = %q, want /usr/local/bin/my-ext", cmd.Path)
	}
	if len(cmd.Args) != 1 || cmd.Args[0] != "/usr/local/bin/my-ext" {
		t.Errorf("go binary: Args = %v, want [/usr/local/bin/my-ext]", cmd.Args)
	}
}

func TestBuildExtCommand_RustBinary(t *testing.T) {
	cmd := buildExtCommand(context.Background(), "/usr/local/bin/rust-ext", "rust")
	if cmd.Path != "/usr/local/bin/rust-ext" {
		t.Errorf("rust binary: Path = %q, want /usr/local/bin/rust-ext", cmd.Path)
	}
}

func TestBuildExtCommand_UnknownLanguage(t *testing.T) {
	cmd := buildExtCommand(context.Background(), "/path/to/ext", "")
	if cmd.Path != "/path/to/ext" {
		t.Errorf("unknown language: Path = %q, want /path/to/ext", cmd.Path)
	}
}

func TestBuildExtCommand_PythonWithUV(t *testing.T) {
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv not installed")
	}
	cmd := buildExtCommand(context.Background(), "/tmp/ext/main.py", "python")
	// Should use uv
	if !strings.HasSuffix(cmd.Path, "uv") && !strings.Contains(cmd.Path, "uv") {
		t.Errorf("python with uv: Path = %q, want path ending in 'uv'", cmd.Path)
	}
	if len(cmd.Args) < 3 || cmd.Args[1] != "run" || cmd.Args[2] != "/tmp/ext/main.py" {
		t.Errorf("python with uv: Args = %v, want [uv run /tmp/ext/main.py]", cmd.Args)
	}
}

func TestBuildExtCommand_PythonFallback(t *testing.T) {
	lookPath := func(name string) (string, error) {
		if name == "python3" {
			return "/usr/bin/python3", nil
		}
		return "", exec.ErrNotFound
	}
	cmd := buildExtCommandForGOOS(context.Background(), "/tmp/ext/main.py", "python", "linux", lookPath)
	if cmd.Path != "/usr/bin/python3" {
		t.Errorf("python fallback: want python3, got Path=%q Args=%v", cmd.Path, cmd.Args)
	}
	if len(cmd.Args) < 2 || cmd.Args[1] != "/tmp/ext/main.py" {
		t.Errorf("python fallback: Args = %v, want [python3 /tmp/ext/main.py]", cmd.Args)
	}
}

// Windows cannot execute a script, so a Node script standalone runs through
// node there, as a Python standalone runs through its interpreter. Elsewhere
// the script is executed and its shebang selects the interpreter.
func TestBuildExtCommand_NodeScriptStandalone(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "run.mjs")
	node := filepath.Join(dir, "node.exe")
	lookPath := func(name string) (string, error) {
		if name == "node" {
			return node, nil
		}
		return "", exec.ErrNotFound
	}
	cmd := buildExtCommandForGOOS(context.Background(), script, "node", "windows", lookPath)
	if cmd.Path != node || !slices.Equal(cmd.Args[1:], []string{script}) {
		t.Fatalf("windows node standalone: Path=%q Args=%q, want %q %q", cmd.Path, cmd.Args, node, script)
	}
	cmd = buildExtCommandForGOOS(context.Background(), script, "node", "linux", lookPath)
	if cmd.Path != script || len(cmd.Args) != 1 {
		t.Fatalf("linux node standalone: Path=%q Args=%q, want the script itself", cmd.Path, cmd.Args)
	}
}

func TestBuildExtCommand_PySuffixDetection(t *testing.T) {
	// Even without RuntimeLanguage set, .py suffix should trigger Python path.
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv not installed")
	}
	cmd := buildExtCommand(context.Background(), "/tmp/ext/main.py", "")
	// Should use uv (or python3), NOT direct execution
	if cmd.Args[0] == "/tmp/ext/main.py" {
		t.Error(".py suffix detection failed: launched script directly instead of via interpreter")
	}
}

func TestBuildExtCommand_PySuffixFallback(t *testing.T) {
	lookPath := func(name string) (string, error) {
		if name == "python3" {
			return "/usr/bin/python3", nil
		}
		return "", exec.ErrNotFound
	}
	cmd := buildExtCommandForGOOS(context.Background(), "/tmp/ext/runner.py", "", "linux", lookPath)
	if cmd.Args[0] == "/tmp/ext/runner.py" {
		t.Error(".py suffix fallback failed: launched script directly instead of via python3")
	}
	if cmd.Path != "/usr/bin/python3" {
		t.Errorf(".py suffix fallback: want python3, got Path=%q Args=%v", cmd.Path, cmd.Args)
	}
}
