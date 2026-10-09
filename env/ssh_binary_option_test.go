package env

import (
	"context"
	"testing"
)

// Pi packages/env/src/ssh.ts:399 deployDaemon(target, remote, binary = packagedDaemon(remote)) defaults only an undefined
// binary, and :401 checks existsSync(binary): a set empty path names no file, so it fails with that path instead of
// deploying the packaged daemon.
func TestDeployDaemonDefaultsOnlyAnUnsetBinary(t *testing.T) {
	ctx := context.Background()
	remote := RemotePlatform{Platform: "plan9", Arch: "sparc", Home: "/h"}
	target, _ := fakeHost(t)
	if _, err := DeployDaemon(ctx, target, remote, nil); err == nil || err.Error() != "No pi-env daemon for plan9-sparc at "+PackagedDaemon(remote) {
		t.Fatalf("unset binary: error %v, want the packaged daemon path", err)
	}
	if _, err := DeployDaemon(ctx, target, remote, new("")); err == nil || err.Error() != "No pi-env daemon for plan9-sparc at " {
		t.Fatalf("empty binary: error %v, want the empty path", err)
	}
}

// Pi ssh.ts:527 connectSsh deploys options.binary ?? packagedDaemon(remote), so SshConnectOptions.binary "" is used as given.
func TestConnectSshUsesASetEmptyBinary(t *testing.T) {
	target, _ := fakeHost(t, fakeRule{Match: probeCommand, Stdout: posixAnswer("Linux", "x86_64", "GNU/Linux", "/home/me", "-", "-")})
	_, _, err := ConnectSsh(context.Background(), SshConnectOptions{SshTarget: target, Binary: new("")})
	if err == nil || err.Error() != "No pi-env daemon for linux-x64 at " {
		t.Fatalf("ConnectSsh with Binary \"\": error %v, want the empty path", err)
	}
}

// Pi ssh.ts:492 sshConnection deploys options.binary ?? packagedDaemon(state.remote) on its first start, so the lazy
// connection also uses a set empty binary as given and the first operation fails with that path.
func TestSshConnectionUsesASetEmptyBinary(t *testing.T) {
	target, _ := fakeHost(t, fakeRule{Match: probeCommand, Stdout: posixAnswer("Linux", "x86_64", "GNU/Linux", "/home/me", "-", "-")})
	connection, _ := sshConnectionPair(SshConnectOptions{SshTarget: target, Binary: new("")})
	defer connection.Close()
	env := NewRemoteExecutionEnv(RemoteExecutionEnvOptions{Connection: connection, ID: "pi-env:test", Cwd: "/home/me"})
	_, err := env.Exists(context.Background(), "file")
	if err == nil || err.Error() != "No pi-env daemon for linux-x64 at " {
		t.Fatalf("first operation: error %v, want the empty binary path", err)
	}
}
