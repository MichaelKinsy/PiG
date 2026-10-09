package env

// Ports packages/env/test/ssh-external.test.ts
//
// The conformance suite over a real SSH server, configured by CI: PI_ENV_SSH_HOST (this gates the tests),
// PI_ENV_SSH_USER, PI_ENV_SSH_PORT, PI_ENV_SSH_KEY and PI_ENV_SSH_PROGRAM, and PI_ENV_SSH_SHELL for the Windows shell
// (cmd by default). On Windows this covers deployment and the daemon's launch through the server's default shell.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable/durabletest"
	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

type externalConnection struct {
	connection *Connection
	remote     RemotePlatform
}

var (
	externalOnce   sync.Once
	externalResult externalConnection
	externalErr    error
)

// connectExternal connects once to the server, logging each stage so a hang in CI shows where it is.
func connectExternal(t *testing.T) externalConnection {
	t.Helper()
	externalOnce.Do(func() {
		root, err := os.MkdirTemp("", "pi-env-ssh-external-")
		if err != nil {
			externalErr = err
			return
		}
		// ssh-external.test.ts:31-39 sets user, port, identityFile and ssh only for variables that are present.
		target := SshTarget{
			Host: os.Getenv("PI_ENV_SSH_HOST"), User: lookupEnv("PI_ENV_SSH_USER"), IdentityFile: lookupEnv("PI_ENV_SSH_KEY"),
			Ssh: lookupEnv("PI_ENV_SSH_PROGRAM"), KnownHostsFile: filepath.Join(root, "known_hosts"), HostKeyAlias: "pi-env-external",
		}
		if port, ok := os.LookupEnv("PI_ENV_SSH_PORT"); ok {
			number, _ := strconv.Atoi(port)
			target.Port = &number
		}
		log := func(format string, arguments ...any) {
			_, _ = fmt.Fprintf(os.Stderr, "[ssh-external %s] %s\n", time.Now().UTC().Format(time.RFC3339Nano), fmt.Sprintf(format, arguments...))
		}
		ctx := context.Background()
		log("scanning the host key")
		lines, _, scanErr := ScanHostKey(ctx, target)
		if scanErr != nil {
			externalErr = scanErr
			return
		}
		if externalErr = AcceptHostKey(target, lines); externalErr != nil {
			return
		}
		log("detecting the platform")
		remote, detectErr := DetectPlatform(ctx, target)
		if detectErr != nil {
			externalErr = detectErr
			return
		}
		log("detected %+v; deploying", remote)
		binary := daemonBinary(t)
		deployed, deployErr := DeployDaemon(ctx, target, remote, new(binary))
		if deployErr != nil {
			externalErr = deployErr
			return
		}
		log("deployed %s; connecting", deployed)
		connection, _, connectErr := ConnectSsh(ctx, SshConnectOptions{SshTarget: target, Binary: new(binary), OnLog: func(text string) { log("daemon: %s", text) }})
		if connectErr != nil {
			externalErr = connectErr
			return
		}
		log("connected; saying hello")
		info, infoErr := connection.Info(ctx)
		if infoErr != nil {
			externalErr = infoErr
			return
		}
		log("hello %+v", info)
		externalResult = externalConnection{connection: connection, remote: remote}
	})
	if externalErr != nil {
		t.Fatalf("connecting to %s: %v", os.Getenv("PI_ENV_SSH_HOST"), externalErr)
	}
	return externalResult
}

// packages/env/src/ssh.ts:462,486,500: SshConnectOptions.onLog receives the daemon log text and the host warnings (the connection helper passes it).
func TestSSHToThisMachinesServer(t *testing.T) {
	if os.Getenv("PI_ENV_SSH_HOST") == "" {
		t.Skip("PI_ENV_SSH_HOST names no server")
	}
	t.Cleanup(func() {
		if externalResult.connection != nil {
			externalResult.connection.Close()
		}
	})
	t.Run("reaches the remote system through its default shell", func(t *testing.T) {
		// upstream: packages/env/test/ssh-external.test.ts:65
		external := connectExternal(t)
		info := must(external.connection.Info(context.Background()))
		want := runtime.GOOS
		if want == "darwin" {
			want = "macos"
		}
		if info.OS != want {
			t.Fatalf("os %q, want %q", info.OS, want)
		}
		if runtime.GOOS == "windows" {
			shell := os.Getenv("PI_ENV_SSH_SHELL")
			if shell == "" {
				shell = "cmd"
			}
			if external.remote.Shell != shell {
				t.Fatalf("shell %q, want %q", external.remote.Shell, shell)
			}
		}
	})
	shell, noSymlinks := conformanceShell()
	// upstream: packages/env/test/ssh-external.test.ts:72
	durabletest.RegisterEnvConformance(t, "RemoteExecutionEnv over SSH", func(use func(durableenv.ExecutionEnv) error) error {
		external := connectExternal(t)
		ctx := context.Background()
		info := must(external.connection.Info(ctx))
		home := NewRemoteExecutionEnv(RemoteExecutionEnvOptions{Connection: external.connection, ID: "pi-env:external", Cwd: info.Home})
		prefix := "pi-env-conformance-"
		cwd := must(home.CreateTempDir(ctx, &prefix))
		defer func() { _ = home.Remove(ctx, cwd, &durableenv.RemoveOptions{Recursive: true, Force: true}) }()
		return use(NewRemoteExecutionEnv(RemoteExecutionEnvOptions{
			Connection: external.connection, ID: "pi-env:external", Cwd: cwd, Watch: RemoteWatchOptions{PollIntervalMs: new(100)},
		}))
	}, durabletest.EnvConformanceRegisterOptions{Shell: shell, Symlinks: new(!noSymlinks)})
}

// lookupEnv is the variable's value, or nil when it is not set.
func lookupEnv(name string) *string {
	if value, ok := os.LookupEnv(name); ok {
		return &value
	}
	return nil
}
