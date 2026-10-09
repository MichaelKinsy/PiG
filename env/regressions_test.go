package env

// Regressions found in review against packages/env/src of Pi 1.0.4; each test names the upstream rule it holds.

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

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// ssh.ts changeKnownHosts: an existing known-hosts file that cannot be read fails the change (readFile rejects); it is
// never replaced by a file holding only the new keys.
func TestAcceptHostKeyKeepsAKnownHostsFileItCannotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions that bind the current user")
	}
	file := filepath.Join(t.TempDir(), "known_hosts")
	trusted := "pi-env-test ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOldKeyOldKeyOldKeyOldKeyOldKeyOldKeyOld"
	mustDo(os.WriteFile(file, []byte(trusted+"\n"), 0o600))
	mustDo(os.Chmod(file, 0o000))
	t.Cleanup(func() { _ = os.Chmod(file, 0o600) })
	target := SshTarget{Host: "h", KnownHostsFile: file, HostKeyAlias: "pi-env-test"}
	err := AcceptHostKey(target, []string{"pi-env-test ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTY="})
	if err == nil {
		t.Fatal("AcceptHostKey succeeded on a known-hosts file it cannot read")
	}
	if err := ForgetHostKey(target); err == nil {
		t.Fatal("ForgetHostKey succeeded on a known-hosts file it cannot read")
	}
	mustDo(os.Chmod(file, 0o600))
	if content := string(must(os.ReadFile(file))); content != trusted+"\n" {
		t.Fatalf("known-hosts file changed to %q", content)
	}
}

// connection.ts #start: the `exit` listener registered before the request rejects the start with
// `pi-env exited with code <code> before it was ready`, whatever else the exit tears down.
func TestConnectionReportsADaemonThatExitsBeforeItIsReady(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	for range 40 {
		connection := NewConnection(ConnectionOptions{Command: []string{"sh", "-c", "exit 3"}})
		_, err := connection.Info(background)
		connection.Close()
		if err == nil || err.Error() != "pi-env exited with code 3 before it was ready" || !IsConnectionLost(err) {
			t.Fatalf("Info() error = %v, want the lost error `pi-env exited with code 3 before it was ready`", err)
		}
	}
}

// packages/env/src/connection.ts #start: `Unsupported protocol ${json.protocol}` prints a missing field as JavaScript does; the daemon is the process ConnectionOptions.command starts.
// mutation-checked: start ignoring ConnectionOptions.Command (no daemon command) fails it.
func TestConnectionRefusesAHelloWithoutAProtocol(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	// The sync line, then a result for request 1 (the hello) whose JSON is `{}`; then the daemon waits.
	script := `printf 'PI-ENV %s\n' "$3"; printf '\000\000\000\013\002\000\000\000\001\000\000\000\002{}'; exec sleep 30`
	connection := NewConnection(ConnectionOptions{Command: []string{"sh", "-c", script, "sh"}})
	defer connection.Close()
	_, err := connection.Info(background)
	if err == nil || err.Error() != "Unsupported protocol undefined" {
		t.Fatalf("Info() error = %v, want `Unsupported protocol undefined`", err)
	}
}

// connection.ts #onData sets lastSeen on every chunk, so a large frame that arrives slowly counts as a live daemon.
func TestConnectionCountsBytesOfAFrameInProgressAsLife(t *testing.T) {
	command := exec.Command(daemonBinary(t), "serve", "--token", "idle")
	stdin := must(command.StdinPipe())
	mustDo(command.Start())
	reader, writer := must2(os.Pipe())
	s := &session{cmd: command, stdin: stdin, token: "token", pending: map[uint32]*pendingCall{}, live: true, exited: make(chan struct{}), stopPing: make(chan struct{})}
	connection := NewConnection(ConnectionOptions{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection.readLoop(s, reader)
	}()
	defer func() {
		_ = writer.Close()
		<-done
		_ = stdin.Close()
		_ = command.Wait()
	}()
	must(writer.WriteString("PI-ENV token\n"))
	waitUntil(t, func() bool { return s.lastSeen.Load() != 0 })
	s.lastSeen.Store(0)
	// The first bytes of a 100-byte frame.
	must(writer.Write([]byte{0, 0, 0, 100, 2, 0, 0, 0, 1}))
	waitUntil(t, func() bool { return s.lastSeen.Load() != 0 })
}

func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached within 5 s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// remote-env.ts watch and watch.ts open: the context of the call checks for an abort before and after opening; the
// watcher it returns lives until it is closed.
// Pi source: packages/env/src/remote-env.ts
// mutation-checked: zeroing the results of RemoteExecutionEnv.Cwd fails it
// Pi: packages/env/src/remote-env.ts:52 (cwd)
// packages/env/src/remote-env.ts:52-57: the remote environment has a cwd and a watch option; its watcher is not tied to the opening call.
func TestRemoteWatcherOutlivesTheContextThatOpenedIt(t *testing.T) {
	environment, _ := remoteEnvironment(t)
	directory := environment.Cwd()
	var seen changes
	ctx, cancel := context.WithCancel(background)
	watcher, err := environment.Watch(ctx, []durableenv.WatchTarget{{Path: directory}}, seen.add)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = watcher.Close(background) }()
	cancel()
	// Give a cancel that reached the daemon time to stop its watcher before the change.
	time.Sleep(200 * time.Millisecond)
	created := filepath.Join(directory, "after-cancel.txt")
	mustDo(os.WriteFile(created, []byte("x"), 0o600))
	seen.waitFor(t, 0, 10*time.Second, pathsInclude(created))
}

// remote-env.ts exec: a daemon `timeout` error is `timeout:${timeout}`, `timeout:undefined` without a timeout.
func TestExecFailureOfATimeoutWithoutATimeout(t *testing.T) {
	failure := execFailure(NewRemoteError(Json{"code": "timeout", "message": "timed out"}), nil)
	if failure.Code != durableenv.ExecutionErrorTimeout || failure.Message != "timeout:undefined" {
		t.Fatalf("execFailure = %s %q, want timeout `timeout:undefined`", failure.Code, failure.Message)
	}
	five := 5.0
	if failure := execFailure(NewRemoteError(Json{"code": "timeout"}), &five); failure.Message != "timeout:5" {
		t.Fatalf("execFailure message = %q, want `timeout:5`", failure.Message)
	}
}

// ssh.ts sshConnection: remote() reads the state; it does not wait for a start that is detecting or deploying.
func TestSshConnectionReportsItsRemoteWhileAStartIsDetecting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as ssh")
	}
	directory := t.TempDir()
	marker := filepath.Join(directory, "started")
	program := filepath.Join(directory, "ssh")
	mustDo(os.WriteFile(program, []byte("#!/bin/sh\n: > '"+marker+"'\nexec sleep 30\n"), 0o700))
	connection, remote := sshConnectionPair(SshConnectOptions{SshTarget: SshTarget{
		Host: "h", Ssh: &program, KnownHostsFile: filepath.Join(directory, "known_hosts"), HostKeyAlias: "pi-env-test",
	}})
	started := make(chan error, 1)
	go func() {
		_, err := connection.Info(background)
		started <- err
	}()
	waitUntil(t, func() bool { _, err := os.Stat(marker); return err == nil })
	answered := make(chan *RemotePlatform, 1)
	go func() { answered <- remote() }()
	select {
	case platform := <-answered:
		if platform != nil {
			t.Fatalf("remote() = %+v before detection finished", platform)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("remote() waited for the start")
	}
	connection.Close()
	if err := <-started; err == nil {
		t.Fatal("Info() succeeded after Close")
	}
}

// ssh.ts deployDaemon (POSIX): the upload runs the remote script as given, with its home taken from the remote, so a
// home with a quote and a space stays one path; an upload whose hash differs is removed and fails, leaving no daemon.
// The "ssh" here runs the remote command with the local sh, feeding it a truncated copy of stdin when told to.
// mutation-checked: dropping the reads and writes of SshError.Stderr, SshTarget.Host, SshTarget.HostKeyAlias, SshTarget.KnownHostsFile, SshTarget.Ssh fails it
func TestDeployDaemonRunsItsPosixScriptsAndRefusesACorruptUpload(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("runs the POSIX scripts with sh")
	}
	if _, err := exec.LookPath("sha256sum"); err != nil {
		if _, err := exec.LookPath("shasum"); err != nil {
			t.Skip("no sha256sum or shasum")
		}
	}
	directory := t.TempDir()
	home := filepath.Join(directory, "it's my home")
	mustDo(os.Mkdir(home, 0o700))
	program := filepath.Join(directory, "ssh")
	mustDo(os.WriteFile(program, []byte("#!/bin/sh\nfor last; do :; done\nif [ -e \"$PI_ENV_TEST_CORRUPT\" ]; then head -c 10 | sh -c \"$last\"; else sh -c \"$last\"; fi\n"), 0o700))
	corrupt := filepath.Join(directory, "corrupt")
	t.Setenv("PI_ENV_TEST_CORRUPT", corrupt)
	binary := filepath.Join(directory, "daemon")
	mustDo(os.WriteFile(binary, []byte(strings.Repeat("daemon bytes\n", 100)), 0o600))
	target := SshTarget{Host: "h", Ssh: &program, KnownHostsFile: filepath.Join(directory, "known_hosts"), HostKeyAlias: "pi-env-test"}
	remote := RemotePlatform{Platform: "linux", Arch: "x64", Home: home}
	tools := filepath.Join(home, ".pi", "mobile", "tools")

	mustDo(os.WriteFile(corrupt, nil, 0o600))
	_, err := DeployDaemon(background, target, remote, new(binary))
	var sshErr *SshError
	if !errors.As(err, &sshErr) || !strings.Contains(sshErr.Stderr, "pi-env upload is corrupt") {
		t.Fatalf("corrupt upload: %v, want an SshError reporting the corrupt upload", err)
	}
	if entries := must(os.ReadDir(tools)); len(entries) != 0 {
		t.Fatalf("a corrupt upload left %v", entries)
	}

	mustDo(os.Remove(corrupt))
	stale := filepath.Join(tools, "pi-env-0123")
	mustDo(os.WriteFile(stale, nil, 0o600))
	file, err := DeployDaemon(background, target, remote, new(binary))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(tools, deployedName(t, binary)); file != want {
		t.Fatalf("deployed to %q, want %q", file, want)
	}
	if got := string(must(os.ReadFile(file))); got != string(must(os.ReadFile(binary))) {
		t.Fatal("the deployed daemon differs from the binary")
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an older daemon was kept: %v", err)
	}
}

// ssh.ts normalizeArch: case-insensitive, with the spellings of uname and of .NET's OSArchitecture.
func TestNormalizeArch(t *testing.T) {
	for machine, want := range map[string]string{"x86_64": "x64", "AMD64": "x64", "amd64": "x64", "X64": "x64", "aarch64": "arm64", "ARM64": "arm64"} {
		if got, err := normalizeArch(machine); err != nil || got != want {
			t.Errorf("normalizeArch(%q) = %q, %v; want %q", machine, got, err, want)
		}
	}
	if _, err := normalizeArch("riscv64"); err == nil || err.Error() != "Unsupported remote architecture: riscv64" {
		t.Errorf("normalizeArch(riscv64) error = %v", err)
	}
}

// ssh.ts deployDaemon: only a binary that does not exist is "No pi-env daemon for ..."; reading one that exists
// fails with readFile's own error.
func TestDeployDaemonReportsAnUnreadableBinaryAsItsReadError(t *testing.T) {
	target, _ := fakeHost(t)
	directory := t.TempDir()
	_, err := DeployDaemon(background, target, RemotePlatform{Platform: "linux", Arch: "x64", Home: "/h"}, new(directory))
	if err == nil || strings.HasPrefix(err.Error(), "No pi-env daemon") {
		t.Fatalf("DeployDaemon of a directory: %v, want its read error", err)
	}
}

// packages/env/src/connection.ts:22-34 RemoteError: message, code, path and fields come from the daemon's error fields, with Pi's defaults when a field is absent or not a string.
// mutation-checked: a changed message or code default, a suffix on either value, a dropped path, and nil fields each fail it.
func TestRemoteErrorReadsDaemonFields(t *testing.T) {
	fields := Json{"message": "boom", "code": "ENOENT", "path": "/x", "extra": 1.0}
	got := NewRemoteError(fields)
	if got.Message != "boom" || got.Error() != "boom" || got.Code != "ENOENT" || got.Path != "/x" || got.Fields["extra"] != 1.0 {
		t.Fatalf("RemoteError = %+v", got)
	}
	defaults := NewRemoteError(Json{"message": 3.0, "code": true, "path": 7.0})
	if defaults.Message != "Remote operation failed" || defaults.Code != "unknown" || defaults.Path != "" {
		t.Fatalf("defaults = %+v", defaults)
	}
}
