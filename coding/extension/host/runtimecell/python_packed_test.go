package runtimecell_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/runtimecell"
)

func TestBuildPythonPackedCellBuildsCachedRunner(t *testing.T) {
	python := "python3"
	if runtime.GOOS == "windows" {
		python = "python"
	}
	if _, err := exec.LookPath(python); err != nil {
		t.Skipf("%s not found: %v", python, err)
	}
	// Run against this checkout's SDK, not a copy staged under the user's
	// config root.
	sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk-py"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_SDK_PY_ROOT", sdkRoot)
	rootA := writePythonFactoryModule(t, "py_a", "a-ext", "a")
	rootB := writePythonFactoryModule(t, "py_b", "b-ext", "b")
	cache := t.TempDir()
	ctx := context.Background()
	cell, err := runtimecell.BuildPythonPackedCell(ctx, cache, "cell:python-test", []runtimecell.PythonExtension{
		{Name: "b-ext", Root: rootB, Package: "py_b", Factory: "new_extension", Hash: "hb"},
		{Name: "a-ext", Root: rootA, Package: "py_a", Factory: "new_extension", Hash: "ha"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cell.Cached || cell.BinaryPath == "" || cell.Hash == "" {
		t.Fatalf("first python cell = %+v", cell)
	}
	cached, err := runtimecell.BuildPythonPackedCell(ctx, cache, "cell:python-test", []runtimecell.PythonExtension{
		{Name: "a-ext", Root: rootA, Package: "py_a", Factory: "new_extension", Hash: "ha"},
		{Name: "b-ext", Root: rootB, Package: "py_b", Factory: "new_extension", Hash: "hb"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !cached.Cached || cached.BinaryPath != cell.BinaryPath || cached.Hash != cell.Hash {
		t.Fatalf("cached python cell = %+v, want cached same artifact as %+v", cached, cell)
	}
	sockA, cleanupA := unixListenerRust(t)
	defer cleanupA()
	sockB, cleanupB := unixListenerRust(t)
	defer cleanupB()
	cmdCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, python, cell.BinaryPath)
	cmd.Env = append(os.Environ(), runtimecell.SocketEnvName("a-ext")+"="+sockA.Addr().String(), runtimecell.SocketEnvName("b-ext")+"="+sockB.Addr().String())
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start python runner: %v", err)
	}
	regA := acceptRegisterRust(t, sockA)
	regB := acceptRegisterRust(t, sockB)
	seen := map[string]bool{regA.Name: true, regB.Name: true}
	if !seen["a-ext"] || !seen["b-ext"] {
		t.Fatalf("registered names = %v/%v", regA.Name, regB.Name)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("python runner wait: %v\nstderr:\n%s", err, stderr.String())
	}
}

func writePythonFactoryModule(t *testing.T, moduleName, extName, toolName string) string {
	t.Helper()
	dir := t.TempDir()
	src := fmt.Sprintf(`import pig_sdk


def new_extension():
    ext = pig_sdk.Extension(%q)
    ext.tool(%q, "test tool", {"type": "object"}, lambda ctx, params: {"content": "ok"})
    return ext
`, extName, toolName)
	if err := os.WriteFile(filepath.Join(dir, moduleName+".py"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
