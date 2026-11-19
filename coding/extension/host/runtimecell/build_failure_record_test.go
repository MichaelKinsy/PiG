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

// failingGoCommandSource is a go command whose `build` counts itself and fails with PIG_TEST_GO_BUILD_OUTPUT every time;
// every other subcommand runs the real go with GOROOT removed, as the wrapper in build_failure_cache_test.go does.
const failingGoCommandSource = `package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "build" {
		countPath := os.Getenv("PIG_TEST_GO_BUILD_COUNT")
		count := 0
		if data, err := os.ReadFile(countPath); err == nil {
			count, _ = strconv.Atoi(string(data))
		}
		if err := os.WriteFile(countPath, []byte(strconv.Itoa(count+1)), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		fmt.Fprint(os.Stderr, os.Getenv("PIG_TEST_GO_BUILD_OUTPUT"))
		os.Exit(1)
	}
	cmd := exec.Command(os.Getenv("PIG_TEST_REAL_GO"), args...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GOROOT=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			os.Exit(exit.ExitCode())
		}
		os.Exit(2)
	}
}
`

// installFailingGo puts a go command on PATH whose every build fails with output, and returns the file that counts the builds.
func installFailingGo(t *testing.T, output string) (countPath string) {
	t.Helper()
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "pkg", "tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(binDir, "go")
	if runtime.GOOS == "windows" {
		fake += ".exe"
	}
	source := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(source, []byte(failingGoCommandSource), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(realGo, "build", "-o", fake, source).CombinedOutput(); err != nil {
		t.Fatalf("build fake go: %v\n%s", err, out)
	}
	countPath = filepath.Join(t.TempDir(), "count")
	t.Setenv("PIG_TEST_REAL_GO", realGo)
	t.Setenv("PIG_TEST_GO_BUILD_COUNT", countPath)
	t.Setenv("PIG_TEST_GO_BUILD_OUTPUT", output)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOROOT", "")
	return countPath
}

// pig additive (D20): a compiler diagnostic is recorded against the cell's inputs, so an unchanged start reports the
// recorded failure without compiling it again and a changed input compiles. A failure that is not a property of the
// inputs, whether network, module or disk, is never recorded: the next start compiles again. The go command is a wrapper
// that counts its builds, so the assertion is on invocations of the compiler, not on a message.
func TestOnlyCompilerDiagnosticsAreRecordedAgainstTheCellInputs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		output   string
		recorded bool
	}{
		{"compiler diagnostic", "# example.com/x\n./extension.go:6:43: undefined: missing\n", true},
		{"network", "go: proxy.golang.org: temporary network failure\n", false},
		{"module resolution", "# example.com/x\ngo: updates to go.mod needed; to update it: go mod tidy\n", false},
		{"disk", "# example.com/x\ncompile: writing output: no space left on device\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configRoot := t.TempDir()
			t.Setenv("PIG_HOME", configRoot)
			countPath := installFailingGo(t, tc.output)
			extension := goExtensionFixture(t, "x", `sdk.New("x")`)
			cacheRoot := filepath.Join(configRoot, "cache")
			build := func(extension GoExtension) error {
				_, err := BuildGoPackedCell(context.Background(), cacheRoot, "cell", []GoExtension{extension})
				if err == nil {
					t.Fatal("a failing go command built the cell")
				}
				return err
			}
			cachedFailure := func(err error) bool {
				failure, ok := errors.AsType[*BuildFailure](err)
				return ok && failure.Cached && strings.Contains(err.Error(), "cached build failure (inputs unchanged)")
			}

			first := build(extension)
			if cachedFailure(first) || buildCount(t, countPath) != "1" {
				t.Fatalf("first build: %v after %s compiles, want a fresh failure after 1", first, buildCount(t, countPath))
			}
			second := build(extension)
			if tc.recorded {
				if !cachedFailure(second) || buildCount(t, countPath) != "1" {
					t.Fatalf("unchanged start: %v after %s compiles, want the recorded failure without compiling", second, buildCount(t, countPath))
				}
				if !strings.Contains(second.Error(), "undefined: missing") || !strings.Contains(second.Error(), RetryCachedBuildCommand) {
					t.Fatalf("the recorded failure lost its cause or the retry command: %v", second)
				}
			} else if cachedFailure(second) || buildCount(t, countPath) != "2" {
				t.Fatalf("unchanged start: %v after %s compiles, want it compiled again, nothing recorded", second, buildCount(t, countPath))
			}

			if tc.recorded {
				// `pig extensions cache prune --failures` removes the record, so the same inputs compile again.
				report, err := PruneCaches(CacheLifecycleOptions{CacheRoot: cacheRoot, RemoveFailures: true})
				if err != nil || report.Removed != 1 {
					t.Fatalf("prune --failures removed %d entries, %v; want the recorded failure", report.Removed, err)
				}
				if again := build(extension); cachedFailure(again) || buildCount(t, countPath) != "2" {
					t.Fatalf("after prune --failures: %v after %s compiles, want a compile (2)", again, buildCount(t, countPath))
				}
			}

			changed := extension
			changed.Hash += "-edited"
			if third := build(changed); cachedFailure(third) || buildCount(t, countPath) != "3" {
				t.Fatalf("changed input: %v after %s compiles, want a compile (3)", third, buildCount(t, countPath))
			}
		})
	}
}
