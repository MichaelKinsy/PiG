//go:build linux

package tsruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// envSelfDigest makes a copy of this test binary print what it is, instead of running tests, so a test can see which file a start ran.
const envSelfDigest = "PIG_TSRUNTIME_TEST_SELF_DIGEST"

func init() {
	if os.Getenv(envSelfDigest) != "1" {
		return
	}
	data, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	sum := sha256.Sum256(data)
	fmt.Println(hex.EncodeToString(sum[:]), os.Args[0])
	os.Exit(0)
}

// selfCopy writes a copy of this test binary at path with marker appended (trailing bytes do not stop an ELF file from running) and
// returns its digest.
func selfCopy(t *testing.T, path, marker string) string {
	t.Helper()
	data, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, marker...)
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	return digestOf(data)
}

// A launch runs the file that was verified, not the file at the path at exec time: after Verify returns, the path is renamed over by
// another executable and the verified one still runs.
func TestExecPathRunsTheVerifiedFileAfterTheSwap(t *testing.T) {
	t.Setenv(envSelfDigest, "1")
	dir := t.TempDir()
	exe := filepath.Join(dir, "pig-ts-host")
	verified := selfCopy(t, exe, "A")
	var probed string
	v := NewVerifier(nil)
	probe := v.probe
	v.probe = func(ctx context.Context, path string) (string, error) {
		probed = path
		return probe(ctx, path)
	}
	m := Manifest{ID: "aot", Kind: KindExecutable, Version: "1", Platform: Platform(), Provider: "test"}
	handle, reason, err := v.Verify(t.Context(), m, exe, verified)
	if err != nil {
		t.Fatalf("verify: %s: %v", reason, err)
	}
	defer func() { _ = handle.Close() }()
	if !strings.HasPrefix(probed, "/proc/self/fd/") {
		t.Fatalf("the --version probe started %q, want the verified descriptor", probed)
	}

	replacement := filepath.Join(dir, "replacement")
	swapped := selfCopy(t, replacement, "B")
	if err := os.Rename(replacement, exe); err != nil {
		t.Fatal(err)
	}
	path, err := handle.ExecPath()
	if err != nil {
		t.Fatal(err)
	}
	if path == exe {
		t.Fatalf("ExecPath is the path %s, not the verified file", path)
	}
	cmd := exec.Command(path)
	cmd.Args[0] = exe
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("start the verified file: %v", err)
	}
	if got, argv0, _ := strings.Cut(strings.TrimSpace(string(out)), " "); got != verified || argv0 != exe {
		t.Fatalf("the process that ran has digest %s and argv[0] %s; want the verified %s (swapped file %s) and argv[0] %s", got, argv0, verified, swapped, exe)
	}
	// What the same swap does to a start by path: it runs the replacement.
	out, err = exec.Command(exe).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _ := strings.Cut(strings.TrimSpace(string(out)), " "); got != swapped {
		t.Fatalf("a start by path ran %s, want the swapped file %s: the test does not distinguish the two", got, swapped)
	}
}

// A second Verify of an unchanged executable, which skips the hash and the probe from the in-process result, still hands back the
// file it opened for that call.
func TestExecPathAfterAnInProcessCacheHit(t *testing.T) {
	t.Setenv(envSelfDigest, "1")
	dir := t.TempDir()
	exe := filepath.Join(dir, "pig-ts-host")
	verified := selfCopy(t, exe, "A")
	v := NewVerifier(nil)
	m := Manifest{ID: "aot", Kind: KindExecutable, Version: "1", Platform: Platform(), Provider: "test"}
	first, _, err := v.Verify(t.Context(), m, exe, verified)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	second, _, err := v.Verify(t.Context(), m, exe, verified)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	swapped := filepath.Join(dir, "replacement")
	selfCopy(t, swapped, "B")
	if err := os.Rename(swapped, exe); err != nil {
		t.Fatal(err)
	}
	path, err := second.ExecPath()
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(path).Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _ := strings.Cut(strings.TrimSpace(string(out)), " "); got != verified {
		t.Fatalf("ran %s, want the verified %s", got, verified)
	}
}

// openAppendAfterExit opens path for appending. The kernel releases a file that a process executed a moment after the process exits and
// its parent has reaped it, so an open for writing right after the probe ran may get ETXTBSY; it succeeds once the release is done.
func openAppendAfterExit(t *testing.T, path string) *os.File {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
		if err == nil {
			return file
		}
		if !errors.Is(err, syscall.ETXTBSY) || time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A write to the verified file in place after Verify is refused when the launch asks for its path, and so is a launch after the
// handle was released.
func TestExecPathRefusesAnExecutableWrittenAfterVerifyAndAReleasedHandle(t *testing.T) {
	t.Setenv(envSelfDigest, "1")
	dir := t.TempDir()
	exe := filepath.Join(dir, "pig-ts-host")
	verified := selfCopy(t, exe, "A")
	v := NewVerifier(nil)
	m := Manifest{ID: "aot", Kind: KindExecutable, Version: "1", Platform: Platform(), Provider: "test"}
	handle, _, err := v.Verify(t.Context(), m, exe, verified)
	if err != nil {
		t.Fatal(err)
	}
	pastTick(t, exe)
	file := openAppendAfterExit(t, exe)
	if _, err := file.WriteString("x"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if path, err := handle.ExecPath(); err == nil || !strings.Contains(err.Error(), "changed after it was verified") {
		t.Fatalf("ExecPath after an in-place write: %q, %v; want a refusal", path, err)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if path, err := handle.ExecPath(); err == nil {
		t.Fatalf("ExecPath of a released handle: %q, want an error (its descriptor number may name another file by now)", path)
	}
	var none *Executable
	if err := none.Close(); err != nil {
		t.Fatal(err)
	}
}

// A script cannot start from a descriptor (its interpreter would open the path after exec closed it), so it starts by path.
func TestScriptStartsByPath(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "host")
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(exe, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	v := NewVerifier(nil)
	v.probe = func(_ context.Context, path string) (string, error) {
		if path != exe {
			t.Errorf("probe started %q, want the path %q", path, exe)
		}
		return "", nil
	}
	m := Manifest{ID: "aot", Kind: KindExecutable, Version: "1", Platform: Platform(), Provider: "test"}
	handle, reason, err := v.Verify(t.Context(), m, exe, digestOf([]byte(script)))
	if err != nil {
		t.Fatalf("%s: %v", reason, err)
	}
	defer func() { _ = handle.Close() }()
	if path, err := handle.ExecPath(); err != nil || path != exe {
		t.Fatalf("ExecPath = %q, %v; want the path", path, err)
	}
}
