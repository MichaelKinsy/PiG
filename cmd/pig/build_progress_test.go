package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/buildprogress"
)

func TestBuildCommandPlainOutputPathFailure(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+pigModulePath+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// This component exceeds native filename limits and contains invalid Windows characters, so every platform fails before invoking a compiler.
	blocked := filepath.Join(root, "\x1b[31mblocked"+strings.Repeat("x", 512)+"\x1b[0m")
	t.Setenv("PIG_BIN", filepath.Join(blocked, "pig"))
	var stdout, stderr bytes.Buffer
	if code := runBuildCommand([]string{"build"}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit=%d: %s", code, &stderr)
	}
	if strings.ContainsAny(stderr.String(), "\x1b\r") || !strings.Contains(stderr.String(), "blocked") || !strings.Contains(stderr.String(), "Resolving source failed") {
		t.Fatalf("plain failure: %q", stderr.String())
	}
}

func TestBuildCommandReportsRelativeTargetFromNestedCheckout(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd", "pig"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+pigModulePath+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cmd", "pig", "main.go"), []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(filepath.Join(root, "cmd"))
	t.Setenv("PIG_BIN", filepath.Join("out", "pig"))
	t.Setenv("GOFLAGS", "")
	var stdout, stderr bytes.Buffer
	if code := runBuildCommand([]string{"build"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit=%d: %s", code, &stderr)
	}
	if !strings.Contains(stderr.String(), "Built "+filepath.Join(root, "out", "pig")) {
		t.Fatalf("wrong output path: %s", &stderr)
	}
}

func TestBuildSigningFailureIsVisibleAndBestEffort(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "codesign")
	if runtime.GOOS == "windows" {
		tool += ".exe"
	}
	source := filepath.Join(root, "main.go")
	if err := os.WriteFile(source, []byte("package main\nimport (\"fmt\"; \"os\")\nfunc main() { fmt.Fprintln(os.Stderr, \"signer denied\"); os.Exit(9) }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("go", "build", "-o", tool, source).CombinedOutput(); err != nil {
		t.Fatalf("build signer: %v\n%s", err, output)
	}
	t.Setenv("PATH", root)
	var diagnostic bytes.Buffer
	progress := buildprogress.New(&diagnostic, false)
	defer progress.Close()
	ctx := buildprogress.Observe(context.Background(), progress.Handle, false)
	signPigBuild(ctx, progress, "pig", &diagnostic)
	for _, want := range []string{"warning: Signing binary failed", "signer denied", "hint:"} {
		if !strings.Contains(diagnostic.String(), want) {
			t.Errorf("missing %q in %q", want, diagnostic.String())
		}
	}
}

func TestBuildCommandReportsCompilerFailureAndPhase(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("PIG_BIN", filepath.Join(root, "pig"))
	t.Setenv("GOFLAGS", "")
	if err := os.MkdirAll(filepath.Join(root, "cmd", "pig"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+pigModulePath+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cmd", "pig", "main.go"), []byte("package main\nfunc main() { missingBuildSymbol() }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostic bytes.Buffer
	if code := runBuildCommand([]string{"build"}, &out, &diagnostic); code != 1 {
		t.Fatalf("exit=%d: %s", code, &diagnostic)
	}
	for _, want := range []string{"Compiling and linking Go binary", "missingBuildSymbol", "hint:"} {
		if !strings.Contains(diagnostic.String(), want) {
			t.Errorf("missing %q in %q", want, diagnostic.String())
		}
	}
	if strings.ContainsAny(diagnostic.String(), "\x1b\r") {
		t.Fatalf("non-TTY escapes: %q", diagnostic.String())
	}
}
