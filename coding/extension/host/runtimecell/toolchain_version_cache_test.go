package runtimecell

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if logPath := os.Getenv("PIG_TEST_FAILING_GO_LOG"); logPath != "" {
		kind := "other"
		if len(os.Args) > 1 {
			kind = os.Args[1]
		}
		logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		_, writeErr := fmt.Fprintln(logFile, kind)
		closeErr := logFile.Close()
		if writeErr != nil || closeErr != nil {
			fmt.Fprintln(os.Stderr, writeErr, closeErr)
			os.Exit(2)
		}
		if kind == "version" {
			if _, err := fmt.Fprintln(os.Stdout, "go version go1.99.0 test/arch"); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, "intentional build failure")
		os.Exit(2)
	}
	os.Exit(m.Run())
}

// Each tool manager resolves its own nearest selector. A child selector for
// another manager must not hide an active parent go.work or .python-version
// from the durable version fingerprint.
func TestCommandVersionReprobesWhenParentSelectorChanges(t *testing.T) {
	cases := []struct {
		name           string
		tool           string
		parentSelector string
		childSelector  string
	}{
		{name: "parent go.work past child go.mod", tool: "go", parentSelector: "go.work", childSelector: "go.mod"},
		{name: "parent pyenv past child asdf", tool: "python3", parentSelector: ".python-version", childSelector: ".tool-versions"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			child := filepath.Join(root, "child")
			writeRuntimecellTestFile(t, filepath.Join(root, tc.parentSelector), "selector-v1\n")
			writeRuntimecellTestFile(t, filepath.Join(child, tc.childSelector), "mask\n")
			t.Chdir(child)

			toolPath := filepath.Join(t.TempDir(), exeNameForRuntime(tc.tool))
			copyTestExecutable(t, toolPath)
			logPath := filepath.Join(t.TempDir(), "probes.log")
			t.Setenv("PIG_TEST_TOOLCHAIN_PROBE_LOG", logPath)
			t.Setenv("PIG_TEST_TOOLCHAIN_VERSION", tc.tool+"-v1")
			args := []string{"-test.run=^TestCommandVersionProbeHelper$"}
			cacheRoot := t.TempDir()

			first := commandVersion(cacheRoot, toolPath, args...)
			if second := commandVersion(cacheRoot, toolPath, args...); second != first {
				t.Fatalf("warm output = %q, want %q", second, first)
			}
			if probes := probeCount(t, logPath); probes != 1 {
				t.Fatalf("warm probes = %d, want 1", probes)
			}

			writeRuntimecellTestFile(t, filepath.Join(root, tc.parentSelector), "selector-v2\n")
			t.Setenv("PIG_TEST_TOOLCHAIN_VERSION", tc.tool+"-v2")
			changed := commandVersion(cacheRoot, toolPath, args...)
			if !strings.Contains(changed, tc.tool+"-v2") || changed == first {
				t.Fatalf("changed selector output = %q, first = %q", changed, first)
			}
			if probes := probeCount(t, logPath); probes != 2 {
				t.Fatalf("changed selector probes = %d, want 2", probes)
			}
		})
	}
}

func TestGoPackedCellBuildFailureInvalidatesVersionProbe(t *testing.T) {
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	goCommand := filepath.Join(binDir, exeNameForRuntime("go"))
	copyTestExecutable(t, goCommand)
	logPath := filepath.Join(root, "go.log")
	t.Setenv("PIG_TEST_FAILING_GO_LOG", logPath)
	t.Setenv("PATH", binDir)

	sdkRoot := filepath.Join(root, "sdk")
	writeRuntimecellTestFile(t, filepath.Join(sdkRoot, "go.mod"), "module github.com/MichaelKinsy/PiG/extensions/sdk\n\ngo 1.26.0\n")
	writeRuntimecellTestFile(t, filepath.Join(sdkRoot, "sdk.go"), "package sdk\n")
	t.Setenv("PIG_SDK_GO_ROOT", sdkRoot)
	extensionRoot := filepath.Join(root, "extension")
	writeRuntimecellTestFile(t, filepath.Join(extensionRoot, "go.mod"), "module example.com/extension\n\ngo 1.26.0\n")
	writeRuntimecellTestFile(t, filepath.Join(extensionRoot, "extension.go"), "package extension\n")
	extensions := []GoExtension{{
		Name: "extension", Root: extensionRoot, ModulePath: "example.com/extension",
		Package: "example.com/extension", Factory: "Extension", Hash: "source",
	}}
	cacheRoot := filepath.Join(root, "cache")
	for range 2 {
		if _, err := BuildGoPackedCell(context.Background(), cacheRoot, "go-failure", extensions); err == nil || !strings.Contains(err.Error(), "intentional build failure") {
			t.Fatalf("build error = %v", err)
		}
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if versions := strings.Count(string(data), "version\n"); versions != 2 {
		t.Fatalf("version probes after two failed builds = %d, want 2; log:\n%s", versions, data)
	}
	if builds := strings.Count(string(data), "build\n"); builds != 2 {
		t.Fatalf("build attempts = %d, want 2; log:\n%s", builds, data)
	}
}

func copyTestExecutable(t *testing.T, targetPath string) {
	t.Helper()
	sourcePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	temporary := targetPath + ".new"
	target, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		t.Fatal(err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(targetPath)
	if err := os.Rename(temporary, targetPath); err != nil {
		t.Fatal(err)
	}
}

func writeRuntimecellTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
