package env

// pi: packages/env/src/ssh.ts

// Ports packages/env/test/ssh.test.ts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

// deployedName is what a daemon with this content is called on the remote machine.
func deployedName(t *testing.T, binary string) string {
	t.Helper()
	sum := sha256.Sum256(must(os.ReadFile(binary)))
	return "pi-env-" + hex.EncodeToString(sum[:])[:32]
}

func findSshd() string {
	if runtime.GOOS == "windows" {
		return ""
	}
	if path, err := exec.LookPath("sshd"); err == nil {
		return path
	}
	if _, err := os.Stat("/usr/sbin/sshd"); err == nil {
		return "/usr/sbin/sshd"
	}
	return ""
}

func freePort() int {
	listener := must(net.Listen("tcp", "127.0.0.1:0"))
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port
}

func waitForPort(t *testing.T, port int) {
	t.Helper()
	for range 100 {
		if connection, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			_ = connection.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("sshd did not start")
}

func run(t *testing.T, name string, args ...string) string {
	t.Helper()
	output, err := exec.Command(name, args...).Output()
	if err != nil {
		t.Fatalf("%s %v: %v", name, args, err)
	}
	return string(output)
}

func publicKey(t *testing.T, name string) string {
	t.Helper()
	return strings.Join(strings.Fields(string(must(os.ReadFile(name + ".pub"))))[:2], " ")
}

// Tests of the SSH bootstrap run against a disposable sshd on localhost with its own host key, client key and home
// directory, as Pi's do; without sshd (or ssh-keygen) they are skipped.
// Pi source: packages/env/src/ssh.ts
// mutation-checked: dropping the reads and writes of HostKeyUnknownError.Message, SshConnectOptions.Binary, SshConnectOptions.LoginShell fails it
func TestSSHBootstrap(t *testing.T) {
	sshd := findSshd()
	if sshd == "" {
		t.Skip("no sshd on this machine")
	}
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("no ssh-keygen on this machine")
	}
	daemon := daemonBinary(t)
	deployed := deployedName(t, daemon)
	root := t.TempDir()
	home := filepath.Join(root, "home")
	mustDo(os.MkdirAll(home, 0o700))
	run(t, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(root, "host_key"))
	run(t, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(root, "client_key"))
	mustDo(os.WriteFile(filepath.Join(root, "authorized_keys"), must(os.ReadFile(filepath.Join(root, "client_key.pub"))), 0o600))
	port := freePort()
	mustDo(os.WriteFile(filepath.Join(root, "sshd_config"), []byte(strings.Join([]string{
		fmt.Sprintf("Port %d", port),
		"ListenAddress 127.0.0.1",
		"HostKey " + filepath.Join(root, "host_key"),
		"AuthorizedKeysFile " + filepath.Join(root, "authorized_keys"),
		"PidFile " + filepath.Join(root, "sshd.pid"),
		"PasswordAuthentication no",
		"KbdInteractiveAuthentication no",
		"StrictModes no",
		"SetEnv HOME=" + home,
		"",
	}, "\n")), 0o600))
	mustDo(os.WriteFile(filepath.Join(root, "ssh_config"), nil, 0o600))
	server := exec.Command(sshd, "-D", "-e", "-f", filepath.Join(root, "sshd_config"))
	mustDo(server.Start())
	t.Cleanup(func() { _ = server.Process.Kill(); _ = server.Wait() })
	waitForPort(t, port)
	current := must(user.Current())
	target := SshTarget{
		Host: "127.0.0.1", Port: &port, User: &current.Username, IdentityFile: new(filepath.Join(root, "client_key")),
		KnownHostsFile: filepath.Join(root, "known_hosts"), HostKeyAlias: "pi-env-test", ConfigFile: new(filepath.Join(root, "ssh_config")),
	}
	ctx := context.Background()
	tools := filepath.Join(home, ".pi/mobile/tools")

	t.Run("refuses an untrusted host, then deploys and runs the daemon once its key is accepted", func(t *testing.T) {
		// upstream: packages/env/test/ssh.test.ts:118
		_, _, err := ConnectSsh(ctx, SshConnectOptions{SshTarget: target, Binary: new(daemon)})
		unknown, ok := errors.AsType[*HostKeyUnknownError](err)
		if !ok {
			t.Fatalf("untrusted host: %v, want HostKeyUnknownError", err)
		}
		// ssh.ts HostKeyUnknownError.message: "The host key of <host> is not trusted yet".
		if want := "The host key of " + target.Host + " is not trusted yet"; unknown.Message != want || err.Error() != want {
			t.Fatalf("HostKeyUnknownError message = %q / %q, want %q", unknown.Message, err.Error(), want)
		}
		lines, fingerprints := must2(ScanHostKey(ctx, target))
		expected := strings.Fields(run(t, "ssh-keygen", "-lf", filepath.Join(root, "host_key.pub")))[1]
		if !strings.Contains(strings.Join(fingerprints, "\n"), expected) {
			t.Fatalf("fingerprints %q do not contain %s", fingerprints, expected)
		}
		mustDo(AcceptHostKey(target, lines))

		mustDo(os.MkdirAll(tools, 0o700))
		mustDo(os.WriteFile(filepath.Join(tools, "pi-env-0123456789abcdef0123456789abcdef"), []byte("old"), 0o600))
		connection, remote, err := ConnectSsh(ctx, SshConnectOptions{SshTarget: target, Binary: new(daemon)})
		mustDo(err)
		defer connection.Close()
		if remote.Home != home {
			t.Fatalf("home %q, want %q", remote.Home, home)
		}
		if !bytes.Equal(must(os.ReadFile(filepath.Join(tools, deployed))), must(os.ReadFile(daemon))) {
			t.Fatal("the deployed daemon differs from the binary")
		}
		// Daemons of other contents are removed.
		var present []string
		for _, entry := range must(os.ReadDir(tools)) {
			if strings.HasPrefix(entry.Name(), "pi-env-") {
				present = append(present, entry.Name())
			}
		}
		if len(present) != 1 || present[0] != deployed {
			t.Fatalf("daemons %v, want only %s", present, deployed)
		}
		env := NewRemoteExecutionEnv(RemoteExecutionEnvOptions{Connection: connection, ID: "pi-env:test", Cwd: home})
		mustDo(env.WriteFile(ctx, "over-ssh.txt", "hello"))
		if text := must(env.ReadTextFile(ctx, "over-ssh.txt")); text != "hello" {
			t.Fatalf("read %q", text)
		}
		output := capture(t, env, []string{"sh", "-c", `printf "%s" "$HOME"`})
		if output != home {
			t.Fatalf("HOME %q, want %q", output, home)
		}
	})

	t.Run("reuses a verified daemon and replaces a tampered one", func(t *testing.T) {
		// upstream: packages/env/test/ssh.test.ts:153
		file := filepath.Join(tools, deployed)
		before := must(os.Stat(file)).ModTime()
		first, _, err := ConnectSsh(ctx, SshConnectOptions{SshTarget: target, Binary: new(daemon)})
		mustDo(err)
		first.Close()
		if after := must(os.Stat(file)).ModTime(); !after.Equal(before) {
			t.Fatalf("the verified daemon was uploaded again: %s -> %s", before, after)
		}
		// The daemon of the first connection may still be leaving, and Linux refuses to write a program in use, so
		// the content is replaced through a rename.
		mustDo(os.WriteFile(file+".tmp", []byte("tampered"), 0o700))
		mustDo(os.Rename(file+".tmp", file))
		connection, _, err := ConnectSsh(ctx, SshConnectOptions{SshTarget: target, Binary: new(daemon)})
		mustDo(err)
		defer connection.Close()
		if !bytes.Equal(must(os.ReadFile(file)), must(os.ReadFile(daemon))) {
			t.Fatal("the tampered daemon was not replaced")
		}
		if info := must(connection.Info(ctx)); info.Home != home {
			t.Fatalf("home %q", info.Home)
		}
	})

	t.Run("verifies the daemon again before starting it after a lost connection", func(t *testing.T) {
		// upstream: packages/env/test/ssh.test.ts:169
		connection, _, err := ConnectSsh(ctx, SshConnectOptions{SshTarget: target, Binary: new(daemon)})
		mustDo(err)
		defer connection.Close()
		file := filepath.Join(tools, deployed)
		info := must(connection.Info(ctx))
		mustDo(must(os.FindProcess(info.Pid)).Kill())
		time.Sleep(300 * time.Millisecond)
		// Linux refuses to write a running program's file.
		// The daemon of the first connection may still be leaving, and Linux refuses to write a program in use, so
		// the content is replaced through a rename.
		mustDo(os.WriteFile(file+".tmp", []byte("tampered"), 0o700))
		mustDo(os.Rename(file+".tmp", file))
		// The next start redeploys the verified binary instead of running whatever is there.
		if again := must(connection.Info(ctx)); again.Pid == info.Pid {
			t.Fatalf("the daemon was not started again: pid %d", again.Pid)
		}
		if !bytes.Equal(must(os.ReadFile(file)), must(os.ReadFile(daemon))) {
			t.Fatal("the tampered daemon was not replaced before the restart")
		}
	})

	t.Run("starts the daemon through the login shell only when asked", func(t *testing.T) {
		// upstream: packages/env/test/ssh.test.ts:186
		// The login shell is the account's shell: sh and bash read .profile, zsh (the macOS default) reads .zprofile.
		mustDo(os.WriteFile(filepath.Join(home, ".profile"), []byte("export PI_ENV_LOGIN=yes\n"), 0o600))
		mustDo(os.WriteFile(filepath.Join(home, ".zprofile"), []byte("export PI_ENV_LOGIN=yes\n"), 0o600))
		login := func(loginShell bool) string {
			connection, _, err := ConnectSsh(ctx, SshConnectOptions{SshTarget: target, Binary: new(daemon), LoginShell: loginShell})
			mustDo(err)
			defer connection.Close()
			env := NewRemoteExecutionEnv(RemoteExecutionEnvOptions{Connection: connection, ID: "pi-env:test", Cwd: home})
			return capture(t, env, []string{"sh", "-c", `printf "%s" "$PI_ENV_LOGIN"`})
		}
		if got := login(false); got != "" {
			t.Fatalf("without the login shell PI_ENV_LOGIN = %q", got)
		}
		if got := login(true); got != "yes" {
			t.Fatalf("with the login shell PI_ENV_LOGIN = %q", got)
		}
	})

	t.Run("connects lazily and reports a failed start as the error of the operation", func(t *testing.T) {
		// upstream: packages/env/test/ssh.test.ts:211
		lazyTarget := target
		lazyTarget.KnownHostsFile = filepath.Join(root, "lazy_known_hosts")
		connection, remote := sshConnectionPair(SshConnectOptions{SshTarget: lazyTarget, Binary: new(daemon)})
		defer connection.Close()
		if remote() != nil {
			t.Fatal("the remote system was detected before the first operation")
		}
		env := NewRemoteExecutionEnv(RemoteExecutionEnvOptions{Connection: connection, ID: "pi-env:test", Cwd: home})
		// Nothing is trusted yet: each operation fails with the reason and tries again.
		_, err := env.ReadTextFile(ctx, "missing.txt")
		if err == nil || !strings.Contains(err.Error(), "not trusted") {
			t.Fatalf("read: %v, want a not-trusted failure", err)
		}
		_, err = env.Exec(ctx, []string{"sh", "-c", "exit 0"}, nil)
		if code := errorCodeOf(err); code != "spawn_error" {
			t.Fatalf("exec code %s, want spawn_error", code)
		}
		lines, _ := must2(ScanHostKey(ctx, lazyTarget))
		mustDo(AcceptHostKey(lazyTarget, lines))
		accepted, err := env.Exec(ctx, []string{"sh", "-c", "exit 0"}, nil)
		if err != nil || accepted.ExitCode != 0 {
			t.Fatalf("exec after accepting: %v, %+v", err, accepted)
		}
		if detected := remote(); detected == nil || detected.Home != home {
			t.Fatalf("detected %+v", detected)
		}
	})

	t.Run("accepts only host keys for the alias and never replaces a trusted key silently", func(t *testing.T) {
		// upstream: packages/env/test/ssh.test.ts:231
		knownHosts := filepath.Join(root, "accept_known_hosts")
		scoped := target
		scoped.KnownHostsFile = knownHosts
		key := func(name string) string {
			file := filepath.Join(root, name)
			run(t, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", file)
			return publicKey(t, file)
		}
		first, second := key("accept_first"), key("accept_second")
		if err := AcceptHostKey(scoped, []string{"other-alias " + first}); err == nil {
			t.Fatal("a line for another alias was accepted")
		}
		if err := AcceptHostKey(scoped, []string{"@cert-authority pi-env-test " + first}); err == nil {
			t.Fatal("a certificate authority line was accepted")
		}
		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() { mustDo(AcceptHostKey(scoped, []string{"pi-env-test " + first})) })
		}
		wg.Wait()
		if got := string(must(os.ReadFile(knownHosts))); got != "pi-env-test "+first+"\n" {
			t.Fatalf("known hosts %q", got)
		}
		if _, ok := errors.AsType[*HostKeyChangedError](AcceptHostKey(scoped, []string{"pi-env-test " + second})); !ok {
			t.Fatal("a different key of the same type replaced a trusted key")
		}
		mustDo(ForgetHostKey(scoped))
		mustDo(AcceptHostKey(scoped, []string{"pi-env-test " + second}))
		if got := string(must(os.ReadFile(knownHosts))); got != "pi-env-test "+second+"\n" {
			t.Fatalf("known hosts %q", got)
		}
	})

	t.Run("refuses a changed host key", func(t *testing.T) {
		// upstream: packages/env/test/ssh.test.ts:254
		other := filepath.Join(root, "other_key")
		run(t, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", other)
		knownHosts := filepath.Join(root, "changed_known_hosts")
		mustDo(os.WriteFile(knownHosts, []byte("pi-env-test "+publicKey(t, other)+"\n"), 0o600))
		changed := target
		changed.KnownHostsFile = knownHosts
		_, _, err := ConnectSsh(ctx, SshConnectOptions{SshTarget: changed, Binary: new(daemon)})
		if _, ok := errors.AsType[*HostKeyChangedError](err); !ok {
			t.Fatalf("changed host key: %v, want HostKeyChangedError", err)
		}
	})
}

func must2[A, B any](first A, second B, err error) (A, B) {
	if err != nil {
		panic(err)
	}
	return first, second
}

// capture runs argv on env and returns its output.
func capture(t *testing.T, env *RemoteExecutionEnv, argv []string) string {
	t.Helper()
	var output strings.Builder
	var mu sync.Mutex
	result, err := env.Exec(context.Background(), argv, &durableenv.ShellExecOptions{OnOutput: func(_ context.Context, text string, _ durableenv.ShellOutputInfo) {
		mu.Lock()
		defer mu.Unlock()
		output.WriteString(text)
	}})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("%v: %v, %+v", argv, err, result)
	}
	mu.Lock()
	defer mu.Unlock()
	return output.String()
}
