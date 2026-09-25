package runtimecell

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuildGoPackedCellCachesCompileFailure(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_SDK_GO_ROOT", sdkRoot)
	extensionRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(extensionRoot, "go.mod"), []byte("module example.com/broken\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extensionRoot, "extension.go"), []byte("package broken\n\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\n\nfunc Extension() *sdk.Extension { return sdk.New(\"broken\", \"extra\") }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	extensions := []GoExtension{{
		Name: "broken", Root: extensionRoot, ModulePath: "example.com/broken", Package: "example.com/broken", Factory: "Extension", Hash: "source-a",
	}}
	cacheRoot := filepath.Join(configRoot, "cache")
	if _, err := BuildGoPackedCell(context.Background(), cacheRoot, "broken-cell", extensions); err == nil || !strings.Contains(err.Error(), "too many arguments") || strings.Contains(err.Error(), "cached build failure") {
		t.Fatalf("first compile error = %v", err)
	}
	if _, err := BuildGoPackedCell(context.Background(), cacheRoot, "broken-cell", extensions); err == nil || !strings.Contains(err.Error(), "cached build failure (inputs unchanged)") || !strings.Contains(err.Error(), "too many arguments") {
		t.Fatalf("cached compile error = %v", err)
	}
}

func TestGoFailureCacheOnlyAcceptsCompilerDiagnostics(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   bool
	}{
		{name: "type error", output: "# example.com/ext\n/path/extension.go:6:43: too many arguments in call to sdk.New\n", want: true},
		{name: "syntax error without column", output: "# example.com/ext\nC:\\work\\extension.go:6: syntax error: unexpected name x\n", want: true},
		{name: "network", output: "go: proxy.golang.org: temporary network failure\n"},
		{name: "module setup after package header", output: "# example.com/ext\ngo: updates to go.mod needed; to update it: go mod tidy\n"},
		{name: "unpositioned tool failure", output: "# example.com/ext\ncompile: writing output: no space left on device\n"},
		{name: "position without compiler package", output: "/path/extension.go:6:43: permission denied\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hasGoCompilerDiagnostic([]byte(test.output)); got != test.want {
				t.Fatalf("hasGoCompilerDiagnostic() = %t, want %t for %q", got, test.want, test.output)
			}
		})
	}
}

func TestBuildFailureCacheCountsOneAttemptPerSourceAndSDKDigest(t *testing.T) {
	cacheRoot := t.TempDir()
	attempts := 0
	build := func(label string) func(string) (string, error) {
		return func(string) (string, error) {
			attempts++
			return "", cacheBuildFailure(errors.New("compile " + label + ": incompatible SDK API"))
		}
	}
	run := func(digest, label string) error {
		_, err := publishArtifactWithFailureCache(
			context.Background(),
			filepath.Join(cacheRoot, "cells", "go", digest),
			"runner",
			digest,
			"go",
			build(label),
		)
		return err
	}

	first := run("source-a-sdk-a", "source-a/sdk-a")
	if first == nil || first.Error() != "compile source-a/sdk-a: incompatible SDK API" {
		t.Fatalf("first build error = %v", first)
	}
	second := run("source-a-sdk-a", "source-a/sdk-a")
	if second == nil || !strings.Contains(second.Error(), "cached build failure (inputs unchanged): compile source-a/sdk-a: incompatible SDK API") {
		t.Fatalf("second build error = %v", second)
	}
	if attempts != 1 {
		t.Fatalf("unchanged failing build attempts = %d, want 1", attempts)
	}
	report, err := InspectCache(CacheLifecycleOptions{CacheRoot: cacheRoot})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Entries) != 1 || report.Entries[0].Class != CacheInactiveRetained || report.Entries[0].Reason != "cached build failure" {
		t.Fatalf("cached failure lifecycle = %+v", report.Entries)
	}

	if err := run("source-b-sdk-a", "source-b/sdk-a"); err == nil {
		t.Fatal("changed source unexpectedly built")
	}
	if attempts != 2 {
		t.Fatalf("attempts after source change = %d, want 2", attempts)
	}
	if err := run("source-b-sdk-b", "source-b/sdk-b"); err == nil {
		t.Fatal("changed SDK unexpectedly built")
	}
	if attempts != 3 {
		t.Fatalf("attempts after SDK change = %d, want 3", attempts)
	}
}

func TestBuildGoPackedCellRetriesTransientGoFailure(t *testing.T) {
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	fakeGo := filepath.Join(binDir, "go")
	if runtime.GOOS == "windows" {
		fakeGo += ".exe"
	}
	helperSource := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(helperSource, []byte(transientGoCommandSource), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(realGo, "build", "-o", fakeGo, helperSource).CombinedOutput(); err != nil {
		t.Fatalf("build fake go command: %v\n%s", err, output)
	}

	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_SDK_GO_ROOT", sdkRoot)
	extensionRoot := t.TempDir()
	goMod := "module example.com/transient\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"
	if err := os.WriteFile(filepath.Join(extensionRoot, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	source := "package transient\n\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\n\nfunc Extension() *sdk.Extension { return sdk.New(\"transient\") }\n"
	if err := os.WriteFile(filepath.Join(extensionRoot, "extension.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	countPath := filepath.Join(t.TempDir(), "build-count")
	t.Setenv("PIG_TEST_REAL_GO", realGo)
	t.Setenv("PIG_TEST_GO_BUILD_COUNT", countPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	extensions := []GoExtension{{
		Name: "transient", Root: extensionRoot, ModulePath: "example.com/transient", Package: "example.com/transient", Factory: "Extension", Hash: "source-a",
	}}
	cacheRoot := filepath.Join(configRoot, "cache")
	if _, err := BuildGoPackedCell(context.Background(), cacheRoot, "transient-cell", extensions); err == nil || !strings.Contains(err.Error(), "temporary network failure") {
		t.Fatalf("first build error = %v", err)
	}
	if _, err := BuildGoPackedCell(context.Background(), cacheRoot, "transient-cell", extensions); err != nil {
		t.Fatalf("second build should retry and succeed, got %v", err)
	}
	if count, err := os.ReadFile(countPath); err != nil || string(count) != "2" {
		t.Fatalf("go build attempts = %q, %v; want 2", count, err)
	}
}

const transientGoCommandSource = `package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "build" {
		countPath := os.Getenv("PIG_TEST_GO_BUILD_COUNT")
		count := 0
		if data, err := os.ReadFile(countPath); err == nil {
			count, _ = strconv.Atoi(string(data))
		}
		count++
		if err := os.WriteFile(countPath, []byte(strconv.Itoa(count)), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if count == 1 {
			fmt.Fprintln(os.Stderr, "go: proxy.golang.org: temporary network failure")
			os.Exit(1)
		}
	}
	cmd := exec.Command(os.Getenv("PIG_TEST_REAL_GO"), args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
`
