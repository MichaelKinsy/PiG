//go:build linux

package tsruntime

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// envSelfDigest makes a copy of this test binary print what it is, instead of running tests, so a test can see which file a start ran.
// The value "hold" makes it wait for its standard input to close after printing, so that a test can read what the kernel shows of the
// running process.
const envSelfDigest = "PIG_TSRUNTIME_TEST_SELF_DIGEST"

func init() {
	mode := os.Getenv(envSelfDigest)
	if mode != "1" && mode != "hold" {
		return
	}
	data, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	sum := sha256.Sum256(data)
	fmt.Println(hex.EncodeToString(sum[:]), os.Args[0])
	if mode == "hold" {
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
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
	if path == exe || filepath.Base(path) != "pig-ts-host" {
		t.Fatalf("ExecPath is %s, want a link named pig-ts-host to the verified file, not the path %s", path, exe)
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

// holdVerified verifies a copy of this test binary named name in a new directory and starts it through ExecPath in hold mode. It
// returns the running command, the handle, the verified digest, and the first line the process printed.
func holdVerified(t *testing.T, name string) (cmd *exec.Cmd, handle *Executable, stdin io.WriteCloser, verified, line string) {
	t.Helper()
	t.Setenv(envSelfDigest, "hold")
	exe := filepath.Join(t.TempDir(), name)
	verified = selfCopy(t, exe, "A")
	m := Manifest{ID: "aot", Kind: KindExecutable, Version: "1", Platform: Platform(), Provider: "test"}
	// The probe runs the copy in hold mode too: it must see its standard input close.
	v := NewVerifier(nil)
	v.probe = func(context.Context, string) (string, error) { return "", nil }
	handle, reason, err := v.Verify(t.Context(), m, exe, verified)
	if err != nil {
		t.Fatalf("%s: %v", reason, err)
	}
	path, err := handle.ExecPath()
	if err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(path)
	cmd.Args[0] = exe
	stdin, err = cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err = bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return cmd, handle, stdin, verified, strings.TrimSpace(line)
}

// The process a launch starts is named after the executable, as a start by path names it: comm (what ps, top, pgrep and OOM logs
// show) is the base name of the path the kernel was asked to run, and the link carries the executable's name; /proc/PID/exe is the
// verified file and argv[0] is the path.
func TestStartedProcessIsNamedAfterTheExecutable(t *testing.T) {
	for _, name := range []string{"pig-ts-host", "bun"} {
		t.Run(name, func(t *testing.T) {
			cmd, handle, stdin, verified, line := holdVerified(t, name)
			defer func() { _ = handle.Close() }()
			pid := cmd.Process.Pid
			comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
			if err != nil {
				t.Fatal(err)
			}
			exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
			if err != nil {
				t.Fatal(err)
			}
			cmdline, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
			if err != nil {
				t.Fatal(err)
			}
			_ = stdin.Close()
			if err := cmd.Wait(); err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(string(comm)); got != name {
				t.Errorf("comm = %q, want %q", got, name)
			}
			if filepath.Base(exe) != name || strings.Contains(exe, "/proc/") {
				t.Errorf("/proc/PID/exe = %q, want the verified file %s", exe, name)
			}
			if argv0, _, _ := strings.Cut(string(cmdline), "\x00"); filepath.Base(argv0) != name || strings.HasPrefix(argv0, "/proc/") || strings.Contains(argv0, "pig-exec-") {
				t.Errorf("argv[0] = %q, want the executable's path", argv0)
			}
			if got, _, _ := strings.Cut(line, " "); got != verified {
				t.Errorf("the process printed digest %s, want %s", got, verified)
			}
		})
	}
}

// The link lives in a private directory only its owner can enter, one directory per handle, and Close removes both.
func TestExecLinkDirectoryIsPrivateAndRemovedOnClose(t *testing.T) {
	t.Setenv(envSelfDigest, "1")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	exe := filepath.Join(t.TempDir(), "pig-ts-host")
	verified := selfCopy(t, exe, "A")
	m := Manifest{ID: "aot", Kind: KindExecutable, Version: "1", Platform: Platform(), Provider: "test"}
	v := NewVerifier(nil)
	handle, _, err := v.Verify(t.Context(), m, exe, verified)
	if err != nil {
		t.Fatal(err)
	}
	// The probe started the descriptor itself: no directory exists yet.
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Fatalf("Verify left %d entries in the temporary directory", len(entries))
	}
	path, err := handle.ExecPath()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || filepath.Dir(dir) != tmp || !strings.HasPrefix(filepath.Base(dir), "pig-exec-") || !ownedByCurrentUser(info) {
		t.Fatalf("link directory %s: %v, %v; want a 0700 directory of the current user named pig-exec-* in %s", dir, info, err, tmp)
	}
	if target, err := os.Readlink(path); err != nil || !strings.HasPrefix(target, "/proc/self/fd/") {
		t.Fatalf("link %s -> %q, %v; want /proc/self/fd/N", path, target, err)
	}
	if again, err := handle.ExecPath(); err != nil || again != path {
		t.Fatalf("second ExecPath = %q, %v; want the same link %q", again, err, path)
	}
	if err := handle.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the link directory survived Close: %v", err)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Fatalf("Close left %d entries in the temporary directory", len(entries))
	}
}

// Simultaneous starts of the same executable each get their own directory and each run the verified file.
func TestConcurrentExecLinksDoNotCollide(t *testing.T) {
	t.Setenv(envSelfDigest, "1")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	exe := filepath.Join(t.TempDir(), "pig-ts-host")
	verified := selfCopy(t, exe, "A")
	m := Manifest{ID: "aot", Kind: KindExecutable, Version: "1", Platform: Platform(), Provider: "test"}
	v := NewVerifier(nil)
	const starts = 12
	dirs := make(chan string, starts)
	var wg sync.WaitGroup
	for range starts {
		wg.Go(func() {
			handle, _, err := v.Verify(t.Context(), m, exe, verified)
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = handle.Close() }()
			path, err := handle.ExecPath()
			if err != nil {
				t.Error(err)
				return
			}
			dirs <- filepath.Dir(path)
			cmd := exec.Command(path)
			cmd.Args[0] = exe
			out, err := cmd.Output()
			if err != nil {
				t.Error(err)
				return
			}
			if got, _, _ := strings.Cut(strings.TrimSpace(string(out)), " "); got != verified {
				t.Errorf("a concurrent start ran %s, want %s", got, verified)
			}
		})
	}
	wg.Wait()
	close(dirs)
	seen := map[string]bool{}
	for dir := range dirs {
		if seen[dir] {
			t.Errorf("two starts shared the directory %s", dir)
		}
		seen[dir] = true
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Errorf("%d entries left in the temporary directory", len(entries))
	}
}

// A directory that already exists in the temporary directory, such as one a crashed process left or one planted, is never used: the
// start makes its own, and the planted link's target does not run.
func TestPlantedExecDirectoryIsNotReused(t *testing.T) {
	t.Setenv(envSelfDigest, "1")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	planted := filepath.Join(tmp, "pig-exec-planted")
	if err := os.Mkdir(planted, 0o700); err != nil {
		t.Fatal(err)
	}
	plantedExe := filepath.Join(t.TempDir(), "other")
	other := selfCopy(t, plantedExe, "B")
	if err := os.Symlink(plantedExe, filepath.Join(planted, "pig-ts-host")); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "pig-ts-host")
	verified := selfCopy(t, exe, "A")
	m := Manifest{ID: "aot", Kind: KindExecutable, Version: "1", Platform: Platform(), Provider: "test"}
	handle, _, err := NewVerifier(nil).Verify(t.Context(), m, exe, verified)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = handle.Close() }()
	path, err := handle.ExecPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) == planted {
		t.Fatalf("the start used the pre-existing directory %s", planted)
	}
	cmd := exec.Command(path)
	cmd.Args[0] = exe
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _ := strings.Cut(strings.TrimSpace(string(out)), " "); got != verified || got == other {
		t.Fatalf("ran %s, want the verified %s (not the planted %s)", got, verified, other)
	}
}

// A later process removes the directories a crashed process left, only when they are old, owned by the current user, directories,
// and named for it.
func TestSweepRemovesOnlyOldOwnedExecDirectories(t *testing.T) {
	tmp := t.TempDir()
	now := time.Now()
	mk := func(name string, age time.Duration) string {
		path := filepath.Join(tmp, name)
		if err := os.MkdirAll(filepath.Join(path, "inner"), 0o700); err != nil {
			t.Fatal(err)
		}
		old := now.Add(-age)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
		return path
	}
	stale := mk("pig-exec-stale", 2*time.Hour)
	fresh := mk("pig-exec-fresh", time.Minute)
	foreign := mk("other-stale", 2*time.Hour)
	file := filepath.Join(tmp, "pig-exec-file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "pig-exec-link")
	if err := os.Symlink(foreign, link); err != nil {
		t.Fatal(err)
	}
	sweepExecDirs(tmp, now)
	for path, want := range map[string]bool{stale: false, fresh: true, foreign: true, file: true, link: true} {
		_, err := os.Lstat(path)
		if exists := err == nil; exists != want {
			t.Errorf("%s exists = %v after the sweep, want %v", filepath.Base(path), exists, want)
		}
	}
}

// Where the private directory cannot be made, the start still runs the verified file, named by its descriptor.
func TestExecPathWithoutAPrivateDirectoryFallsBackToTheDescriptor(t *testing.T) {
	t.Setenv(envSelfDigest, "1")
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	exe := filepath.Join(t.TempDir(), "pig-ts-host")
	verified := selfCopy(t, exe, "A")
	m := Manifest{ID: "aot", Kind: KindExecutable, Version: "1", Platform: Platform(), Provider: "test"}
	handle, _, err := NewVerifier(nil).Verify(t.Context(), m, exe, verified)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = handle.Close() }()
	path, err := handle.ExecPath()
	if err != nil || !strings.HasPrefix(path, "/proc/self/fd/") {
		t.Fatalf("ExecPath = %q, %v; want /proc/self/fd/N", path, err)
	}
	cmd := exec.Command(path)
	cmd.Args[0] = exe
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _ := strings.Cut(strings.TrimSpace(string(out)), " "); got != verified {
		t.Fatalf("ran %s, want %s", got, verified)
	}
}
