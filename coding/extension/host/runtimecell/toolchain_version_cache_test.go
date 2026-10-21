package runtimecell

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/fsretry"
	"github.com/MichaelKinsy/PiG/internal/testenv"
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
	os.Exit(testenv.RunScoped(m, "pig-cell-"))
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
	// Windows denies replacing an executable for a short time after the process that ran it exits, so the replacement retries that transient denial.
	if err := fsretry.Rename(t.Context(), temporary, targetPath, nil); err != nil {
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

// startSlowProbe starts a version probe of toolPath that answers after sleep and returns when the probe has started.
func startSlowProbe(t *testing.T, cacheRoot, toolPath string, args []string, logPath string, sleep string) <-chan string {
	t.Helper()
	t.Setenv("PIG_TEST_TOOLCHAIN_SLEEP", sleep)
	done := make(chan string, 1)
	go func() { done <- commandVersion(cacheRoot, toolPath, args...) }()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if data, err := os.ReadFile(logPath); err == nil && strings.Contains(string(data), "probe\n") {
			return done
		}
		if time.Now().After(deadline) {
			t.Fatal("the slow probe never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func setToolchainBounds(t *testing.T, probe, lockWait time.Duration) {
	t.Helper()
	oldProbe, oldLock := toolchainProbeTimeout, toolchainLockWait
	toolchainProbeTimeout, toolchainLockWait = probe, lockWait
	t.Cleanup(func() { toolchainProbeTimeout, toolchainLockWait = oldProbe, oldLock })
}

// A start that finds a peer probing the same record does not wait for the peer past toolchainLockWait: it probes for itself and answers.
func TestCommandVersionDoesNotWaitForAPeerProbeWithoutBound(t *testing.T) {
	setToolchainBounds(t, time.Minute, 200*time.Millisecond)
	toolPath := filepath.Join(t.TempDir(), exeNameForRuntime("go"))
	copyTestExecutable(t, toolPath)
	logPath := filepath.Join(t.TempDir(), "probes.log")
	t.Setenv("PIG_TEST_TOOLCHAIN_PROBE_LOG", logPath)
	t.Setenv("PIG_TEST_TOOLCHAIN_VERSION", "go-v1")
	args := []string{"-test.run=^TestCommandVersionProbeHelper$"}
	cacheRoot := t.TempDir()

	holder := startSlowProbe(t, cacheRoot, toolPath, args, logPath, "4s")
	t.Setenv("PIG_TEST_TOOLCHAIN_SLEEP", "0s")
	start := time.Now()
	got := commandVersion(cacheRoot, toolPath, args...)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("the second start waited %v for the peer's probe", elapsed)
	}
	if !strings.Contains(got, "go-v1") {
		t.Fatalf("second start = %q, want the version", got)
	}
	if peer := <-holder; !strings.Contains(peer, "go-v1") {
		t.Fatalf("peer = %q", peer)
	}
}

// A probe that does not answer within toolchainProbeTimeout is ended and reported as an error instead of holding the start.
func TestCommandVersionEndsAProbeThatDoesNotAnswer(t *testing.T) {
	setToolchainBounds(t, 300*time.Millisecond, time.Minute)
	toolPath := filepath.Join(t.TempDir(), exeNameForRuntime("go"))
	copyTestExecutable(t, toolPath)
	t.Setenv("PIG_TEST_TOOLCHAIN_PROBE_LOG", filepath.Join(t.TempDir(), "probes.log"))
	t.Setenv("PIG_TEST_TOOLCHAIN_VERSION", "go-v1")
	t.Setenv("PIG_TEST_TOOLCHAIN_SLEEP", "10s")
	start := time.Now()
	got := commandVersion(t.TempDir(), toolPath, "-test.run=^TestCommandVersionProbeHelper$")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the probe ran %v past its bound", elapsed)
	}
	if !strings.HasPrefix(got, "error:") {
		t.Fatalf("commandVersion = %q, want an error", got)
	}
}

// Two projects whose selectors differ (go.mod here) each keep their record: starting in one after the other does not make the first probe again.
func TestCommandVersionKeepsOneRecordPerSelectorState(t *testing.T) {
	root := t.TempDir()
	dirs := []string{filepath.Join(root, "a"), filepath.Join(root, "b")}
	writeRuntimecellTestFile(t, filepath.Join(dirs[0], "go.mod"), "module a\n")
	writeRuntimecellTestFile(t, filepath.Join(dirs[1], "go.mod"), "module project-b-with-a-longer-name\n")
	toolPath := filepath.Join(t.TempDir(), exeNameForRuntime("go"))
	copyTestExecutable(t, toolPath)
	logPath := filepath.Join(t.TempDir(), "probes.log")
	t.Setenv("PIG_TEST_TOOLCHAIN_PROBE_LOG", logPath)
	t.Setenv("PIG_TEST_TOOLCHAIN_VERSION", "go-v1")
	args := []string{"-test.run=^TestCommandVersionProbeHelper$"}
	cacheRoot := t.TempDir()
	for range 3 {
		for _, dir := range dirs {
			t.Chdir(dir)
			if got := commandVersion(cacheRoot, toolPath, args...); !strings.Contains(got, "go-v1") {
				t.Fatalf("commandVersion in %s = %q", dir, got)
			}
		}
	}
	if probes := probeCount(t, logPath); probes != 2 {
		t.Fatalf("probes alternating between two projects = %d, want 2 (one per project)", probes)
	}
	invalidateCommandVersion(cacheRoot, toolPath, args...)
	t.Chdir(dirs[0])
	commandVersion(cacheRoot, toolPath, args...)
	if probes := probeCount(t, logPath); probes != 3 {
		t.Fatalf("probes after invalidation = %d, want 3", probes)
	}
}

// A command keeps the records of at most toolchainSlotsPerCommand selector states, the newest.
func TestCommandVersionPrunesOldRecords(t *testing.T) {
	root := t.TempDir()
	toolPath := filepath.Join(t.TempDir(), exeNameForRuntime("go"))
	copyTestExecutable(t, toolPath)
	t.Setenv("PIG_TEST_TOOLCHAIN_PROBE_LOG", filepath.Join(t.TempDir(), "probes.log"))
	t.Setenv("PIG_TEST_TOOLCHAIN_VERSION", "go-v1")
	args := []string{"-test.run=^TestCommandVersionProbeHelper$"}
	cacheRoot := t.TempDir()
	for i := range toolchainSlotsPerCommand + 4 {
		dir := filepath.Join(root, fmt.Sprint(i))
		writeRuntimecellTestFile(t, filepath.Join(dir, "go.mod"), strings.Repeat("x", i+1))
		t.Chdir(dir)
		commandVersion(cacheRoot, toolPath, args...)
		time.Sleep(20 * time.Millisecond)
	}
	if got := len(toolchainVersionSlots(cacheRoot, toolPath, args)); got != toolchainSlotsPerCommand {
		t.Fatalf("records kept = %d, want %d", got, toolchainSlotsPerCommand)
	}
}
