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
	"time"
)

// goExtensionFixture writes a compilable factory extension and returns its cell input.
func goExtensionFixture(t *testing.T, name string, body string) GoExtension {
	t.Helper()
	sdkRoot, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "extensions", "sdk"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_SDK_GO_ROOT", sdkRoot)
	root := t.TempDir()
	goMod := "module example.com/" + name + "\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	source := "package " + name + "\n\nimport sdk \"github.com/MichaelKinsy/PiG/extensions/sdk\"\n\nfunc Extension() *sdk.Extension { return " + body + " }\n"
	if err := os.WriteFile(filepath.Join(root, "extension.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return GoExtension{Name: name, Root: root, ModulePath: "example.com/" + name, Package: "example.com/" + name, Factory: "Extension", Hash: name + "-source"}
}

// A go command with GOROOT inherited from another installation is the reported
// failure: mise exports GOROOT for the project directory while PATH resolves
// another release. The build must use the installation of the go command it runs.
func TestBuildGoPackedCellIgnoresInheritedGOROOT(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	t.Setenv("GOROOT", t.TempDir())
	extension := goExtensionFixture(t, "fine", `sdk.New("fine")`)
	cell, err := BuildGoPackedCell(context.Background(), filepath.Join(configRoot, "cache"), "fine-cell", []GoExtension{extension})
	if err != nil {
		t.Fatalf("build with an inherited GOROOT of another installation: %v", err)
	}
	if _, err := os.Stat(cell.BinaryPath); err != nil {
		t.Fatal(err)
	}
}

const releaseMismatchGoSource = `package main

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
			os.Exit(2)
		}
		for range 3 {
			fmt.Fprintln(os.Stderr, "# internal/goarch")
			fmt.Fprintln(os.Stderr, "compile: version \"go1.26.7\" does not match go tool version \"go1.26.1\"")
		}
		os.Exit(1)
	}
	cmd := exec.Command(os.Getenv("PIG_TEST_REAL_GO"), args...)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GOROOT=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
}
`

// installMismatchedGo puts a go command on PATH whose compiler belongs to another release.
func installMismatchedGo(t *testing.T) (countPath, compilerPath string) {
	t.Helper()
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	toolDir := filepath.Join(root, "pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH)
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
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
	if err := os.WriteFile(source, []byte(releaseMismatchGoSource), 0o600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command(realGo, "build", "-o", fake, source).CombinedOutput(); err != nil {
		t.Fatalf("build fake go: %v\n%s", err, output)
	}
	countPath = filepath.Join(t.TempDir(), "count")
	t.Setenv("PIG_TEST_REAL_GO", realGo)
	t.Setenv("PIG_TEST_GO_BUILD_COUNT", countPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOROOT", "")
	return countPath, filepath.Join(toolDir, exeNameForRuntime("compile"))
}

func buildCount(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "0"
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A toolchain whose compiler is another release fails every cell for one
// reason. Each failure is one line naming both releases, keeps the compiler
// output in a log, is found again without compiling, and is retried only when
// the compiler is replaced.
func TestBuildGoPackedCellReportsReleaseMismatchOnceAndRecordsIt(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	countPath, compiler := installMismatchedGo(t)
	extension := goExtensionFixture(t, "mismatch", `sdk.New("mismatch")`)
	cacheRoot := filepath.Join(configRoot, "cache")

	var failures []*BuildFailure
	for _, key := range []string{"cell-a", "cell-b", "cell-c"} {
		_, err := BuildGoPackedCell(context.Background(), cacheRoot, key, []GoExtension{extension})
		failure, ok := errors.AsType[*BuildFailure](err)
		if !ok {
			t.Fatalf("%s: err = %v, want a BuildFailure", key, err)
		}
		if strings.Contains(err.Error(), "\n") || strings.Contains(err.Error(), "go.mod") || strings.Count(err.Error(), "go1.26.7") != 1 {
			t.Fatalf("%s: error is not one summary line: %q", key, err.Error())
		}
		for _, want := range []string{"go1.26.7", "go1.26.1", "reinstall"} {
			if !strings.Contains(failure.Summary, want) {
				t.Fatalf("%s: summary %q lacks %q", key, failure.Summary, want)
			}
		}
		failures = append(failures, failure)
	}
	if failures[0].Cause != failures[1].Cause || failures[1].Cause != failures[2].Cause {
		t.Fatalf("one root cause reported as %q, %q, %q", failures[0].Cause, failures[1].Cause, failures[2].Cause)
	}
	if got := buildCount(t, countPath); got != "1" {
		t.Fatalf("go build ran %s times for one broken toolchain, want 1", got)
	}
	log, err := os.ReadFile(failures[0].Log)
	if err != nil {
		t.Fatalf("read build log: %v", err)
	}
	if !strings.Contains(string(log), "does not match go tool version") || !strings.Contains(string(log), "--- generated go.mod ---") {
		t.Fatalf("log lacks the complete output:\n%s", log)
	}

	// The next start: nothing is compiled and the recorded failure is reported.
	brokenToolchains.Clear()
	_, err = BuildGoPackedCell(context.Background(), cacheRoot, "cell-a", []GoExtension{extension})
	recorded, ok := errors.AsType[*BuildFailure](err)
	if !ok || !recorded.Cached || !strings.Contains(err.Error(), RetryCachedBuildCommand) || strings.Contains(err.Error(), "\n") {
		t.Fatalf("second start err = %v, want the recorded one-line failure with the retry command", err)
	}
	if got := buildCount(t, countPath); got != "1" {
		t.Fatalf("go build ran %s times across two starts, want 1", got)
	}

	// A compiler replaced by a repair changes an input, so the build runs again.
	if err := os.WriteFile(compiler, []byte("repaired"), 0o755); err != nil {
		t.Fatal(err)
	}
	brokenToolchains.Clear()
	if _, err := BuildGoPackedCell(context.Background(), cacheRoot, "cell-a", []GoExtension{extension}); err == nil {
		t.Fatal("the fake toolchain still fails")
	}
	if got := buildCount(t, countPath); got != "2" {
		t.Fatalf("go build ran %s times after the compiler changed, want 2", got)
	}
}

// A compile error is reported as its first diagnostic; the rest stays in the log.
func TestBuildGoPackedCellCompileErrorIsOneLineWithLog(t *testing.T) {
	configRoot := t.TempDir()
	t.Setenv("PIG_HOME", configRoot)
	extension := goExtensionFixture(t, "broken", `sdk.New("broken", "extra")`)
	_, err := BuildGoPackedCell(context.Background(), filepath.Join(configRoot, "cache"), "broken-cell", []GoExtension{extension})
	failure, ok := errors.AsType[*BuildFailure](err)
	if !ok {
		t.Fatalf("err = %v, want a BuildFailure", err)
	}
	if strings.Contains(err.Error(), "\n") || strings.Contains(err.Error(), "go.mod") {
		t.Fatalf("error is not one line: %q", err.Error())
	}
	if !strings.Contains(failure.Summary, "extension.go:") || !strings.Contains(failure.Cause, "too many arguments") || strings.Contains(failure.Cause, "extension.go") {
		t.Fatalf("summary %q / cause %q", failure.Summary, failure.Cause)
	}
	if log, err := os.ReadFile(failure.Log); err != nil || !strings.Contains(string(log), "# example.com/broken") {
		t.Fatalf("log %q: %v", failure.Log, err)
	}
}

func TestBuildLogsAreBounded(t *testing.T) {
	cacheRoot := t.TempDir()
	for i := range maxBuildLogs + 10 {
		writeBuildLog(cacheRoot, string(rune('a'+i%26))+strings.Repeat("x", i), []byte("output"))
	}
	entries, err := os.ReadDir(filepath.Join(cacheRoot, buildLogDirectory))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > maxBuildLogs {
		t.Fatalf("%d logs kept, want at most %d", len(entries), maxBuildLogs)
	}
	big := writeBuildLog(cacheRoot, "big", make([]byte, maxBuildLogBytes+1000))
	if info, err := os.Stat(big); err != nil || info.Size() > maxBuildLogBytes+100 {
		t.Fatalf("large log size = %v, %v", info, err)
	}
}

func TestRustBuildFailureSummarizesRustcDiagnostic(t *testing.T) {
	cacheRoot := t.TempDir()
	output := []byte("error[E0425]: cannot find value `x` in this scope\n --> src/main.rs:3:5\n  |\n3 |     x\n  |     ^ not found\n\nerror: could not compile `cell` (bin \"cell\") due to 1 previous error\n")
	failure, recordable := RustBuildFailure(cacheRoot, "k", "/scratch", errors.New("exit status 101"), output)
	if !recordable {
		t.Fatal("a rustc diagnostic is a property of the inputs")
	}
	if want := "cargo build: error[E0425]: cannot find value `x` in this scope (src/main.rs:3:5)"; failure.Summary != want {
		t.Fatalf("summary = %q, want %q", failure.Summary, want)
	}
	if strings.Contains(failure.Cause, "src/main.rs") {
		t.Fatalf("cause %q depends on the failing extension's path", failure.Cause)
	}
	if log, err := os.ReadFile(failure.Log); err != nil || !strings.Contains(string(log), "could not compile") {
		t.Fatalf("log: %v", err)
	}
	network := []byte("error: failed to get `serde` as a dependency\n\nCaused by:\n  network failure\n")
	if failure, recordable := RustBuildFailure(cacheRoot, "n", "/scratch", errors.New("exit status 101"), network); recordable || !strings.Contains(failure.Summary, "failed to get") {
		t.Fatalf("network failure = %+v recordable=%v", failure, recordable)
	}
}

// Recorded build failures are removed on request, including one a current
// extension uses (that is the point: the next start compiles again), and built
// artifacts stay.
func TestPruneRemovesRecordedFailuresOnRequest(t *testing.T) {
	cacheRoot := t.TempDir()
	fail := func(digest string) string {
		dir := filepath.Join(cacheRoot, "cells", "go", digest)
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := publishCellFailure(t.Context(), dir, digest, "go", &BuildFailure{Summary: "s", Cause: "c"}); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	current, other := fail("aaaa"), fail("bbbb")
	built := writeLifecycleEntry(t, cacheRoot, "cells/go", "cccc", time.Now())
	options := CacheLifecycleOptions{CacheRoot: cacheRoot, Current: map[string]struct{}{filepath.Clean(current): {}}}

	kept, err := PruneCaches(options)
	if err != nil || kept.Removed != 0 {
		t.Fatalf("without the option: removed %d, %v", kept.Removed, err)
	}
	options.RemoveFailures = true
	report, err := PruneCaches(options)
	if err != nil || report.Removed != 2 {
		t.Fatalf("removed %d, %v; want both failures", report.Removed, err)
	}
	for _, gone := range []string{current, other} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s still present: %v", gone, err)
		}
	}
	if _, err := os.Stat(built); err != nil {
		t.Fatalf("built artifact removed: %v", err)
	}
}

// A log that cannot be written leaves the failure one line without a path; the
// failure itself is not lost.
func TestBuildFailureWithoutWritableLogKeepsTheSummary(t *testing.T) {
	cacheRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(cacheRoot, buildLogDirectory), []byte("a file, not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if path := writeBuildLog(cacheRoot, "k", []byte("output")); path != "" {
		t.Fatalf("log path %q written under a file", path)
	}
	failure, _ := RustBuildFailure(cacheRoot, "k", "/scratch", errors.New("exit status 101"), []byte("error: boom\n"))
	if failure.Log != "" || strings.Contains(failure.Error(), "details") || !strings.Contains(failure.Error(), "boom") {
		t.Fatalf("failure = %q", failure.Error())
	}
}
